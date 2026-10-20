// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const testSetupConfig = `
targetURL: "http://xmidt.example.com"
logging:
  level: info
`

// writeConfig writes contents to name in dir and returns its path.
func writeConfig(t *testing.T, dir, name, contents string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))

	return path
}

// isolateSearchPath makes the working directory and $HOME an empty directory,
// so that only /etc/tr1d1um is left on the config search path.
func isolateSearchPath(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", dir)

	return dir
}

// flags parses args as tr1d1um's command line flags.
func flags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()

	fs, err := parseFlags(args)
	require.NoError(t, err)

	return fs
}

func TestParseFlags(t *testing.T) {
	t.Run("flags", func(t *testing.T) {
		fs := flags(t, applicationName, "-f", "c.yaml", "-d", "-v")

		file, err := fs.GetString("file")
		require.NoError(t, err)
		assert.Equal(t, "c.yaml", file)

		debug, err := fs.GetBool("debug")
		require.NoError(t, err)
		assert.True(t, debug)

		version, err := fs.GetBool("version")
		require.NoError(t, err)
		assert.True(t, version)
	})

	t.Run("defaults", func(t *testing.T) {
		fs := flags(t)

		file, _ := fs.GetString("file")
		debug, _ := fs.GetBool("debug")
		version, _ := fs.GetBool("version")
		assert.Empty(t, file)
		assert.False(t, debug)
		assert.False(t, version)
	})

	t.Run("unknown flag", func(t *testing.T) {
		fs, err := parseFlags([]string{applicationName, "--nope"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse args")
		assert.Nil(t, fs)
	})
}

func TestSetup(t *testing.T) {
	t.Run("config file", func(t *testing.T) {
		path := writeConfig(t, t.TempDir(), "c.yaml", testSetupConfig)

		v, l, err := setup(flags(t, "--file", path, "--debug"))
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.NotNil(t, l)

		assert.Equal(t, "http://xmidt.example.com", v.GetString(targetURLKey), "the file is read")
		assert.Equal(t, defaults[wrpSourceKey], v.Get(wrpSourceKey), "defaults fill the gaps")
	})

	t.Run("search path", func(t *testing.T) {
		dir := isolateSearchPath(t)
		writeConfig(t, dir, applicationName+".yaml", testSetupConfig)

		v, l, err := setup(flags(t))
		require.NoError(t, err)
		assert.NotNil(t, l)
		assert.Equal(t, "http://xmidt.example.com", v.GetString(targetURLKey))
	})

	t.Run("search path from home", func(t *testing.T) {
		dir := isolateSearchPath(t)
		home := filepath.Join(dir, "."+applicationName)
		require.NoError(t, os.Mkdir(home, 0o700))
		writeConfig(t, home, applicationName+".yaml", testSetupConfig)

		v, _, err := setup(flags(t))
		require.NoError(t, err)
		assert.Equal(t, "http://xmidt.example.com", v.GetString(targetURLKey))
	})

	t.Run("missing config file", func(t *testing.T) {
		_, l, err := setup(flags(t, "-f", filepath.Join(t.TempDir(), "missing.yaml")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read config file")
		assert.Nil(t, l)
	})

	t.Run("no config on the search path", func(t *testing.T) {
		if _, err := os.Stat("/etc/" + applicationName); err == nil {
			t.Skip("/etc/" + applicationName + " exists on this machine")
		}
		isolateSearchPath(t)

		_, _, err := setup(flags(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read config file")
	})

	t.Run("invalid logging config", func(t *testing.T) {
		path := writeConfig(t, t.TempDir(), "c.yaml", "logging: not-a-map\n")

		_, l, err := setup(flags(t, "-f", path))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read logging config")
		assert.Nil(t, l)
	})
}

// TestSetup_Debug checks that --debug enables debug logging, overriding the
// configured level, and keeps the rest of the logging configuration.
func TestSetup_Debug(t *testing.T) {
	tests := []struct {
		name      string
		level     string
		debug     bool
		wantDebug bool
	}{
		{name: "configured level", level: "info"},
		{name: "debug flag overrides the configured level", level: "info", debug: true, wantDebug: true},
		{name: "debug flag with no configured level", debug: true, wantDebug: true},
		{name: "configured debug level", level: "debug", wantDebug: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			logFile := filepath.Join(dir, "tr1d1um.log")

			config := "logging:\n  outputPaths:\n    - " + logFile + "\n"
			if tc.level != "" {
				config += "  level: " + tc.level + "\n"
			}

			args := []string{"-f", writeConfig(t, dir, "c.yaml", config)}
			if tc.debug {
				args = append(args, "-d")
			}

			_, l, err := setup(flags(t, args...))
			require.NoError(t, err)
			assert.Equal(t, tc.wantDebug, l.Core().Enabled(zap.DebugLevel))

			// the configured output path is still used
			l.Debug("debug message")
			l.Info("info message")
			require.NoError(t, l.Sync())

			written, err := os.ReadFile(filepath.Clean(logFile))
			require.NoError(t, err)
			assert.Contains(t, string(written), "info message")
			if tc.wantDebug {
				assert.Contains(t, string(written), "debug message")
			} else {
				assert.NotContains(t, string(written), "debug message")
			}
		})
	}
}

// TestVersionWithoutConfig checks that the version can be printed before
// tr1d1um is configured.
func TestVersionWithoutConfig(t *testing.T) {
	if _, err := os.Stat("/etc/" + applicationName); err == nil {
		t.Skip("/etc/" + applicationName + " exists on this machine")
	}

	for _, flag := range []string{"-v", "--version"} {
		t.Run(flag, func(t *testing.T) {
			isolateSearchPath(t)
			assert.Equal(t, 0, tr1d1um([]string{applicationName, flag}))
		})
	}
}

func TestTr1d1um_BadFlag(t *testing.T) {
	// the flags are checked before the version or the configuration
	assert.Equal(t, 1, tr1d1um([]string{applicationName, "--nope", "-v"}))
}

func TestPrintVersion(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "short flag", args: []string{applicationName, "-v"}, want: true},
		{name: "long flag", args: []string{applicationName, "--version"}, want: true},
		{name: "with other flags", args: []string{applicationName, "-d", "-f", "missing.yaml", "-v"}, want: true},
		{name: "not asked", args: []string{applicationName, "-d"}},
		{name: "no arguments"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			assert.Equal(t, tc.want, printVersion(flags(t, tc.args...), &out))

			if !tc.want {
				assert.Empty(t, out.String())
				return
			}

			assert.Contains(t, out.String(), applicationName+":")
			assert.Contains(t, out.String(), "version:")
		})
	}
}
