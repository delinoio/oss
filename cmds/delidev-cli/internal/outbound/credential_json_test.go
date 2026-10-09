// SPDX-License-Identifier: Apache-2.0
package outbound

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func escapedCredential(value string, upper bool) string {
	var out strings.Builder
	for _, r := range value {
		units := []uint16{uint16(r)}
		if r > 0xffff {
			a, b := utf16.EncodeRune(r)
			units = []uint16{uint16(a), uint16(b)}
		}
		for _, unit := range units {
			if upper {
				fmt.Fprintf(&out, `\u%04X`, unit)
			} else {
				fmt.Fprintf(&out, `\u%04x`, unit)
			}
		}
	}
	return out.String()
}

type trackedCredentialReader struct {
	io.Reader
	closed bool
}

func (r *trackedCredentialReader) Close() error { r.closed = true; return nil }

func TestCredentialDecodedJSONReflectionsStayWithheld(t *testing.T) {
	credential := domain.ProxyCredential{Username: "fixture-user", Password: "fixture-password"}
	for _, value := range []string{credential.Username, credential.Password, credential.Username + ":" + credential.Password, "fixture-🔒-password"} {
		current := credential
		if strings.Contains(value, "🔒") {
			current.Password = value
		}
		encoded, _ := json.Marshal(value)
		for _, form := range []string{string(encoded[1 : len(encoded)-1]), `\u0066` + value[1:], escapedCredential(value, false), escapedCredential(value, true)} {
			for _, wire := range []string{`{"data":[{"id":"` + form + `"}]}`, `{"` + form + `":"ordinary"}`, "data: {\"value\":\"" + form + "\"}\n\n"} {
				for _, fragmented := range []bool{false, true} {
					var source io.Reader = strings.NewReader(wire)
					if fragmented {
						source = fragmentedReader{source}
					}
					original := &trackedCredentialReader{Reader: source}
					guard := newCredentialBody(original, current)
					var output strings.Builder
					buffer := make([]byte, 1)
					var final error
					for {
						n, err := guard.Read(buffer)
						output.Write(buffer[:n])
						if err != nil {
							final = err
							break
						}
					}
					if final == io.EOF || final == nil || !original.closed {
						t.Fatal("decoded reflection accepted or original body not closed", final)
					}
					if strings.Contains(output.String(), form) {
						t.Fatal("protected wire representation released")
					}
					guard.Close()
				}
			}
		}
	}
}

func TestCredentialDecodedJSONPreservesUnrelatedWireBytes(t *testing.T) {
	credential := domain.ProxyCredential{Username: "x", Password: "pass123"}
	for _, wire := range []string{
		`{"data":[{"id":"ordinary","name":"Caf\u00e9 \uD83D\uDD12"}]}`,
		`{"value":"e\u0078tra prefixpass123 \u0070ass123more"}`,
		`{"value":"\\u0078 literal escape"}`,
		"data: {\"value\":\"e\\u0078tra\"}\n\n",
		`{"value":"\uD800 then \u0061"}`,
		`{"value":"\uD800\u0061"}`,
		`{"value":"invalid \z remains the caller's bytes"}`,
		"ordinary non-JSON text and SSE\n\n",
	} {
		guard := newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader(wire)}), credential)
		var output strings.Builder
		buffer := make([]byte, 1)
		for {
			n, err := guard.Read(buffer)
			output.Write(buffer[:n])
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal("unrelated JSON rejected", err)
			}
		}
		guard.Close()
		if output.String() != wire {
			t.Fatal("allowed wire bytes changed")
		}
	}
}

func TestCredentialJSONGuardRetainsOnlyBoundedPrefixes(t *testing.T) {
	credential := domain.ProxyCredential{Username: "fixture-user", Password: "fixture-password"}
	wire := `{"value":"` + strings.Repeat(`\u0061`, 100000) + `"}`
	source := strings.NewReader(wire)
	guard := newCredentialBody(io.NopCloser(source), credential)
	buffer := make([]byte, 256)
	n, err := guard.Read(buffer)
	if err != nil || n == 0 || source.Len() == 0 {
		t.Fatal("guard buffered entire unrelated string", err)
	}
	var output strings.Builder
	output.Write(buffer[:n])
	for {
		n, err = guard.Read(buffer)
		output.Write(buffer[:n])
		if len(guard.jsonGuard.decoded) > 32 || len(guard.jsonGuard.escape) > 12 || len(guard.pending) > 8192+12 {
			t.Fatal("unbounded JSON state")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	guard.Close()
	if output.String() != wire {
		t.Fatal("bounded stream changed")
	}
}

type blockedCredentialPrefix struct {
	prefix  []byte
	reading chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (r *blockedCredentialPrefix) Read(p []byte) (int, error) {
	if len(r.prefix) > 0 {
		n := copy(p, r.prefix)
		r.prefix = r.prefix[n:]
		return n, nil
	}
	r.once.Do(func() { close(r.reading) })
	<-r.closed
	return 0, io.ErrClosedPipe
}
func (r *blockedCredentialPrefix) Close() error { close(r.closed); return nil }
func TestCredentialJSONCancellationJoinsWithoutReleasingPrefix(t *testing.T) {
	original := &blockedCredentialPrefix{prefix: []byte(`{"value":"\u0066`), reading: make(chan struct{}), closed: make(chan struct{})}
	guard := newCredentialBody(original, domain.ProxyCredential{Username: "fixture-user", Password: "fixture-password"})
	buffer := make([]byte, 256)
	if n, err := guard.Read(buffer); err != nil || string(buffer[:n]) != `{"value":"` {
		t.Fatal("unresolved escape was released", err)
	}
	readDone := make(chan int, 1)
	go func() { n, _ := guard.Read(buffer); readDone <- n }()
	<-original.reading
	closeDone := make(chan error, 1)
	go func() { closeDone <- guard.Close() }()
	select {
	case n := <-readDone:
		if n != 0 {
			t.Fatal("cancellation released protected prefix")
		}
	case <-time.After(time.Second):
		t.Fatal("read did not join")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not join")
	}
}
