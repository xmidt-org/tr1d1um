// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/justinas/alice"
)

// DefaultMaxRequestBodyBytes is the most bytes of a request body tr1d1um
// reads when the configuration gives no maxRequestBodyBytes.  A WDMP
// request or a webhook registration is a few kilobytes; a mebibyte is far
// beyond any legitimate one.
const DefaultMaxRequestBodyBytes = 1 << 20

// ErrRequestBodyTooLarge is the error a request whose body is over the
// limit is refused with.
var ErrRequestBodyTooLarge = NewCodedError(errors.New("request body too large"), http.StatusRequestEntityTooLarge)

// BodyLimit returns middleware that refuses a request whose body is longer
// than limit bytes with 413 Request Entity Too Large, before the handler
// sees it.  A limit of zero or less is DefaultMaxRequestBodyBytes.
//
// The body is read here, so a handler that reads it all, as every tr1d1um
// handler does, reads at most limit bytes from memory and never from the
// network.  A request without a body passes through untouched.
func BodyLimit(limit int64) alice.Constructor {
	if limit <= 0 {
		limit = DefaultMaxRequestBodyBytes
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.ContentLength > limit {
				refuseBody(response)
				return
			}

			if request.Body != nil && request.Body != http.NoBody {
				body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, limit))
				if err != nil {
					if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
						refuseBody(response)
						return
					}

					// the body could not be read; the handler reports that
					// as it would have
					next.ServeHTTP(response, request)
					return
				}

				request.Body = io.NopCloser(bytes.NewReader(body))
				request.ContentLength = int64(len(body))
			}

			next.ServeHTTP(response, request)
		})
	}
}

// refuseBody writes the 413 in tr1d1um's error shape.
func refuseBody(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(ErrRequestBodyTooLarge.StatusCode())
	_ = json.NewEncoder(response).Encode(map[string]string{
		"message": ErrRequestBodyTooLarge.Error(),
	})
}
