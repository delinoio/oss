// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

type observedHandshakeDialer struct {
	fixtureDialer
	status int
}

func (d observedHandshakeDialer) DialHandshake(context.Context, string, http.Header, *http.Client) (Socket, *http.Response, error) {
	r := &http.Response{StatusCode: d.status, Header: http.Header{"x-quota": []string{"1"}}, Body: http.NoBody}
	if d.status != 101 {
		return nil, r, errors.New("refused handshake")
	}
	return d.s, r, nil
}
func TestResponsesSocketObservesHandshakeBeforeCreate(t *testing.T) {
	for _, status := range []int{101, 429} {
		for _, fail := range []bool{false, true} {
			h, _, _, _, u := harness(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP") }, Responses)
			socket := newFixtureSocket()
			if status == 101 && !fail {
				socket.in <- []byte(`{"type":"response.completed","response":` + fixture(Responses) + `}`)
			}
			h.c.SocketDialer = observedHandshakeDialer{fixtureDialer{socket}, status}
			observer := &responseObserverFixture{fail: fail}
			h.c.ResponseObserver = observer
			rt := h.c.Router.(testRouter)
			rt.route.Targets[0].Transport = WebSocketTransport
			h.c.Router = rt
			out := call(h, "/v1/responses", `{"model":"m","input":"hi","store":false}`)
			if len(observer.statuses) != 1 || observer.statuses[0] != status {
				t.Fatalf("observer %+v", observer)
			}
			if fail || status != 101 {
				if u.records[0].Sends != 0 || out.Code == 200 {
					t.Fatal("create escaped refusal")
				}
			} else if out.Code != 200 || u.records[0].Sends != 1 {
				t.Fatalf("%d %s", out.Code, out.Body)
			}
		}
	}
}
