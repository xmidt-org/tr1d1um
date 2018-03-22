package translation

import "errors"

// Error values
var (
	ErrEmptyNames        = errors.New("bad request, names parameter is required")
	ErrInvalidService    = errors.New("bad request, unsupported Service")
	ErrInternal          = errors.New("oops! Something unexpected went wrong in our service")
	ErrUnsupportedMethod = errors.New("unsupported method. Could not decode request payload")

	//Set command errors
	ErrInvalidSetWDMP = errors.New("bad request, invalid XPC SET message")
	ErrNewCIDRequired = errors.New("bad request, NewCid is required for TEST_AND_SET")

	//Add/Delete command  errors
	ErrMissingTable = errors.New("bad request, table property is required")
	ErrMissingRow   = errors.New("bad request, row property is required")

	//Replace command error
	ErrMissingRows = errors.New("bad request, rows property is required")
)
