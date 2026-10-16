// SPDX-License-Identifier: Apache-2.0
package subscription_test

import (
	"net/http"
	"net/http/httptest"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func recorded(serve func(http.ResponseWriter)) *http.Response {
	recorder := httptest.NewRecorder()
	serve(recorder)
	return recorder.Result()
}
