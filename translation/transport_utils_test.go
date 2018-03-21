package translation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Comcast/tr1d1um/common"

	"github.com/gorilla/mux"

	"github.com/Comcast/webpa-common/device"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/stretchr/testify/assert"
)

func TestGenTID(t *testing.T) {
	assert := assert.New(t)
	tid, err := genTID()
	assert.Nil(err)
	assert.NotEmpty(tid)
}
func TestValidateAndDeduceSETCommand(t *testing.T) {
	assert := assert.New(t)

	t.Run("newCIDMissing", func(t *testing.T) {
		wdmp := new(setWDMP)
		err := validateAndDeduceSET(wdmp, "", "old-cid", "sync-cm")
		assert.EqualValues(ErrNewCIDRequired, err)
	})

	t.Run("NilParams", func(t *testing.T) {
		wdmp := new(setWDMP)
		err := validateAndDeduceSET(wdmp, "", "", "")
		assert.EqualValues(ErrInvalidSetWDMP, err)
	})

	t.Run("TestSetNilValues", func(t *testing.T) {
		wdmp := new(setWDMP)

		err := validateAndDeduceSET(wdmp, "newVal", "oldVal", "")
		assert.Nil(err)
		assert.EqualValues(CommandTestSet, wdmp.Command)
	})
}
func TestIsValidSetWDMP(t *testing.T) {
	t.Run("TestAndSetZeroParams", func(t *testing.T) {
		assert := assert.New(t)

		wdmp := &setWDMP{Command: CommandTestSet} //nil parameters
		assert.True(isValidSetWDMP(wdmp))

		wdmp = &setWDMP{Command: CommandTestSet, Parameters: []setParam{}} //empty parameters
		assert.True(isValidSetWDMP(wdmp))
	})

	t.Run("NilNameInParam", func(t *testing.T) {
		assert := assert.New(t)

		dataType := int8(0)
		nilNameParam := setParam{
			Value:    "val",
			DataType: &dataType,
			// Name is left undefined
		}
		params := []setParam{nilNameParam}
		wdmp := &setWDMP{Command: CommandSet, Parameters: params}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("NilDataTypeNonNilValue", func(t *testing.T) {
		assert := assert.New(t)

		name := "nameVal"
		param := setParam{
			Name:  &name,
			Value: 3,
			//DataType is left undefined
		}
		params := []setParam{param}
		wdmp := &setWDMP{Command: CommandSet, Parameters: params}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("SetAttrsParamNilAttr", func(t *testing.T) {
		assert := assert.New(t)

		name := "nameVal"
		param := setParam{
			Name: &name,
		}
		params := []setParam{param}
		wdmp := &setWDMP{Command: CommandSetAttrs, Parameters: params}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("MixedParams", func(t *testing.T) {
		assert := assert.New(t)

		name, dataType := "victorious", int8(1)
		setAttrParam := setParam{
			Name:       &name,
			Attributes: map[string]interface{}{"three": 3},
		}

		sp := setParam{
			Name:       &name,
			Attributes: map[string]interface{}{"two": 2},
			Value:      3,
			DataType:   &dataType,
		}
		mixParams := []setParam{setAttrParam, sp}
		wdmp := &setWDMP{Command: CommandSetAttrs, Parameters: mixParams}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("IdealSet", func(t *testing.T) {
		assert := assert.New(t)

		name := "victorious"
		setAttrParam := setParam{
			Name:       &name,
			Attributes: map[string]interface{}{"three": 3},
		}
		params := []setParam{setAttrParam}
		wdmp := &setWDMP{Command: CommandSetAttrs, Parameters: params}
		assert.True(isValidSetWDMP(wdmp))
	})
}

func TestGetCommandForParam(t *testing.T) {
	t.Run("EmptyParams", func(t *testing.T) {
		assert := assert.New(t)
		assert.EqualValues(CommandSet, getCommandForParams(nil))
		assert.EqualValues(CommandSet, getCommandForParams([]setParam{}))
	})

	//Attributes and Name are required properties for SET_ATTRS
	t.Run("SetCommandUndefinedAttributes", func(t *testing.T) {
		assert := assert.New(t)
		name := "setParam"
		setCommandParam := setParam{Name: &name}
		assert.EqualValues(CommandSet, getCommandForParams([]setParam{setCommandParam}))
	})

	//DataType and Value must be null for SET_ATTRS
	t.Run("SetAttrsCommand", func(t *testing.T) {
		assert := assert.New(t)
		name := "setAttrsParam"
		setCommandParam := setParam{
			Name:       &name,
			Attributes: map[string]interface{}{"zero": 0},
		}
		assert.EqualValues(CommandSetAttrs, getCommandForParams([]setParam{setCommandParam}))
	})
}

func TestWrapInWRP(t *testing.T) {
	t.Run("EmptyVars", func(t *testing.T) {
		assert := assert.New(t)

		w, e := wrap([]byte(""), "", nil)

		assert.Nil(w)
		assert.EqualValues(device.ErrorInvalidDeviceName, e)
	})

	t.Run("GivenTID", func(t *testing.T) {
		assert := assert.New(t)

		w, e := wrap([]byte{'t'}, "t0", map[string]string{"deviceid": "mac:112233445566", "service": "s0"})

		assert.Nil(e)
		assert.EqualValues(wrp.SimpleRequestResponseMessageType, w.Type)
		assert.EqualValues([]byte{'t'}, w.Payload)
		assert.EqualValues("mac:112233445566/s0", w.Destination)
		assert.EqualValues("s0", w.Source)
		assert.EqualValues("t0", w.TransactionUUID)
	})

	t.Run("GeneratedTID", func(t *testing.T) {
		assert := assert.New(t)

		w, e := wrap([]byte{'t'}, "", map[string]string{"deviceid": "mac:112233445566", "service": "s0"})

		assert.Nil(e)
		assert.EqualValues(wrp.SimpleRequestResponseMessageType, w.Type)
		assert.EqualValues([]byte{'t'}, w.Payload)
		assert.EqualValues("mac:112233445566/s0", w.Destination)
		assert.EqualValues("s0", w.Source)
		assert.NotEmpty(w.TransactionUUID)
	})
}

func TestDecodeValidServiceRequest(t *testing.T) {
	f := decodeValidServiceRequest([]string{"s0"}, func(_ context.Context, _ *http.Request) (interface{}, error) {
		return nil, nil
	})

	t.Run("InvalidService", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "localhost:8090/api", nil)
		i, err := f(context.TODO(), r)
		assert.Nil(i)
		assert.EqualValues(ErrInvalidService, err)
	})

	t.Run("ValidService", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "localhost:8090/api", nil)
		r = mux.SetURLVars(r, map[string]string{"service": "s0"})

		i, err := f(context.TODO(), r)
		assert.Nil(i)
		assert.Nil(err)
	})
}

func TestContains(t *testing.T) {
	assert := assert.New(t)
	assert.False(contains("a", nil))
	assert.False(contains("a", []string{}))
	assert.True(contains("a", []string{"a", "b"}))
}

func TestForwardHeadersByPrefix(t *testing.T) {
	t.Run("NoHeaders", func(t *testing.T) {
		assert := assert.New(t)

		to := httptest.NewRecorder()
		resp := &http.Response{Header: http.Header{}}

		forwardHeadersByPrefix("H", resp, to)
		assert.Empty(to.Header())
	})

	t.Run("MultipleHeadersFiltered", func(t *testing.T) {
		assert := assert.New(t)
		resp, to := &http.Response{Header: http.Header{}}, httptest.NewRecorder()

		resp.Header.Add("Helium", "3")
		resp.Header.Add("Hydrogen", "5")
		resp.Header.Add("Hydrogen", "6")

		forwardHeadersByPrefix("He", resp, to)
		assert.NotEmpty(to.Header())
		assert.EqualValues(1, len(to.Header()))
		assert.EqualValues("3", to.Header().Get("Helium"))
	})

	t.Run("MultipleHeadersFilteredFullArray", func(t *testing.T) {
		assert := assert.New(t)
		to := httptest.NewRecorder()
		resp := &http.Response{Header: http.Header{}}

		resp.Header.Add("Helium", "3")
		resp.Header.Add("Hydrogen", "5")
		resp.Header.Add("Hydrogen", "6")

		forwardHeadersByPrefix("H", resp, to)
		assert.NotEmpty(to.Header)
		assert.EqualValues(2, len(to.Header()))
		assert.EqualValues([]string{"5", "6"}, to.Header()["Hydrogen"])
	})

	t.Run("NilCases", func(t *testing.T) {
		to, resp := httptest.NewRecorder(), &http.Response{}
		//none of these should panic
		forwardHeadersByPrefix("", nil, nil)
		forwardHeadersByPrefix("", resp, to)
	})
}

func TestMarkArrivalTime(t *testing.T) {
	assert := assert.New(t)

	ctx := markArrivalTime(context.TODO(), nil)

	assert.IsType(time.Now(), ctx.Value(common.ContextKeyRequestArrivalTime))
}
