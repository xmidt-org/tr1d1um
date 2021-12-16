/**
 * Copyright 2021 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package translation

import (
	"errors"

	"github.com/xmidt-org/tr1d1um/transaction"
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
	ErrMissingTable = transaction.NewBadRequestError(errors.New("table property is required"))
	ErrMissingRow   = transaction.NewBadRequestError(errors.New("row property is required"))
	ErrInvalidRow   = transaction.NewBadRequestError(errors.New("row property is invalid"))

	//Replace command error
	ErrMissingRows = transaction.NewBadRequestError(errors.New("rows property is required"))
	ErrInvalidRows = transaction.NewBadRequestError(errors.New("rows property is invalid"))
)
