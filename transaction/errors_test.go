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

package transaction

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xmidt-org/sallust"
	"go.uber.org/zap"
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

func TestErrorLogEncoder(t *testing.T) {
	tcs := []struct {
		desc      string
		getLogger func(context.Context) *zap.Logger
	}{
		{
			desc:      "nil getlogger",
			getLogger: nil,
		},
		{
			desc:      "valid getlogger",
			getLogger: sallust.Get,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			e := func(ctx context.Context, _ error, _ http.ResponseWriter) {
				assert.EqualValues("tid00", ctx.Value(ContextKeyRequestTID))
			}
			le := ErrorLogEncoder(tc.getLogger, e)

			if tc.getLogger == nil {
				assert.Panics(func() {
					//assumes TID is context
					le(context.WithValue(context.TODO(), ContextKeyRequestTID, "tid00"), errors.New("test"), nil)
				})
			} else {
				assert.NotPanics(func() {
					//assumes TID is context
					le(context.WithValue(context.TODO(), ContextKeyRequestTID, "tid00"), errors.New("test"), nil)
				})
			}
		})
	}
}
