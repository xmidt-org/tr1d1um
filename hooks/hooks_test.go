package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/xmidt-org/argus/chrysom"
	"github.com/xmidt-org/argus/model"
	"github.com/xmidt-org/webpa-common/logging"
	"github.com/xmidt-org/webpa-common/webhook"
	"io/ioutil"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPostWebhookHandler(t *testing.T) {
	goodHook := webhook.W{
		Config: struct {
			URL             string   `json:"url"`
			ContentType     string   `json:"content_type"`
			Secret          string   `json:"secret,omitempty"`
			AlternativeURLs []string `json:"alt_urls,omitempty"`
		}{
			URL:             "http://localhost:8080/events",
			ContentType:     "application/json",
			Secret:          "noice",
			AlternativeURLs: nil,
		},
		FailureURL: "",
		Events:     []string{".*"},
		Matcher: struct {
			DeviceId []string `json:"device_id"`
		}{},
		Duration: 0,
		Until:    time.Time{},
		Address:  "",
	}

	mockStore := &MockHookPusherStore{}
	mockStore.On("Push", mock.Anything, mock.Anything).Return("world", nil).Once()
	mockStore.On("Push", mock.Anything, mock.Anything).Return("", errors.New("failed to put item, non 200 statuscode")).Once()

	registry := Registry{
		hookStore: mockStore,
		config: RegistryConfig{
			Logger:   logging.NewTestLogger(nil, t),
			Listener: nil,
			Config: chrysom.ClientConfig{
				DefaultTTL: 5,
			},
		},
	}

	type testStruct struct {
		title              string
		hook               webhook.W
		expectedStatusCode int
	}

	testData := []testStruct{
		{
			title:              "empty webhook",
			hook:               webhook.W{},
			expectedStatusCode: 400,
		},
		{
			title:              "good webhook",
			hook:               goodHook,
			expectedStatusCode: 200,
		},
		{
			title:              "backend failed",
			hook:               goodHook,
			expectedStatusCode: 500,
		},
	}

	for _, tc := range testData {
		t.Run(tc.title, func(t *testing.T) {
			assert := assert.New(t)
			status, body := testRegistryPostWithRequest(registry, tc.hook)
			assert.Equal(tc.expectedStatusCode, status)
			assert.NotEmpty(body)
			registry.config.Logger.Log("body", string(body))
		})
	}
	mockStore.AssertExpectations(t)

}

func testRegistryPostWithRequest(registry Registry, w webhook.W) (int, []byte) {
	response := httptest.NewRecorder()
	payload, _ := json.Marshal(w)
	request := httptest.NewRequest("POST", "/hook", bytes.NewBuffer(payload))

	registry.UpdateRegistry(response, request)
	result := response.Result()

	data, _ := ioutil.ReadAll(result.Body)
	return result.StatusCode, data

}

func TestGetWebhookHandler(t *testing.T) {
	assert := assert.New(t)

	goodHook := webhook.W{
		Config: struct {
			URL             string   `json:"url"`
			ContentType     string   `json:"content_type"`
			Secret          string   `json:"secret,omitempty"`
			AlternativeURLs []string `json:"alt_urls,omitempty"`
		}{
			URL:             "http://localhost:8080/events",
			ContentType:     "application/json",
			Secret:          "noice",
			AlternativeURLs: nil,
		},
		FailureURL: "",
		Events:     []string{".*"},
		Matcher: struct {
			DeviceId []string `json:"device_id"`
		}{},
		Duration: 0,
		Until:    time.Time{},
		Address:  "",
	}
	data, err := json.Marshal(&goodHook)
	assert.NoError(err)
	var payload map[string]interface{}
	err = json.Unmarshal(data, &payload)
	assert.NoError(err)

	item := model.Item{
		Identifier: "world",
		Data:       payload,
		TTL:        500,
	}

	mockStore := &MockHookPusherStore{}
	mockStore.On("GetItems", mock.Anything).Return([]model.Item{item}, nil).Once()
	mockStore.On("GetItems", mock.Anything).Return([]model.Item{}, errors.New("failed to get items, non 200 statuscode")).Once()

	registry := Registry{
		hookStore: mockStore,
		config: RegistryConfig{
			Logger:   logging.NewTestLogger(nil, t),
			Listener: nil,
			Config: chrysom.ClientConfig{
				DefaultTTL: 5,
			},
		},
	}

	response := httptest.NewRecorder()
	registry.GetRegistry(response, httptest.NewRequest("GET", "/hooks", nil))
	assert.Equal(200, response.Result().StatusCode)
	data, _ = ioutil.ReadAll(response.Result().Body)
	assert.NotEmpty(data)
	registry.config.Logger.Log("body", string(data))

	response = httptest.NewRecorder()
	registry.GetRegistry(response, httptest.NewRequest("GET", "/hooks", nil))
	assert.Equal(500, response.Result().StatusCode)
	data, _ = ioutil.ReadAll(response.Result().Body)
	assert.NotEmpty(data)
	registry.config.Logger.Log("body", string(data))

	mockStore.AssertExpectations(t)
}
