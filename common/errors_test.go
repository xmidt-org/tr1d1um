package common

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewCodedError(t *testing.T) {
	assert := assert.New(t)
	var ce = NewCodedError(errors.New("test"), 500)
	assert.NotNil(ce)
	assert.EqualValues(500, ce.StatusCode())
	assert.EqualValues("test", ce.Error())
}

func TestBadRequestError(t *testing.T) {
	assert := assert.New(t)
	var ce = NewBadRequestError(errors.New("test"))
	assert.NotNil(ce)
	assert.EqualValues(400, ce.StatusCode())
	assert.EqualValues("test", ce.Error())
}
