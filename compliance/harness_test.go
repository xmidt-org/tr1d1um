// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

// Package compliance holds the compliance suite: black-box tests that run
// the real tr1d1um binary against fakes of everything it talks to, and check
// what a caller of the WebPA API sees.
//
// TestMain builds tr1d1um once.  Each test asks for an instance by profile
// (appFor); an instance is started on first use with the profile's
// configuration and stopped when the suite ends.  All instances share one
// fake XMiDT, one fake Argus and one signing key.
package compliance

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// The shared pieces every instance uses.
var (
	binaryPath string
	workDir    string
	xmidt      *fakeXmidt
	argus      *fakeArgus
	signer     *tokenSigner
	instances  = &registry{apps: map[string]*app{}}
)

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	var err error
	workDir, err = os.MkdirTemp("", "tr1d1um-compliance-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "compliance: temp dir:", err)
		return 1
	}
	defer os.RemoveAll(workDir)

	binaryPath, err = buildBinary(workDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "compliance: build tr1d1um:", err)
		return 1
	}

	signer, err = newTokenSigner()
	if err != nil {
		fmt.Fprintln(os.Stderr, "compliance: signing key:", err)
		return 1
	}

	xmidt = newFakeXmidt()
	defer xmidt.Close()
	argus = newFakeArgus()
	defer argus.Close()
	defer instances.stopAll()

	return m.Run()
}

// buildBinary compiles tr1d1um from the module root into dir.  The race
// detector is used when this test binary was built with it.
func buildBinary(dir string) (string, error) {
	out := filepath.Join(dir, "tr1d1um")
	args := []string{"build", "-o", out}
	if raceBuild {
		args = append(args, "-race")
	}
	args = append(args, "github.com/xmidt-org/tr1d1um")

	cmd := exec.Command("go", args...) //nolint:gosec // the arguments are the suite's own
	cmd.Dir = ".."
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}

	return out, nil
}

// A profile is one way of configuring tr1d1um.  Its overrides are merged
// over tr1d1um.yaml; the keys are viper paths.
type profile struct {
	name      string
	overrides map[string]any
}

// modes sets the mode of every capability kind at once.
func modes(cidr, rate, param string) map[string]any {
	return map[string]any{
		"auth": map[string]any{
			"inbound": map[string]any{
				"jwt": map[string]any{
					"cidr":  map[string]any{"mode": cidr},
					"rate":  map[string]any{"mode": rate},
					"param": map[string]any{"mode": param},
				},
			},
		},
	}
}

// The profiles the suite runs.  Most tests use enforce, where a token's
// own capabilities decide; the others exist to see each mode's behavior.
var (
	// enforce rejects what fails and tells the caller about malformed
	// capabilities.  Also the profile for everything not about modes.
	enforce = profile{name: "enforce", overrides: modes("enforce", "enforce", "enforce")}

	// warn lets everything through and tells the caller what enforce would
	// have rejected.
	warn = profile{name: "warn", overrides: modes("permissiveWithWarnings", "permissiveWithWarnings", "permissiveWithWarnings")}

	// silent lets everything through and tells the caller nothing.
	silent = profile{name: "silent", overrides: modes("permissive", "permissive", "permissive")}

	// required is enforce, and a token with no CIDR capability is refused.
	required = profile{name: "required", overrides: modes("required", "enforce", "enforce")}

	// requiredWarn is the trial run of required, with warnings.
	requiredWarn = profile{name: "requiredWarn", overrides: modes("permissiveRequiredWithWarnings", "enforce", "enforce")}

	// requiredSilent is the trial run of required, without warnings.
	requiredSilent = profile{name: "requiredSilent", overrides: modes("permissiveRequired", "enforce", "enforce")}

	// proxied trusts the loopback peer to report where a request came from,
	// so forwarding headers choose the caller origin.
	proxied = profile{name: "proxied", overrides: map[string]any{
		"auth": map[string]any{"inbound": map[string]any{"jwt": map[string]any{
			"cidr": map[string]any{"mode": "enforce", "trustedProxies": []string{"127.0.0.0/8"}},
		}}},
	}}

	// rateDefaults holds every caller without a rate capability to a small
	// default rate.
	rateDefaults = profile{name: "rateDefaults", overrides: map[string]any{
		"auth": map[string]any{"inbound": map[string]any{"jwt": map[string]any{
			"rate": map[string]any{"mode": "enforce", "defaults": []string{"3/1m"}},
		}}},
	}}

	// basicAuth accepts one basic credential and no tokens at all.
	basicAuth = profile{name: "basic", overrides: map[string]any{
		"auth": map[string]any{"inbound": map[string]any{
			"jwt":   nil,
			"basic": []string{basicCredential},
		}},
	}}
)

// basicCredential is the base64 of "user:pass", the credential the basicAuth
// profile accepts.
const basicCredential = "dXNlcjpwYXNz" //nolint:gosec // a test credential

// app is a running tr1d1um instance.
type app struct {
	profile profile
	cmd     *exec.Cmd
	logPath string

	// exited is closed once the process has ended; exitErr is then its
	// exit error.
	exited  chan struct{}
	exitErr error

	// The base URL of each server, http://127.0.0.1:port.
	primary   string
	alternate string
	health    string
	metrics   string
	pprof     string
}

// registry starts instances on demand and stops them all at the end.
type registry struct {
	lock sync.Mutex
	apps map[string]*app
}

// appFor returns the running instance for the profile, starting it if
// needed.  When the test fails, the tail of the instance's log is printed.
func appFor(t *testing.T, p profile) *app {
	t.Helper()

	instances.lock.Lock()
	defer instances.lock.Unlock()

	a, ok := instances.apps[p.name]
	if !ok {
		var err error
		a, err = startApp(p)
		require.NoError(t, err, "start tr1d1um with the %s profile", p.name)
		instances.apps[p.name] = a
	}

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("tail of the %s instance log:\n%s", a.profile.name, a.logTail(8))
		}
	})

	return a
}

func (r *registry) stopAll() {
	r.lock.Lock()
	defer r.lock.Unlock()

	for _, a := range r.apps {
		a.stop()
	}
}

// startApp writes the profile's configuration and starts tr1d1um with it,
// returning once the health server answers.
func startApp(p profile) (*app, error) {
	ports, err := freePorts(5)
	if err != nil {
		return nil, err
	}

	a := &app{
		profile:   p,
		logPath:   filepath.Join(workDir, p.name+".log"),
		primary:   "http://" + ports[0],
		alternate: "http://" + ports[1],
		health:    "http://" + ports[2],
		metrics:   "http://" + ports[3],
		pprof:     "http://" + ports[4],
		exited:    make(chan struct{}),
	}

	configPath, err := a.writeConfig(ports)
	if err != nil {
		return nil, err
	}

	a.cmd = exec.Command(binaryPath, "-f", configPath) //nolint:gosec // the binary was built above
	a.cmd.Stdout = os.Stdout
	a.cmd.Stderr = os.Stderr
	if err := a.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", binaryPath, err)
	}
	go func() {
		a.exitErr = a.cmd.Wait()
		close(a.exited)
	}()

	if err := a.waitHealthy(10 * time.Second); err != nil {
		a.stop()
		return nil, fmt.Errorf("%w\n%s", err, a.logTail(40))
	}

	return a, nil
}

// writeConfig merges the run-time values and the profile's overrides over
// tr1d1um.yaml and writes the result for the instance.  The merge is done
// on the YAML tree itself, since viper would split map keys that contain a
// dot, such as a parameter pattern.
func (a *app) writeConfig(ports []string) (string, error) {
	base, err := os.ReadFile("tr1d1um.yaml")
	if err != nil {
		return "", err
	}

	var settings map[string]any
	if err := yaml.Unmarshal(base, &settings); err != nil {
		return "", fmt.Errorf("tr1d1um.yaml: %w", err)
	}

	runtime := map[string]any{
		"servers": map[string]any{
			"primary":   map[string]any{"address": ports[0]},
			"alternate": map[string]any{"address": ports[1]},
			"health":    map[string]any{"address": ports[2]},
			"metrics":   map[string]any{"address": ports[3]},
			"pprof":     map[string]any{"address": ports[4]},
		},
		"logging":   map[string]any{"outputPaths": []string{a.logPath}},
		"webhook":   map[string]any{"client": map[string]any{"storeBaseURL": argus.URL()}},
		"targetURL": xmidt.URL() + "/api/v3",
		"auth": map[string]any{"inbound": map[string]any{"jwt": map[string]any{
			"clortho": map[string]any{"fixed": []any{map[string]any{
				"keys": []any{map[string]any{"keyID": signer.keyID, "key": signer.publicPEM}},
			}}},
		}}},
	}

	settings = merge(settings, runtime)
	settings = merge(settings, a.profile.overrides)

	out, err := yaml.Marshal(settings)
	if err != nil {
		return "", err
	}

	path := filepath.Join(workDir, a.profile.name+".yaml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return "", err
	}

	return path, nil
}

// merge overlays src on dst, map by map.  A nil value in src removes the
// key, so a profile can drop a whole section.  Anything else in src
// replaces what dst has.
func merge(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		switch tv := v.(type) {
		case nil:
			delete(dst, k)
		case map[string]any:
			existing, _ := dst[k].(map[string]any)
			dst[k] = merge(existing, tv)
		default:
			dst[k] = v
		}
	}

	return dst
}

// waitHealthy polls the health server until it answers 200.
func (a *app) waitHealthy(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	for {
		select {
		case <-a.exited:
			return fmt.Errorf("tr1d1um exited before it was healthy: %v", a.exitErr)
		default:
		}

		resp, err := client.Get(a.health + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("tr1d1um (%s) not healthy after %s: %v", a.profile.name, timeout, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stop ends the instance as a deployment would, with SIGTERM.
func (a *app) stop() {
	if a.cmd == nil || a.cmd.Process == nil {
		return
	}
	_ = a.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-a.exited:
	case <-time.After(5 * time.Second):
		_ = a.cmd.Process.Kill()
		<-a.exited
	}
}

// logTail returns the last n lines of the instance's log.
func (a *app) logTail(n int) string {
	data, err := os.ReadFile(a.logPath)
	if err != nil {
		return "(no log: " + err.Error() + ")"
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	return strings.Join(lines, "\n")
}

// freePorts finds n free loopback addresses.  Each is bound and released, so
// another process could take one in between; the window is tiny and a
// collision fails the start, which the test reports.
func freePorts(n int) ([]string, error) {
	addrs := make([]string, 0, n)
	listeners := make([]net.Listener, 0, n)
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()

	var lc net.ListenConfig
	for range n {
		l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, l)
		addrs = append(addrs, l.Addr().String())
	}

	return addrs, nil
}

// hostPort returns the host:port of a base URL.
func hostPort(baseURL string) string {
	return strings.TrimPrefix(baseURL, "http://")
}
