// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// apiChecker grants access to an endpoint for a method.  A full capability
// looks like
//
//	example-prefix:api:device/.*/config:get
//	└──────────┬───────┘└──────┬─────┘ └┬┘
//	         prefix         endpoint  method
//
// but the checker only sees what follows the configured prefix: the approver
// strips the prefix before calling Compile, so the specs compiled here are of
// the form "{endpoint}:{method}", e.g. "device/.*/config:get".
//
// endpoint is a regular expression matched against the request path, anchored
// at the start of the path.  method is a lowercased HTTP verb, or the
// configured accept-all value that grants every method.
//
// These semantics match basculechecks.RegexEndpointCheck, which enforced them
// before the capability checks were removed, so tokens already in circulation
// keep working.
type apiChecker struct {
	acceptAllMethod string
}

// capabilityLabelAPI is the value recorded on the capability_check metric for
// this check.  It is unrelated to apiLabel in metrics.go, a Prometheus label
// name that happens to share the same value.
const capabilityLabelAPI = "api"

// httpMethods are the HTTP verbs a capability's method may be.  The method must
// be one of these or the configured accept-all value; validating it means a
// typo is rejected at startup rather than silently never matching a request.
var httpMethods = []string{
	"get", "head", "post", "put", "patch", "delete", "connect", "options", "trace",
}

// errNoCapability is returned when nothing grants the request.
var errNoCapability = errors.New("no capability grants this endpoint and method")

// Compile turns each capability spec into a grant.  The specs have already had
// the prefix removed, so each is "{endpoint}:{method}".  The spec is split at
// its final colon: the method is the segment after it, the endpoint is
// everything before.  The method is then validated -- it must be an HTTP verb
// or the accept-all value -- so a typo is caught here rather than silently
// never matching.  Worked examples, with accept-all "all":
//
//	"device/.*/config:get"    -> endpoint /device/.*/config,   method "get"
//	"device/.*/config:all"    -> endpoint /device/.*/config,   method "all"
//	"hook:post"               -> endpoint /hook,               method "post"
//	"device/mac:.*/stat:get"  -> endpoint /device/mac:.*/stat, method "get"
//	                             (only the final colon splits, so the ":" in
//	                              "mac:" stays with the endpoint)
//	"device/foo"              -> error: no method
//	"device/foo:frobnicate"   -> error: "frobnicate" is not a known method
//	":get"                    -> error: empty endpoint
func (c apiChecker) Compile(specs []string) (Check, error) {
	grants := make([]apiGrant, 0, len(specs))
	for _, spec := range specs {
		endpoint, method, err := c.parseSpec(spec)
		if err != nil {
			return nil, err
		}

		re, err := regexp.Compile(normalizePath(endpoint))
		if err != nil {
			return nil, fmt.Errorf("api capability %q has an invalid endpoint: %w", spec, err)
		}

		grants = append(grants, apiGrant{endpoint: re, method: method})
	}

	return apiCheck{grants: grants, acceptAllMethod: c.acceptAllMethod}, nil
}

// parseSpec splits an "{endpoint}:{method}" spec at its final colon and
// validates the method.  The accept-all value is accepted here as a method;
// whether it acts as a wildcard is decided later, in Allow.
func (c apiChecker) parseSpec(spec string) (endpoint, method string, err error) {
	i := strings.LastIndex(spec, ":")
	if i < 0 {
		return "", "", fmt.Errorf("api capability %q is missing a method", spec)
	}

	endpoint, method = spec[:i], spec[i+1:]
	if endpoint == "" {
		return "", "", fmt.Errorf("api capability %q has an empty endpoint", spec)
	}
	if !c.knownMethod(method) {
		return "", "", fmt.Errorf("api capability %q has an unknown method %q", spec, method)
	}

	return endpoint, method, nil
}

// knownMethod reports whether method is an HTTP verb or the accept-all value.
func (c apiChecker) knownMethod(method string) bool {
	if method == c.acceptAllMethod {
		return true
	}
	for _, m := range httpMethods {
		if m == method {
			return true
		}
	}
	return false
}

type apiGrant struct {
	endpoint *regexp.Regexp
	method   string
}

type apiCheck struct {
	grants          []apiGrant
	acceptAllMethod string
}

// Allow permits the request when any single grant covers it.
//
// Worked example, for a grant {endpoint: /device/.*/config, method: "get"}:
//
//	GET /api/v3/device/mac:112233445566/config
//	  stripAPIBase  -> /device/mac:112233445566/config   (drop /api/v3)
//	  normalizePath -> /device/mac:112233445566/config   (already rooted)
//	  method "get" matches; regex matches at index 0     -> allowed
//
//	PATCH /api/v3/device/mac:.../config                  -> denied: method "get" != "patch"
//	GET   /api/v3/device/mac:.../stat                    -> denied: regex does not match
//	GET   /api/v3/device/mac:.../configXYZ               -> ALLOWED: the pattern is
//	     only start-anchored, so it matches the prefix; a capability that must
//	     stop at a boundary has to say so, e.g. "device/.*/config\b:get".
func (c apiCheck) Allow(r *http.Request) error {
	if r.Method == "" {
		return errors.New("request has no method")
	}

	path := normalizePath(stripAPIBase(r.URL.EscapedPath()))
	method := strings.ToLower(r.Method)

	for _, g := range c.grants {
		if g.method != c.acceptAllMethod && g.method != method {
			continue
		}

		// The endpoint pattern must match from the start of the path.  A
		// match anywhere else would let a capability for one endpoint grant
		// access to an unrelated one that merely contains it.
		if loc := g.endpoint.FindStringIndex(path); loc != nil && loc[0] == 0 {
			return nil
		}
	}

	return errNoCapability
}

// apiBasePrefixes are stripped from a request path before it is matched
// against a capability's endpoint pattern.  Capabilities are written relative
// to the API root -- "device/.*/config", not "/api/v3/device/.*/config" -- so
// the version prefix has to come off first.  This mirrors what
// createRemovePrefixURLFuncLegacy did before the capability checks were
// removed, and is what keeps capabilities already in circulation working.
var apiBasePrefixes = []string{"/" + apiBase, "/" + prevAPIBase}

// stripAPIBase removes the API version prefix from a request path.
func stripAPIBase(path string) string {
	for _, prefix := range apiBasePrefixes {
		if rest, found := strings.CutPrefix(path, prefix); found {
			return rest
		}
	}
	return path
}

// normalizePath gives both the capability's endpoint pattern and the request
// path a leading slash so the two are compared on the same footing.
func normalizePath(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}
