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

package customErrors

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"

	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/xmidt-org/tr1d1um/contextValues"
	"github.com/xmidt-org/webpa-common/v2/logging"
)

// ErrTr1d1umInternal should be the error shown to external API consumers in Internal Server error cases
var ErrTr1d1umInternal = errors.New("oops! Something unexpected went wrong in this service")

// CodedError describes the behavior of an error that additionally has an HTTP status code used for TR1D1UM business logic
type CodedError interface {
	error
	StatusCode() int
}

type codedError struct {
	error
	statusCode int
}

type GetLoggerFunc func(context.Context) kitlog.Logger

func (c *codedError) StatusCode() int {
	return c.statusCode
}

// NewBadRequestError is the constructor for an error returned for bad HTTP requests to tr1d1um
func NewBadRequestError(e error) CodedError {
	return NewCodedError(e, http.StatusBadRequest)
}

// NewCodedError upgrades an Error to a CodedError
// e must not be non-nil to avoid panics
func NewCodedError(e error, code int) CodedError {
	return &codedError{
		error:      e,
		statusCode: code,
	}
}

// ErrorLogEncoder decorates the errorEncoder in such a way that
// errors are logged with their corresponding unique request identifier
func ErrorLogEncoder(getLogger GetLoggerFunc, ee kithttp.ErrorEncoder) kithttp.ErrorEncoder {
	if getLogger == nil {
		getLogger = func(_ context.Context) kitlog.Logger {
			return nil
		}
	}

	return func(ctx context.Context, e error, w http.ResponseWriter) {
		code := http.StatusInternalServerError
		var sc kithttp.StatusCoder
		if errors.As(e, &sc) {
			code = sc.StatusCode()
		}
		logger := getLogger(ctx)
		if logger != nil && code != http.StatusNotFound {
			logger.Log("sending non-200 response, non-404 response", level.Key(), level.ErrorValue(),
				logging.ErrorKey(), e.Error(), "tid", ctx.Value(contextValues.ContextKeyRequestTID).(string),
			)
		}
		ee(ctx, e, w)
	}
}

// GenTID generates a 16-byte long string
// it returns "N/A" in the extreme case the random string could not be generated
func GenTID() (tid string) {
	buf := make([]byte, 16)
	tid = "N/A"
	if _, err := rand.Read(buf); err == nil {
		tid = base64.RawURLEncoding.EncodeToString(buf)
	}
	return
}

func GetLogger(ctx context.Context) kitlog.Logger {
	logger := kitlog.With(logging.GetLogger(ctx), "ts", kitlog.DefaultTimestampUTC)
	return logger
}
