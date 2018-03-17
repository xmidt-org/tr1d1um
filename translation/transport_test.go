package translation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestPayload(t *testing.T) {
	t.Run("EmptyNames", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestPayload("", "")
		assert.EqualValues(ErrEmptyNames, e)
		assert.Nil(p)
	})

	t.Run("GET", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestPayload("n0,n1", "")
		assert.Nil(e)

		expectedBytes, err := json.Marshal(&GetWDMP{Command: CommandGet, Names: []string{"n0", "n1"}})

		if err != nil {
			panic(err)
		}

		assert.EqualValues(expectedBytes, p)
	})

	t.Run("GETAttrs", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestPayload("n0,n1", "attr0")
		assert.Nil(e)

		expectedBytes, err := json.Marshal(&GetWDMP{Command: CommandGetAttrs, Names: []string{"n0", "n1"}, Attributes: "attr0"})

		if err != nil {
			panic(err)
		}

		assert.EqualValues(expectedBytes, p)
	})
}
