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
	"errors"
	"time"

	"github.com/Comcast/webpa-common/logging"
	"github.com/go-kit/kit/log"
)

var (
	errInvalidMaxRetriesVal = errors.New("retry: MaxRetries should be >= 1")
	errUndefinedShouldRetry = errors.New("retry: ShouldRetry function is required")
	errUndefinedLogger         = errors.New("retry: logger is undefined")
	errUndefinedRetryOp        = errors.New("retry: operation to retry is undefined")
)

//RetryStrategy defines
type RetryStrategy interface {
	Execute(func(...interface{}) (interface{}, error), ...interface{}) (interface{}, error)
}


//Retry is the realization of RetryStrategy. It
type Retry struct {
	log.Logger
	Interval       time.Duration                 // time we wait between retries
	MaxRetries     int                           //maximum number of retries
	ShouldRetry    func(interface{}, error) bool // provided function to determine whether or not to retry
	OnInternalFail func() interface{}            // provided function to define some result in the case of failure
}

//RetryFactory is the fool-proof method to get a RetryStrategy struct initialized
type RetryStrategyFactory struct{}

func (r RetryStrategyFactory) NewRetryStrategy(logger log.Logger, interval time.Duration, maxRetries int,
	shouldRetry func(interface{}, error) bool, onInternalFailure func() interface{}) RetryStrategy {
	retry := &Retry{
		Logger:         logger,
		Interval:       interval,
		MaxRetries:     maxRetries,
		ShouldRetry:    shouldRetry,
		OnInternalFail: onInternalFailure,
	}
	return retry
}

func (r *Retry) Execute(op func(...interface{}) (interface{}, error), arguments ...interface{}) (result interface{}, err error) {
	var (
		debugLogger = logging.Debug(r.Logger)
	)

	if err = r.checkDependencies(); err != nil {
		result = r.OnInternalFail()
		return
	}

	if op == nil {
		result, err = r.OnInternalFail(), errUndefinedRetryOp
		return
	}

	for attempt := 0; attempt < r.MaxRetries; attempt++ {
		debugLogger.Log(logging.MessageKey(), "Attempting operation", "attempt", attempt)

		result, err = op(arguments...)
		if !r.ShouldRetry(result, err) {
			break
		}
		time.Sleep(r.Interval)
	}
	return
}

func (r *Retry) checkDependencies() (err error) {
	if r.ShouldRetry == nil {
		err = errUndefinedShouldRetry
		return
	}

	if r.MaxRetries < 1 {
		err = errInvalidMaxRetriesVal
		return
	}

	if r.Logger == nil {
		err = errUndefinedLogger
		return
	}

	return
}
