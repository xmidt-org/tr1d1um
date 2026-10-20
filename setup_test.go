// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestSetupFlagSet(t *testing.T) {
	_, _, fs, err := setup([]string{"-f", writeConfig(t, t.TempDir(), "c.yaml", testSetupConfig), "-d", "-v"})
	require.NoError(t, err)

	file, err := fs.GetString("file")
	require.NoError(t, err)
	assert.NotEmpty(t, file)

	debug, err := fs.GetBool("debug")
	require.NoError(t, err)
	assert.True(t, debug)

	version, err := fs.GetBool("version")
	require.NoError(t, err)
	assert.True(t, version)
}

func TestSetup(t *testing.T) {
	t.Run("config file", func(t *testing.T) {
		path := writeConfig(t, t.TempDir(), "c.yaml", testSetupConfig)

		v, l, fs, err := setup([]string{"--file", path})
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.NotNil(t, l)
		assert.NotNil(t, fs)

		assert.Equal(t, "http://xmidt.example.com", v.GetString(targetURLKey), "the file is read")
		assert.Equal(t, defaults[wrpSourceKey], v.Get(wrpSourceKey), "defaults fill the gaps")
	})

	t.Run("search path", func(t *testing.T) {
		dir := isolateSearchPath(t)
		writeConfig(t, dir, applicationName+".yaml", testSetupConfig)

		v, l, _, err := setup(nil)
		require.NoError(t, err)
		assert.NotNil(t, l)
		assert.Equal(t, "http://xmidt.example.com", v.GetString(targetURLKey))
	})

	t.Run("search path from home", func(t *testing.T) {
		dir := isolateSearchPath(t)
		home := filepath.Join(dir, "."+applicationName)
		require.NoError(t, os.Mkdir(home, 0o700))
		writeConfig(t, home, applicationName+".yaml", testSetupConfig)

		v, _, _, err := setup(nil)
		require.NoError(t, err)
		assert.Equal(t, "http://xmidt.example.com", v.GetString(targetURLKey))
	})

	t.Run("unknown flag", func(t *testing.T) {
		v, l, fs, err := setup([]string{"--nope"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create parse args")
		assert.Nil(t, v)
		assert.Nil(t, l)
		assert.NotNil(t, fs)
	})

	t.Run("missing config file", func(t *testing.T) {
		_, l, _, err := setup([]string{"-f", filepath.Join(t.TempDir(), "missing.yaml")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read config file")
		assert.Nil(t, l)
	})

	t.Run("no config on the search path", func(t *testing.T) {
		if _, err := os.Stat("/etc/" + applicationName); err == nil {
			t.Skip("/etc/" + applicationName + " exists on this machine")
		}
		isolateSearchPath(t)

		_, _, _, err := setup(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read config file")
	})

	t.Run("invalid logging config", func(t *testing.T) {
		path := writeConfig(t, t.TempDir(), "c.yaml", "logging: not-a-map\n")

		_, l, _, err := setup([]string{"-f", path})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read logging config")
		assert.Nil(t, l)
	})
}
