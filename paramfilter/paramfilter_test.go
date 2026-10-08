// SPDX-FileCopyrightText: 2025 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package paramfilter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	wifiRadioEnable    = "Device.WiFi.Radio.Enable"
	wifiGlob           = "Device.WiFi.**"
	ethernetStatus     = "Device.Ethernet.Status"
	ethernetGlob       = "Device.Ethernet.**"
	lowerWiFiRadioSSID = "device.wifi.radio.ssid"
)

func TestNew_DefaultsToNever(t *testing.T) {
	tests := []struct {
		name string
		mode Mode
	}{
		{name: "empty string", mode: ""},
		{name: "garbage", mode: "garbage"},
		{name: "explicit never", mode: ModeNever},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New(FilterConfig{Mode: tt.mode})
			assert.False(t, f.ShouldLog(wifiRadioEnable))
		})
	}
}

func TestShouldLog_NilFilter(t *testing.T) {
	var f *Filter
	assert.False(t, f.ShouldLog("anything"))
}

func TestShouldLog_NeverMode(t *testing.T) {
	tests := []struct {
		name   string
		except []string
		param  string
		want   bool
	}{
		{
			name:   "no exceptions logs nothing",
			except: nil,
			param:  wifiRadioEnable,
			want:   false,
		},
		{
			name:   "exact match allows logging",
			except: []string{wifiRadioEnable},
			param:  wifiRadioEnable,
			want:   true,
		},
		{
			name:   "case insensitive match",
			except: []string{"device.wifi.radio.enable"},
			param:  wifiRadioEnable,
			want:   true,
		},
		{
			name:   "single star matches one level",
			except: []string{"Device.WiFi.*.Enable"},
			param:  wifiRadioEnable,
			want:   true,
		},
		{
			name:   "single star does not cross dots",
			except: []string{"Device.WiFi.*.Enable"},
			param:  "Device.WiFi.Radio.Settings.Enable",
			want:   false,
		},
		{
			name:   "double star matches across dots",
			except: []string{wifiGlob},
			param:  "Device.WiFi.Radio.Settings.Enable",
			want:   true,
		},
		{
			name:   "double star matches single level too",
			except: []string{wifiGlob},
			param:  "Device.WiFi.SSID",
			want:   true,
		},
		{
			name:   "no match",
			except: []string{wifiGlob},
			param:  ethernetStatus,
			want:   false,
		},
		{
			name:   "star in middle of path",
			except: []string{"Device.*.Status"},
			param:  ethernetStatus,
			want:   true,
		},
		{
			name:   "star in middle of path, no match",
			except: []string{"Device.*.Status"},
			param:  "Device.Ethernet.Other",
			want:   false,
		},
		{
			name:   "star and double star together",
			except: []string{"Device.*.Status.**"},
			param:  "Device.Foo.Status.Bar.Enable",
			want:   true,
		},
		{
			name:   "multiple patterns first matches",
			except: []string{wifiGlob, ethernetGlob},
			param:  wifiRadioEnable,
			want:   true,
		},
		{
			name:   "multiple patterns second matches",
			except: []string{wifiGlob, ethernetGlob},
			param:  ethernetStatus,
			want:   true,
		},
		{
			name:   "multiple patterns none match",
			except: []string{wifiGlob, ethernetGlob},
			param:  "Device.DeviceInfo.SerialNumber",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New(FilterConfig{
				Mode:   ModeNever,
				Except: tt.except,
			})
			assert.Equal(t, tt.want, f.ShouldLog(tt.param))
		})
	}
}

func TestShouldLog_AlwaysMode(t *testing.T) {
	tests := []struct {
		name   string
		except []string
		param  string
		want   bool
	}{
		{
			name:   "no exceptions logs everything",
			except: nil,
			param:  wifiRadioEnable,
			want:   true,
		},
		{
			name:   "exact match denies logging",
			except: []string{wifiRadioEnable},
			param:  wifiRadioEnable,
			want:   false,
		},
		{
			name:   "non-matching param still logged",
			except: []string{wifiRadioEnable},
			param:  ethernetStatus,
			want:   true,
		},
		{
			name:   "double star deny",
			except: []string{wifiGlob},
			param:  wifiRadioEnable,
			want:   false,
		},
		{
			name:   "double star deny does not affect others",
			except: []string{wifiGlob},
			param:  ethernetStatus,
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New(FilterConfig{
				Mode:   ModeAlways,
				Except: tt.except,
			})
			assert.Equal(t, tt.want, f.ShouldLog(tt.param))
		})
	}
}

func TestCompileGlob(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		match   string
		want    bool
	}{
		{
			name:    "literal",
			pattern: "Device.WiFi.SSID",
			match:   "device.wifi.ssid",
			want:    true,
		},
		{
			name:    "single star",
			pattern: "Device.*.SSID",
			match:   "device.wifi.ssid",
			want:    true,
		},
		{
			name:    "single star no dot crossing",
			pattern: "Device.*.SSID",
			match:   lowerWiFiRadioSSID,
			want:    false,
		},
		{
			name:    "double star",
			pattern: "Device.**",
			match:   lowerWiFiRadioSSID,
			want:    true,
		},
		{
			name:    "double star then literal",
			pattern: "Device.**.SSID",
			match:   lowerWiFiRadioSSID,
			want:    true,
		},
		{
			name:    "regex metachars escaped",
			pattern: "Device.WiFi(1).Status",
			match:   "device.wifi(1).status",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := compileGlob(tt.pattern)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, re.MatchString(tt.match))
		})
	}
}

func TestNewFilters(t *testing.T) {
	cfg := Config{
		Get: FilterConfig{
			Mode:   ModeNever,
			Except: []string{wifiGlob},
		},
		Patch: FilterConfig{
			Mode:   ModeAlways,
			Except: []string{"Device.DeviceInfo.SerialNumber"},
		},
	}

	f := NewFilters(cfg)

	// GET: never except Device.WiFi.**
	assert.True(t, f.Get.ShouldLog(wifiRadioEnable))
	assert.False(t, f.Get.ShouldLog(ethernetStatus))

	// PATCH: always except Device.DeviceInfo.SerialNumber
	assert.True(t, f.Patch.ShouldLog(wifiRadioEnable))
	assert.False(t, f.Patch.ShouldLog("Device.DeviceInfo.SerialNumber"))
}

func TestNewFilters_ZeroConfig(t *testing.T) {
	f := NewFilters(Config{})

	// Both default to never with no exceptions -- log nothing.
	assert.False(t, f.Get.ShouldLog(wifiRadioEnable))
	assert.False(t, f.Patch.ShouldLog(wifiRadioEnable))
}
