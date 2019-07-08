package common

import (
	"errors"
)

// Error values definitions for the translation service
var (
	ErrEmptyNames        = NewBadRequestError(errors.New("names parameter is required"))
	ErrInvalidService    = NewBadRequestError(errors.New("unsupported Service"))
	ErrUnsupportedMethod = NewBadRequestError(errors.New("unsupported method. Could not decode request payload"))

	//Set command errors
	ErrInvalidSetWDMP = NewBadRequestError(errors.New("invalid XPC SET message"))
	ErrNewCIDRequired = NewBadRequestError(errors.New("newCid is required for TEST_AND_SET"))

	//Add/Delete command  errors
	ErrMissingTable = NewBadRequestError(errors.New("table property is required"))
	ErrMissingRow   = NewBadRequestError(errors.New("row property is required"))

	//Replace command error
	ErrMissingRows = NewBadRequestError(errors.New("rows property is required"))
)
