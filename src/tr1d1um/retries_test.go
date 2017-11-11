/**
 * Copyright 2017 Comcast Cable Communications Management, LLC
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

package main

import (
	"testing"
	"time"

	"github.com/Comcast/webpa-common/logging"
	"github.com/stretchr/testify/assert"
)

func TestRetryExecute(t *testing.T) {

	OnRetryInternalFailure()
	t.Run("MaxRetries", func(t *testing.T) {
		assert := assert.New(t)
		retry := Retry{
			Logger:      logging.DefaultLogger(),
			MaxRetries:  2,
			ShouldRetry: func(_ interface{}, _ error) bool { return true },
			OnInternalFail: func() interface{} {
				return -1
			},
		}

		callCount := 0
		countOp := func(_ ...interface{}) (_ interface{}, _ error) {
			callCount++
			return
		}
		resp, err := retry.Execute(countOp, 0)
		assert.Nil(err)
		assert.Nil(resp)
		assert.EqualValues(retry.MaxRetries, callCount)
	})

	t.Run("FailInBetween", func(t *testing.T) {
		assert := assert.New(t)
		retry := Retry{
			Logger:     logging.DefaultLogger(),
			MaxRetries: 2,
			ShouldRetry: func(c interface{}, _ error) bool {
				if c.(int) < 1 {
					return true
				}
				return false
			},
			OnInternalFail: func() interface{} {
				return -1
			},
		}

		callCount := 0
		countOp := func(_ ...interface{}) (resp interface{}, _ error) {
			callCount++
			resp = callCount
			return
		}
		resp, err := retry.Execute(countOp, 0)
		assert.Nil(err)
		assert.EqualValues(1, resp)
		assert.EqualValues(retry.MaxRetries-1, callCount)
	})
}

func TestRetryCheckDependencies(t *testing.T) {
	t.Run("shouldRetryUndefined", func(t *testing.T) {
		assert := assert.New(t)
		retry := Retry{
			Interval:   time.Second,
			MaxRetries: 3,
			Logger:     logging.DefaultLogger(),
			OnInternalFail: func() interface{} {
				return -1
			},
		}

		result, err := retry.Execute(nil, "")
		assert.EqualValues(-1, result)
		assert.EqualValues(errUndefinedShouldRetry, err)
	})

	t.Run("InvalidMaxRetries", func(t *testing.T) {
		assert := assert.New(t)
		retry := Retry{
			Interval:    time.Second,
			MaxRetries:  0,
			Logger:      logging.DefaultLogger(),
			ShouldRetry: func(_ interface{}, _ error) bool { return true },
			OnInternalFail: func() interface{} {
				return -1
			},
		}

		result, err := retry.Execute(nil, "")
		assert.EqualValues(-1, result)
		assert.EqualValues(errInvalidMaxRetriesVal, err)
	})

	t.Run("undefinedLogger", func(t *testing.T) {
		assert := assert.New(t)
		retry := Retry{
			Interval:    time.Second,
			MaxRetries:  1,
			ShouldRetry: func(_ interface{}, _ error) bool { return true },
			OnInternalFail: func() interface{} {
				return -1
			},
		}

		result, err := retry.Execute(nil, "")
		assert.EqualValues(-1, result)
		assert.EqualValues(errUndefinedLogger, err)
	})

	t.Run("undefinedOperation", func(t *testing.T) {
		assert := assert.New(t)
		retry := Retry{
			Interval:    time.Second,
			MaxRetries:  1,
			Logger: logging.DefaultLogger(),
			ShouldRetry: func(_ interface{}, _ error) bool { return true },
			OnInternalFail: func() interface{} {
				return -1
			},
		}

		result, err := retry.Execute(nil, "")
		assert.EqualValues(-1, result)
		assert.EqualValues(errUndefinedRetryOp, err)
	})
}
