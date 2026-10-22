// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The response headers the suite looks at.
const (
	transactionIDHeader = "X-Webpa-Transaction-Id"
	warningHeader       = "X-Webpa-Capability-Warning"
	v2WarningHeader     = "X-Xmidt-Warning"
)

// httpClient is shared so connections are reused.  It never follows a
// redirect, so a test sees the redirect itself.
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// apiResponse is a fully-read HTTP response.
type apiResponse struct {
	Status int
	Header http.Header
	Body   string
}

// message returns the "message" field of a JSON error body, or "" when the
// body is not that shape.
func (r apiResponse) message() string {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		return ""
	}

	return body.Message
}

// request is one API call.  An empty Token sends no Authorization header; an
// empty Body sends none.  A JSON content type is added to a body unless
// Headers gives one.
type request struct {
	Method  string
	URL     string
	Token   string
	Headers map[string]string
	Body    string
}

// send performs one request.
func send(t *testing.T, r request) apiResponse {
	t.Helper()

	var reader io.Reader
	if r.Body != "" {
		reader = strings.NewReader(r.Body)
	}
	req, err := http.NewRequest(r.Method, r.URL, reader)
	require.NoError(t, err)
	if r.Token != "" {
		req.Header.Set("Authorization", r.Token)
	}
	if r.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range r.Headers {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	require.NoError(t, err, "%s %s", r.Method, r.URL)
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	require.NoError(t, err)

	return apiResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(data)}
}

// get is send for a GET with only a token.
func get(t *testing.T, url, tok string) apiResponse {
	t.Helper()
	return send(t, request{Method: http.MethodGet, URL: url, Token: tok})
}

// URL builders for the primary server.  version is v2 or v3.
func (a *app) statURL(version, id string) string {
	return fmt.Sprintf("%s/api/%s/device/%s/stat", a.primary, version, id)
}

func (a *app) configURL(version, id string) string {
	return fmt.Sprintf("%s/api/%s/device/%s/config", a.primary, version, id)
}

func (a *app) tableURL(version, id, table string) string {
	return fmt.Sprintf("%s/api/%s/device/%s/config/%s", a.primary, version, id, table)
}

func (a *app) hookURL(version string) string {
	return fmt.Sprintf("%s/api/%s/hook", a.primary, version)
}

func (a *app) hooksURL(version string) string {
	return fmt.Sprintf("%s/api/%s/hooks", a.primary, version)
}

// assertErrorContract verifies tr1d1um's error response contract: the status,
// a JSON content type, a transaction ID and a JSON {"message": ...} body that
// contains wantMessage.
func assertErrorContract(t *testing.T, r apiResponse, wantStatus int, wantMessage string) {
	t.Helper()
	assert.Equal(t, wantStatus, r.Status, "body: %s", r.Body)
	assert.True(t, strings.HasPrefix(r.Header.Get("Content-Type"), "application/json"),
		"error responses should be JSON, got Content-Type %q", r.Header.Get("Content-Type"))
	assert.NotEmpty(t, r.Header.Get(transactionIDHeader), "error responses should carry a transaction ID")
	if wantMessage != "" {
		assert.Contains(t, r.message(), wantMessage, "body: %s", r.Body)
	}
}

// rawRequest sends a request whose request-target is written to the wire
// exactly as given, bypassing Go's URL normalization, so encoded and
// malformed paths reach the server as written.
func rawRequest(t *testing.T, a *app, method, target, tok string) apiResponse {
	t.Helper()

	conn, err := net.DialTimeout("tcp", hostPort(a.primary), 5*time.Second)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: tr1d1um-compliance\r\nConnection: close\r\n", method, target, hostPort(a.primary))
	if tok != "" {
		fmt.Fprintf(&b, "Authorization: %s\r\n", tok)
	}
	b.WriteString("\r\n")
	_, err = io.WriteString(conn, b.String())
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err, "raw request %s", target)
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	require.NoError(t, err)

	return apiResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(data)}
}

// randomHex returns 2*n random hex characters.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}

	return hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// Metrics.
// ---------------------------------------------------------------------------

// scrape reads the instance's metrics in the Prometheus text format.
func scrape(t *testing.T, a *app) string {
	t.Helper()
	resp := get(t, a.metrics+"/metrics", "")
	require.Equal(t, http.StatusOK, resp.Status)

	return resp.Body
}

// counter sums the samples of a metric whose labels include every pair in
// labels, in a scrape.
func counter(scraped, name string, labels map[string]string) float64 {
	var total float64
	for line := range strings.SplitSeq(scraped, "\n") {
		if !strings.HasPrefix(line, name+"{") && !strings.HasPrefix(line, name+" ") {
			continue
		}
		rest := strings.TrimPrefix(line, name)
		var labelText string
		if strings.HasPrefix(rest, "{") {
			end := strings.LastIndex(rest, "}")
			if end < 0 {
				continue
			}
			labelText, rest = rest[1:end], rest[end+1:]
		}
		if !hasLabels(labelText, labels) {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil {
			continue
		}
		total += value
	}

	return total
}

// hasLabels reports whether a metric's label text has every wanted pair.
func hasLabels(labelText string, want map[string]string) bool {
	got := map[string]string{}
	for pair := range strings.SplitSeq(labelText, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		// the text format escapes backslashes, quotes and newlines
		got[strings.TrimSpace(k)] = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n").Replace(v)
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}

	return true
}

// counterDelta runs do and returns how much the counter grew.
func counterDelta(t *testing.T, a *app, name string, labels map[string]string, do func()) float64 {
	t.Helper()
	before := counter(scrape(t, a), name, labels)
	do()

	return counter(scrape(t, a), name, labels) - before
}

// Metric names, as the prometheus section of tr1d1um.yaml namespaces them.
// They are spelled out because a counter with no samples yet is absent from
// a scrape altogether.
const (
	metricPrefix       = "webpa_tr1d1um_"
	checkMetric        = metricPrefix + "auth_capability_check"
	warningMetric      = metricPrefix + "auth_capability_warning"
	requestCountMetric = metricPrefix + "server_request_count"
)
