// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package translation

import (
	"errors"

	"github.com/xmidt-org/tr1d1um/internal/transaction"
)

// Error values definitions for the translation service
var (
	ErrEmptyNames        = transaction.NewBadRequestError(errors.New("names parameter is required"))
	ErrInvalidService    = transaction.NewBadRequestError(errors.New("unsupported Service"))
	ErrUnsupportedMethod = transaction.NewBadRequestError(errors.New("unsupported method. Could not decode request payload"))

	//Set command errors
	ErrInvalidSetWDMP = transaction.NewBadRequestError(errors.New("invalid SET message"))
	ErrNewCIDRequired = transaction.NewBadRequestError(errors.New("newCid is required for TEST_AND_SET"))

	//Add/Delete command  errors
	ErrMissingTable   = transaction.NewBadRequestError(errors.New("table property is required"))
	ErrMissingRow     = transaction.NewBadRequestError(errors.New("row property is required"))
	ErrInvalidRow     = transaction.NewBadRequestError(errors.New("row property is invalid"))
	ErrInvalidPayload = transaction.NewBadRequestError(errors.New("payload is invalid"))

	//Replace command error
	ErrMissingRows = transaction.NewBadRequestError(errors.New("rows property is required"))
	ErrInvalidRows = transaction.NewBadRequestError(errors.New("rows property is invalid"))
)
