// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCodedErrorUnwrap(t *testing.T) {
	cause := errors.New("the cause")
	err := NewBadRequestError(cause)

	assert.ErrorIs(t, err, cause)

	var coded CodedError
	assert.ErrorAs(t, err, &coded)
	assert.Equal(t, http.StatusBadRequest, coded.StatusCode())
	assert.Equal(t, cause.Error(), err.Error())
}
