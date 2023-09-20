// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

type contextKey int

// Keys to important context values on incoming requests to TR1D1UM
const (
	ContextKeyRequestArrivalTime contextKey = iota
	ContextKeyRequestTID
)
