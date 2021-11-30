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

package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-kit/kit/log"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/webpa-common/v2/logging"
)

func sanitizeHeaders(headers http.Header) (filtered http.Header) {
	filtered = headers.Clone()
	if authHeader := filtered.Get("Authorization"); authHeader != "" {
		filtered.Del("Authorization")
		parts := strings.Split(authHeader, " ")
		if len(parts) == 2 {
			filtered.Set("Authorization-Type", parts[0])
		}
	}
	return
}

func setLogger(logger log.Logger) func(delegate http.Handler) http.Handler {
	return func(delegate http.Handler) http.Handler {
		return http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				kvs := []interface{}{"requestHeaders", sanitizeHeaders(r.Header), "requestURL", r.URL.EscapedPath(), "method", r.Method}
				kvs, _ = candlelight.AppendTraceInfo(r.Context(), kvs)
				ctx := r.WithContext(logging.WithLogger(r.Context(), log.With(logger, kvs...)))
				delegate.ServeHTTP(w, ctx)
			})
	}
}

func getLogger(ctx context.Context) log.Logger {
	logger := log.With(logging.GetLogger(ctx), "ts", log.DefaultTimestampUTC)
	return logger
}
