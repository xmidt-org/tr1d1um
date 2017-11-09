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

package retryUtilities

import (
	"testing"
	"time"

	"github.com/Comcast/webpa-common/logging"
	"github.com/stretchr/testify/assert"
)

func TestRetryExecute(t *testing.T) {

	t.Run("MaxRetries", func(t *testing.T) {
		assert := assert.New(t)
		retry := Retry{
			Logger:      logging.DefaultLogger(),
			maxRetries:  2,
			shouldRetry: func(_ interface{}, _ error) bool { return true },
		}

		countOp := func(args ...interface{}) (count interface{}, err error) {
			count = args[0].(int) + 1
			return
		}
		resp, err := retry.Execute(countOp, 0)
		assert.Nil(err)
		assert.EqualValues(retry.maxRetries, resp.(int))
	})

	t.Run("FailInBetween", func(t *testing.T) {
		assert := assert.New(t)
		retry := Retry{
			Logger:     logging.DefaultLogger(),
			maxRetries: 2,
			shouldRetry: func(c interface{}, _ error) bool {
				if c.(int) < 1 {
					return true
				}
				return false
			},
		}

		countOp := func(args ...interface{}) (count interface{}, err error) {
			count = args[0].(int) + 1
			return
		}
		resp, err := retry.Execute(countOp, 0)
		assert.Nil(err)
		assert.EqualValues(retry.maxRetries-1, resp.(int))
	})
}

func TestRetryCheckDependencies(t *testing.T) {
	t.Run("shouldRetryUndefined", func(t *testing.T) {
		assert := assert.New(t)
		retry := Retry{
			interval:   time.Second,
			maxRetries: 3,
			Logger:     logging.DefaultLogger(),
		}

		result, err := retry.Execute(nil, "")
		assert.Nil(result)
		assert.EqualValues(undefinedShouldRetryErr, err)
	})

	t.Run("InvalidmaxRetries", func(t *testing.T) {
		assert := assert.New(t)
		retry := Retry{
			interval:    time.Second,
			maxRetries:  0,
			Logger:      logging.DefaultLogger(),
			shouldRetry: func(_ interface{}, _ error) bool { return true },
		}

		result, err := retry.Execute(nil, "")
		assert.Nil(result)
		assert.EqualValues(invalidMaxRetriesValErr, err)
	})

	t.Run("undefinedLogger", func(t *testing.T) {
		assert := assert.New(t)
		retry := Retry{
			interval:    time.Second,
			maxRetries:  1,
			shouldRetry: func(_ interface{}, _ error) bool { return true },
		}

		result, err := retry.Execute(nil, "")
		assert.Nil(result)
		assert.EqualValues(undefinedLogger, err)
	})
}
