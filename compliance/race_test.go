// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

//go:build race

package compliance

// raceBuild is true when the test binary was built with the race detector,
// so that the tr1d1um binary under test is built with it too.
const raceBuild = true
