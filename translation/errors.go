package translation

import (
	"errors"

	"github.com/xmidt-org/tr1d1um/common"
)

// Error values definitions for the translation service
var (
	ErrEmptyNames        = common.NewBadRequestError(errors.New("names parameter is required"))
	ErrInvalidService    = common.NewBadRequestError(errors.New("unsupported Service"))
	ErrUnsupportedMethod = common.NewBadRequestError(errors.New("unsupported method. Could not decode request payload"))

	//Set command errors
	ErrInvalidSetWDMP = common.NewBadRequestError(errors.New("invalid XPC SET message"))
	ErrNewCIDRequired = common.NewBadRequestError(errors.New("newCid is required for TEST_AND_SET"))

	//Add/Delete command  errors
	ErrMissingTable = common.NewBadRequestError(errors.New("table property is required"))
	ErrMissingRow   = common.NewBadRequestError(errors.New("row property is required"))

	//Replace command error
	ErrMissingRows = common.NewBadRequestError(errors.New("rows property is required"))
)
