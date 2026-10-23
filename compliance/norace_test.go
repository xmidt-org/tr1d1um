// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

//go:build !race

package compliance

// raceBuild is false when the test binary was built without the race
// detector.
const raceBuild = false
