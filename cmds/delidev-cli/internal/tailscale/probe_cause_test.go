// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
)

type probeIdentity struct {
	Version int `json:"version"`
}

func probeCase(t *testing.T, handler http.HandlerFunc, secure bool, timeout time.Duration, classify bool) error {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = newProbeClient().CheckRedirect
	if !secure {
		client = newProbeClient()
	}
	var out probeIdentity
	return probeURL(context.Background(), client, server.URL+"/delidev/tailscale/v1/identity", timeout, &out, classify)
}

func TestCheckProbeClassifiesProvenCauses(t *testing.T) {
	ok := func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"version":1}`)) }
	tests := []struct {
		name    string
		handler http.HandlerFunc
		secure  bool
		timeout time.Duration
		cause   ProbeCause
		code    domain.Code
	}{
		{"accepted", ok, true, time.Second, "", ""},
		{"timeout", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }, true, 150 * time.Millisecond, ProbeTimeout, domain.Unavailable},
		{"certificate", ok, false, time.Second, ProbeCertificateInvalid, domain.Unavailable},
		{"status", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "secret body", http.StatusInternalServerError)
		}, true, time.Second, ProbeUnexpectedHTTP, domain.Unavailable},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://elsewhere.invalid/", http.StatusFound)
		}, true, time.Second, ProbeUnexpectedHTTP, domain.Unavailable},
		{"undecodable", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) }, true, time.Second, ProbeInvalidIdentity, domain.Unsupported},
		{"oversize stays generic", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("a", 9000))) }, true, time.Second, "", domain.Unavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := probeCase(t, test.handler, test.secure, test.timeout, true)
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			safe := domain.SafeError(err)
			if safe.Code != test.code || ProbeCauseOf(err) != test.cause || safe.Cause != string(test.cause) {
				t.Fatalf("code=%s cause=%q err=%v", safe.Code, safe.Cause, err)
			}
			text := safe.Message + safe.Guidance + safe.Cause
			for _, leak := range []string{"127.0.0.1", "secret body", "elsewhere", "x509", "unknown authority", "refused"} {
				if strings.Contains(text, leak) {
					t.Fatalf("safe error leaked %q: %s", leak, text)
				}
			}
		})
	}
}

func TestCheckProbeConnectionRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	var out probeIdentity
	err = probeURL(context.Background(), newProbeClient(), "https://"+address+"/delidev/tailscale/v1/identity", time.Second, &out, true)
	if ProbeCauseOf(err) != ProbeConnectionRefused || domain.SafeError(err).Code != domain.Unavailable {
		t.Fatalf("closed port was not proven refused: %v", err)
	}
	// The access-enable path keeps the generic error without any cause.
	err = probeURL(context.Background(), newProbeClient(), "https://"+address+"/delidev/tailscale/v1/identity", time.Second, &out, false)
	if domain.SafeError(err).Code != domain.Unavailable || domain.SafeError(err).Cause != "" {
		t.Fatalf("non-classifying probe attached a cause: %v", err)
	}
}

func TestProbeKeepsGenericErrorsWithoutClassification(t *testing.T) {
	generic := fail(domain.Unavailable).(*domain.Error)
	cases := map[string]func(http.ResponseWriter, *http.Request){
		"status":      func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"undecodable": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) },
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			err := probeCase(t, handler, true, time.Second, false)
			safe := domain.SafeError(err)
			if safe.Cause != "" || safe.Message != generic.Message || safe.Guidance != generic.Guidance {
				t.Fatalf("access probe changed: %#v", safe)
			}
		})
	}
}

func TestClassifyTransportRequiresProof(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	refused := &net.OpError{Op: "dial", Err: &net.OpError{Op: "connect", Err: syscall.ECONNREFUSED}}
	tests := []struct {
		name   string
		parent context.Context
		err    error
		cause  ProbeCause
	}{
		{"refused", context.Background(), refused, ProbeConnectionRefused},
		{"windows refused", context.Background(), &net.OpError{Op: "dial", Err: wsaeConnRefused}, ProbeConnectionRefused},
		{"deadline", context.Background(), fmt.Errorf("get: %w", context.DeadlineExceeded), ProbeTimeout},
		{"parent canceled", canceled, context.Canceled, ""},
		{"parent canceled while refused", canceled, refused, ""},
		{"dns", context.Background(), &net.DNSError{Err: "no such host", Name: "peer.invalid", IsNotFound: true}, ""},
		{"dns timeout", context.Background(), &net.DNSError{Err: "i/o timeout", Name: "peer.invalid", IsTimeout: true}, ""},
		{"reset", context.Background(), &net.OpError{Op: "read", Err: syscall.ECONNRESET}, ""},
		{"unknown", context.Background(), errors.New("boom"), ""},
		{"nil", context.Background(), nil, ""},
	}
	for _, test := range tests {
		if got := classifyTransport(test.parent, test.err); got != test.cause {
			t.Errorf("%s: got %q want %q", test.name, got, test.cause)
		}
	}
}

func TestProbeCauseCrossesRPCErrorDetail(t *testing.T) {
	for _, cause := range []ProbeCause{ProbeConnectionRefused, ProbeTimeout, ProbeCertificateInvalid, ProbeUnexpectedHTTP} {
		client := rpc.ClientError(rpc.Error(probeFailure(domain.Unavailable, cause), ""))
		if client.Code != domain.Unavailable || client.Cause != string(cause) {
			t.Fatalf("%s: %#v", cause, client)
		}
	}
	client := rpc.ClientError(rpc.Error(InvalidIdentity(), ""))
	if client.Code != domain.Unsupported || client.Cause != string(ProbeInvalidIdentity) {
		t.Fatalf("%#v", client)
	}
}

// stalledBody sends 200 headers and part of a declared body, then waits for the
// request to end so client.Do succeeds and the failure surfaces from the body read.
func stalledBody(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Length", "64")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"version"`))
	w.(http.Flusher).Flush()
	<-r.Context().Done()
}

func TestCheckProbeClassifiesBodyReadTimeout(t *testing.T) {
	err := probeCase(t, stalledBody, true, 150*time.Millisecond, true)
	safe := domain.SafeError(err)
	if safe.Code != domain.Unavailable || ProbeCauseOf(err) != ProbeTimeout || safe.Cause != string(ProbeTimeout) {
		t.Fatalf("stalled identity body was not proven a timeout: code=%s cause=%q err=%v", safe.Code, safe.Cause, err)
	}
	// The access-enable path keeps the generic error without any cause.
	err = probeCase(t, stalledBody, true, 150*time.Millisecond, false)
	safe = domain.SafeError(err)
	if safe.Code != domain.Unavailable || safe.Cause != "" {
		t.Fatalf("non-classifying probe attached a body-read cause: %#v", safe)
	}
}

func TestCheckProbeBodyReadKeepsGenericWithoutProof(t *testing.T) {
	// A connection closed mid-body is not a proven timeout.
	truncated := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "64")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version"`))
		w.(http.Flusher).Flush()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}
	err := probeCase(t, truncated, true, time.Second, true)
	if safe := domain.SafeError(err); safe.Code != domain.Unavailable || safe.Cause != "" {
		t.Fatalf("truncated body gained a cause: %#v", safe)
	}

	// A canceled parent never proves a peer timeout, even for a stalled body.
	server := httptest.NewTLSServer(http.HandlerFunc(stalledBody))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = newProbeClient().CheckRedirect
	parent, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	var out probeIdentity
	err = probeURL(parent, client, server.URL+"/delidev/tailscale/v1/identity", 5*time.Second, &out, true)
	if safe := domain.SafeError(err); safe.Code != domain.Unavailable || safe.Cause != "" {
		t.Fatalf("parent cancellation during body read gained a cause: %#v", safe)
	}
}

func TestClassifyTransportBodyReadErrors(t *testing.T) {
	bodyTimeout := fmt.Errorf("read: %w", &net.OpError{Op: "read", Err: context.DeadlineExceeded})
	if got := classifyTransport(context.Background(), bodyTimeout); got != ProbeTimeout {
		t.Errorf("body timeout: got %q", got)
	}
	if got := classifyTransport(context.Background(), io.ErrUnexpectedEOF); got != "" {
		t.Errorf("unexpected EOF: got %q", got)
	}
}
