// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"net/http"

	"github.com/stretchr/testify/mock"
	"github.com/xmidt-org/bascule/basculehttp"
)

const (
	MockAuthHeaderValue = "mockAuth"
)

type MockDecorator struct {
	mock.Mock
}

func (m *MockDecorator) Decorate(ctx context.Context, req *http.Request) error {
	defer func() { req.Header.Set(basculehttp.DefaultAuthorizationHeader, MockAuthHeaderValue) }()

	return m.Called(ctx, req).Error(0)
}
