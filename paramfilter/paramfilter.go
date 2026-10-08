// SPDX-FileCopyrightText: 2025 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

// Package paramfilter provides a configurable filter that decides whether a
// TR-181 parameter's value should be included in log output.
//
// Parameter names are always logged; this filter controls only values.
//
// Separate rules can be configured for GET and PATCH operations:
//
//	parameterLogging:
//	  get:
//	    mode: never
//	    except:
//	      - "Device.WiFi.**"
//	  patch:
//	    mode: always
//	    except:
//	      - "Device.DeviceInfo.SerialNumber"
//
// Each section supports two modes:
//
//   - "never"  (default) -- log no values, except parameters matching the
//     patterns in the Except list.
//   - "always" -- log all values, except parameters matching the patterns
//     in the Except list.
//
// Pattern syntax (case-insensitive):
//
//   - *  matches any sequence of characters except '.' (single level).
//   - ** matches any sequence of characters including '.' (multi-level).
//   - All other characters are matched literally (case-insensitive).
package paramfilter

import (
	"regexp"
	"strings"
)

// Mode describes whether logging is on or off by default.
type Mode string

const (
	// ModeNever means no parameters are logged unless they match an Except
	// pattern (allowlist).  This is the default.
	ModeNever Mode = "never"

	// ModeAlways means all parameters are logged unless they match an Except
	// pattern (denylist).
	ModeAlways Mode = "always"
)

// FilterConfig is the mode + exceptions for a single operation type.
//
//	mode: never
//	except:
//	  - "Device.WiFi.*"
type FilterConfig struct {
	Mode   Mode     `json:"mode"   yaml:"mode"`
	Except []string `json:"except" yaml:"except"`
}

// Config is the top-level YAML-friendly configuration for parameter logging.
//
//	parameterLogging:
//	  get:
//	    mode: never
//	    except:
//	      - "Device.WiFi.**"
//	  patch:
//	    mode: always
//	    except:
//	      - "Device.DeviceInfo.SerialNumber"
type Config struct {
	Get   FilterConfig `json:"get"   yaml:"get"`
	Patch FilterConfig `json:"patch" yaml:"patch"`
}

// Filters holds the compiled filters for both GET and PATCH operations.
type Filters struct {
	Get   *Filter
	Patch *Filter
}

// NewFilters compiles both GET and PATCH filters from the given Config.
func NewFilters(cfg Config) *Filters {
	return &Filters{
		Get:   New(cfg.Get),
		Patch: New(cfg.Patch),
	}
}

// GetFilter returns the GET filter, or nil if the receiver is nil.
func (f *Filters) GetFilter() *Filter {
	if f == nil {
		return nil
	}
	return f.Get
}

// PatchFilter returns the PATCH filter, or nil if the receiver is nil.
func (f *Filters) PatchFilter() *Filter {
	if f == nil {
		return nil
	}
	return f.Patch
}

// Filter is the compiled, ready-to-use filter built from a FilterConfig.
type Filter struct {
	mode     Mode
	patterns []*regexp.Regexp
}

// New compiles a Filter from the given FilterConfig.  An empty or unrecognized
// Mode is treated as ModeNever.  Invalid glob patterns are silently skipped.
func New(cfg FilterConfig) *Filter {
	mode := cfg.Mode
	if mode != ModeAlways {
		mode = ModeNever
	}

	patterns := make([]*regexp.Regexp, 0, len(cfg.Except))
	for _, p := range cfg.Except {
		re, err := compileGlob(p)
		if err == nil {
			patterns = append(patterns, re)
		}
	}

	return &Filter{
		mode:     mode,
		patterns: patterns,
	}
}

// ShouldLog returns true when the given TR-181 parameter's value should be
// included in log output.
func (f *Filter) ShouldLog(name string) bool {
	if f == nil {
		return false
	}

	matched := f.matchesAny(name)

	switch f.mode {
	case ModeAlways:
		// log everything except matches
		return !matched
	default:
		// log nothing except matches
		return matched
	}
}

// matchesAny reports whether name matches any of the compiled patterns.
func (f *Filter) matchesAny(name string) bool {
	lower := strings.ToLower(name)
	for _, re := range f.patterns {
		if re.MatchString(lower) {
			return true
		}
	}
	return false
}

// compileGlob converts a TR-181 glob pattern into a case-insensitive regexp.
//
// The conversion rules are:
//   - **  -> .* (match everything including dots)
//   - *   -> [^.]* (match everything except dots)
//   - all other characters are regexp-quoted then lowered.
func compileGlob(pattern string) (*regexp.Regexp, error) {
	lower := strings.ToLower(pattern)

	var b strings.Builder
	b.WriteString("^")

	for i := 0; i < len(lower); {
		if i+1 < len(lower) && lower[i] == '*' && lower[i+1] == '*' {
			b.WriteString(".*")
			i += 2
			continue
		}
		if lower[i] == '*' {
			b.WriteString("[^.]*")
			i++
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(lower[i])))
		i++
	}

	b.WriteString("$")
	return regexp.Compile(b.String())
}
