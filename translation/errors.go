package translation

import "errors"

// Error values
var (
	ErrEmptyNames        = errors.New("names parameter is required")
	ErrInvalidService    = errors.New("unsupported Service")
	ErrInternal          = errors.New("oops! Something unexpected went wrong in our service")
	ErrUnsupportedMethod = errors.New("unsupported method. Could not decode request payload")

	//Set command errors
	ErrInvalidSetWDMP = errors.New("invalid XPC SET message")
	ErrNewCIDRequired = errors.New("NewCid is required for TEST_AND_SET")

	//Add/Delete command  errors
	ErrMissingTable = errors.New("table property is required")
	ErrMissingRow   = errors.New("row property is required")

	//Replace command error
	ErrMissingRows = errors.New("rows property is required")
)
