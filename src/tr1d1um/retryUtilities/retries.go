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
	"time"
	"github.com/go-kit/kit/log"
	"errors"
)

var (
	invalidMaxRetriesValErr = errors.New("maxRetries should be >= 1")
	undefinedShouldRetryErr = errors.New("shouldRetry function is required")
	undefinedLogger = errors.New("logger is undefined")
	undefinedRetryOp = errors.New("operation to retry is undefined")
)
type RetryStrategy interface {
	Execute(func(... interface{})(interface{}, error), ... interface{}) (interface{}, error)
}

type Retry struct {
	log.Logger
	interval time.Duration // time we wait between retries
	maxRetries int //maximum number of retries
	shouldRetry func(interface{}, error) bool // provided function to determine whether or not to retry
}

func (r *Retry) Execute(op func(... interface{}) (interface{}, error), arguments ... interface{}) (result interface{}, err error) {
	if err = r.checkDependencies(); err != nil {
		return
	}

	if op == nil {
		err = undefinedRetryOp
		return
	}

	for attempt := 0; attempt < r.maxRetries; attempt++ {
		result, err = op(arguments...)
		if !r.shouldRetry(result, err) {
			break
		}
		time.Sleep(r.interval)
	}
	return
}

func (r *Retry) checkDependencies() (err error) {
	if r.shouldRetry == nil{
		err = undefinedShouldRetryErr
		return
	}

	if r.maxRetries < 1 {
		err = invalidMaxRetriesValErr
		return
	}

	if r.Logger == nil {
		err = undefinedLogger
		return
	}

	return
}
