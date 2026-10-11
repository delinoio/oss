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
	assertCredentialCancellationWithheld(t, `{"value":"\u0066`, `{"value":"`)
}
func TestCredentialSSECancellationJoinsWithoutReleasingPrefix(t *testing.T) {
	assertCredentialCancellationWithheld(t, ": \"\ndata: {\"value\":\"\\u0066", ": \"\ndata: {\"value\":\"")
}
func assertCredentialCancellationWithheld(t *testing.T, prefix, released string) {
	t.Helper()
	original := &blockedCredentialPrefix{prefix: []byte(prefix), reading: make(chan struct{}), closed: make(chan struct{})}
	guard := newCredentialBody(original, domain.ProxyCredential{Username: "fixture-user", Password: "fixture-password"})
	buffer := make([]byte, 256)
	if n, err := guard.Read(buffer); err != nil || string(buffer[:n]) != released {
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

func TestCredentialJSONEscapeDecodingMatchesJSON(t *testing.T) {
	for _, escaped := range []string{
		`\"\\\/\b\f\n\r\t`,
		`\u0000\u007f\u0080\u07ff\u0800\uFFFF`,
		`\ud83d\uDD12`,
		`\uD800`, `\uDC00`, `\uD800a`, `\uD800\n`,
		`\uD800\u0061`, `\uD800\uD800`,
		`\uD800\uD83D\uDD12`, `\uDC00\uD83D\uDD12`,
	} {
		var want string
		if err := json.Unmarshal([]byte(`"`+escaped+`"`), &want); err != nil {
			t.Fatal(err)
		}
		wire := []byte(`"` + escaped)
		for split := 0; split <= len(wire); split++ {
			// Keep the entire decoded value as a candidate prefix so it can be
			// compared with encoding/json without adding a production observer.
			guard := credentialJSONGuard{
				framing:  credentialOrdinary,
				patterns: [][]byte{[]byte(want + "\x00unmatched-suffix")},
			}
			if guard.scan(wire[:split]) || guard.scan(wire[split:]) || guard.finish() {
				t.Fatalf("unexpected reflection for %q at split %d", escaped, split)
			}
			if got := string(guard.decoded); got != want || len(guard.escape) != 0 {
				t.Fatalf("decode %q at split %d: got %q, want %q, pending %d", escaped, split, got, want, len(guard.escape))
			}
		}
	}
}

func TestCredentialJSONEscapesReuseBuffers(t *testing.T) {
	patterns := [][]byte{[]byte("credential-never-occurs")}
	for _, escape := range []string{`\n`, `\u0061`, `\uD83D\uDD12`, `\uD800a`, `\uD800\uD83D\uDD12`} {
		var baseline float64
		for _, count := range []int{1, 64, 4096} {
			wire := []byte(`{"value":"` + strings.Repeat(escape, count) + `"}`)
			failed := false
			allocs := testing.AllocsPerRun(10, func() {
				guard := credentialJSONGuard{framing: credentialOrdinary, patterns: patterns}
				if guard.scan(wire) || guard.finish() {
					failed = true
				}
			})
			if failed {
				t.Fatal("harmless escape fixture was rejected")
			}
			if count == 1 {
				baseline = allocs
			}
			if allocs > 16 || allocs > baseline+1 {
				t.Fatalf("%q repeated %d times allocated %.0f times; initial %.0f", escape, count, allocs, baseline)
			}
		}
	}
}

func TestCredentialJSONEscapeOffsets(t *testing.T) {
	for _, test := range []struct {
		wire   string
		want   string
		starts []int64
	}{
		{`"\uD83D\uDD12`, "🔒", []int64{1, 1, 1, 1}},
		{`"\uD800\u0061`, "�a", []int64{1, 1, 1, 7}},
		{`"\uD800\uD83D\uDD12`, "�🔒", []int64{1, 1, 1, 7, 7, 7, 7}},
		{`"\uD800\n`, "�\n", []int64{1, 1, 1, 7}},
	} {
		for split := 0; split <= len(test.wire); split++ {
			guard := credentialJSONGuard{framing: credentialOrdinary, patterns: [][]byte{[]byte(test.want + "unmatched-suffix")}}
			guard.scan([]byte(test.wire[:split]))
			guard.scan([]byte(test.wire[split:]))
			guard.finish()
			if string(guard.decoded) != test.want || len(guard.starts) != len(test.starts) || guard.retainFrom() != 1 {
				t.Fatalf("wrong decoded candidate for %q at split %d", test.wire, split)
			}
			for i, start := range guard.starts {
				if start != test.starts[i] {
					t.Fatalf("wrong wire offset for %q at split %d, byte %d: %d", test.wire, split, i, start)
				}
			}
		}
	}
}

func TestCredentialMalformedJSONKeepsLiteralProtection(t *testing.T) {
	credential := domain.ProxyCredential{Username: "fixture-user", Password: "fixture-password"}
	for _, escape := range []string{`\z`, `\u00xz`, `\uD800\u00xz`} {
		wire := `{"value":"` + escape + credential.Password + `"}`
		for split := 0; split <= len(wire); split++ {
			source := io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))
			original := &trackedCredentialReader{Reader: source}
			guard := newCredentialBody(original, credential, "application/json")
			output, err := io.ReadAll(guard)
			if err == nil || !original.closed || strings.Contains(string(output), credential.Password) {
				t.Fatalf("literal reflection after %q escaped protection at split %d", escape, split)
			}
			guard.Close()
		}
	}
}

func TestCredentialIncompleteJSONPreservesWire(t *testing.T) {
	credential := domain.ProxyCredential{Username: "fixture-user", Password: "fixture-password"}
	for _, escape := range []string{`\`, `\u`, `\u00`, `\uD800`, `\uD800\`, `\uD800\u`, `\uD800\u00`} {
		wire := `{"value":"` + escape
		guard := newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader(wire)}), credential, "application/json")
		output, err := io.ReadAll(guard)
		if err != nil || string(output) != wire {
			t.Fatalf("incomplete escape %q changed safe wire: %v", escape, err)
		}
		guard.Close()
	}
}

func TestCredentialJSONSurrogateTailKeepsRejection(t *testing.T) {
	credential := domain.ProxyCredential{Username: "fixture-user", Password: "fixture-password"}
	credential.Password = "�"
	// The following quote must not reset a rejection found while flushing the
	// unmatched high surrogate. This also exercises a short token at EOF.
	for _, wire := range []string{`{"value":"\uD800"}`, `{"value":"\uD800`, `{"value":"\uD800\uD83D\uDD12 "}`} {
		for split := 0; split <= len(wire); split++ {
			source := io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))
			original := &trackedCredentialReader{Reader: source}
			guard := newCredentialBody(original, credential, "application/json")
			output, err := io.ReadAll(guard)
			// Non-ASCII continuation bytes mean adjacent replacement/lock runes
			// form one token, so only the first two complete-token cases reject.
			if strings.Contains(wire, `\uD83D`) {
				if err != nil || string(output) != wire {
					t.Fatalf("embedded short token rejected at split %d: %v", split, err)
				}
			} else if err == nil || !original.closed || strings.Contains(string(output), `\uD800`) {
				t.Fatalf("surrogate reflection accepted at split %d", split)
			}
			guard.Close()
		}
	}
}

func TestCredentialJSONSurrogateFailureSurvivesTailQuote(t *testing.T) {
	credential := domain.ProxyCredential{Username: "fixture-user", Password: "fixture-�"}
	wire := `{"value":"fixture-\uD800"}`
	for split := 0; split <= len(wire); split++ {
		source := io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))
		original := &trackedCredentialReader{Reader: source}
		guard := newCredentialBody(original, credential, "application/json")
		output, err := io.ReadAll(guard)
		if err == nil || !original.closed || strings.Contains(string(output), `fixture-\uD800`) {
			t.Fatalf("tail quote cleared decoded reflection at split %d", split)
		}
		guard.Close()
	}
}

func TestCredentialJSONSurrogateChainReflection(t *testing.T) {
	credential := domain.ProxyCredential{Username: "fixture-user", Password: "fixture-�🔒-password"}
	wire := `{"value":"fixture-\uD800\uD83D\uDD12-password"}`
	for split := 0; split <= len(wire); split++ {
		source := io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))
		original := &trackedCredentialReader{Reader: source}
		guard := newCredentialBody(original, credential, "application/json")
		output, err := io.ReadAll(guard)
		if err == nil || !original.closed || strings.Contains(string(output), `fixture-\uD800`) {
			t.Fatalf("surrogate chain reflection accepted at split %d", split)
		}
		guard.Close()
	}
}
