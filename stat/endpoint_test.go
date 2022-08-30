/**
 * Copyright 2022 Comcast Cable Communications Management, LLC
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

package stat

import (
	"context"
	"testing"
)

func TestMakeStatEndpoint(t *testing.T) {
	s := new(MockService)
	endpoint := makeStatEndpoint(s)

	sr := &statRequest{
		DeviceID:        "mac:1122334455",
		AuthHeaderValue: "a0",
	}

	s.On("RequestStat", context.TODO(), "a0", "mac:1122334455").Return(nil, nil)

	endpoint(context.TODO(), sr)
	s.AssertExpectations(t)
}
