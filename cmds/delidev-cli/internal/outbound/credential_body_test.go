// SPDX-License-Identifier: Apache-2.0
package outbound

import (
	"encoding/base64"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type fragmentedReader struct{ io.Reader }

func (r fragmentedReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}
func TestNetworkProxyCredentialReflectionAcrossReads(t *testing.T) {
	c := domain.ProxyCredential{Username: "fixture-user", Password: `fixture-"password`}
	for _, secret := range []string{c.Username, c.Password, `fixture-\"password`, base64.StdEncoding.EncodeToString([]byte(c.Username + ":" + c.Password))} {
		g := newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader("safe prefix " + secret + " suffix")}), c)
		raw, err := io.ReadAll(g)
		g.Close()
		if err == nil || strings.Contains(string(raw), secret) {
			t.Fatal("reflected credential", err)
		}
	}
	g := newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader("an unrelated response")}), c)
	raw, err := io.ReadAll(g)
	g.Close()
	if err != nil || string(raw) != "an unrelated response" {
		t.Fatal("changed response", err)
	}
}

func TestNetworkShortCredentialsDoNotRejectUnrelatedJSON(t *testing.T) {
	g := newCredentialBody(io.NopCloser(strings.NewReader(`{"data":[],"value":"unrelated"}`)), domain.ProxyCredential{Username: "u", Password: "p"})
	raw, err := io.ReadAll(g)
	g.Close()
	if err != nil || len(raw) == 0 {
		t.Fatal("short credential collision", err)
	}
}

func TestNetworkShortCredentialReflectionsAcrossReadsAndEOF(t *testing.T) {
	credential := domain.ProxyCredential{Username: "x", Password: "pass123"}
	for _, value := range []string{credential.Username, credential.Password, base64.StdEncoding.EncodeToString([]byte(credential.Username)), base64.RawStdEncoding.EncodeToString([]byte(credential.Username))} {
		for _, content := range []string{value, "data: " + value + "\n\n", "data: " + value, "[" + value + "]", "safe lead " + value + " tail"} {
			for _, fragmented := range []bool{false, true} {
				var reader io.Reader = strings.NewReader(content)
				if fragmented {
					reader = fragmentedReader{reader}
				}
				guard := newCredentialBody(io.NopCloser(reader), credential)
				output, err := io.ReadAll(guard)
				guard.Close()
				if err == nil || strings.Contains(string(output), value) {
					t.Fatalf("short reflection escaped (fragmented=%v)", fragmented)
				}
			}
		}
	}
}

func TestNetworkShortCredentialTokenBoundariesPreserveUnrelatedData(t *testing.T) {
	credential := domain.ProxyCredential{Username: "x", Password: "pass123"}
	for _, content := range []string{`{"data":[],"value":"extra"}`, "extra text", "prefixx", "xylophone", "token-x-part", "_x_", "πxπ", "pass123more", "prefixpass123"} {
		for _, fragmented := range []bool{false, true} {
			var reader io.Reader = strings.NewReader(content)
			if fragmented {
				reader = fragmentedReader{reader}
			}
			guard := newCredentialBody(io.NopCloser(reader), credential)
			var output strings.Builder
			// A one-byte caller buffer also forces safe pending bytes to move
			// into previous-byte boundary state independently of source reads.
			buffer := make([]byte, 1)
			for {
				n, err := guard.Read(buffer)
				output.Write(buffer[:n])
				if err == io.EOF {
					break
				}
				if err != nil {
					guard.Close()
					t.Fatalf("unrelated token rejected: %v", err)
				}
			}
			guard.Close()
			if output.String() != content {
				t.Fatal("unrelated response changed")
			}
		}
	}
}

func TestNetworkShortCredentialHeadersUseIndependentBoundaries(t *testing.T) {
	guard := newCredentialBody(io.NopCloser(strings.NewReader("extra")), domain.ProxyCredential{Username: "x", Password: "pass123"})
	defer guard.Close()
	if _, err := io.ReadAll(guard); err != nil {
		t.Fatal("unrelated body rejected", err)
	}
	for _, header := range []string{"x", "data: x", "x; parameter", "pass123", "eA=="} {
		if !guard.contains([]byte(header)) {
			t.Fatal("short header reflection accepted")
		}
	}
	for _, header := range []string{"extra", "prefixx", "xylophone", "_x_"} {
		if guard.contains([]byte(header)) {
			t.Fatal("unrelated header rejected")
		}
	}
}

func TestNetworkShortCredentialHeaderNameBoundaries(t *testing.T) {
	guard := newCredentialBody(io.NopCloser(strings.NewReader("safe")), domain.ProxyCredential{Username: "x", Password: "pass123"})
	defer guard.Close()
	for _, name := range []string{"X", "PaSs123", "eA"} {
		if !guard.containsHeaderName(name) {
			t.Fatal("canonicalized credential field name accepted")
		}
	}
	for _, name := range []string{"X-Allowed", "Extra", "Pass123more", "Prefixx"} {
		if guard.containsHeaderName(name) {
			t.Fatal("unrelated field name rejected")
		}
	}
}

func TestNetworkShortCredentialBoundaryReadClosesOnCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	guard := newCredentialBody(reader, domain.ProxyCredential{Username: "x", Password: "pass123"})
	defer guard.Close()
	type outcome struct {
		n   int
		err error
	}
	readDone := make(chan outcome, 1)
	go func() {
		buffer := make([]byte, 1)
		n, err := guard.Read(buffer)
		readDone <- outcome{n, err}
	}()
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	// The complete candidate has been consumed but its right boundary is
	// unresolved. Closing the response must release that blocked owned read.
	closeDone := make(chan error, 1)
	go func() { closeDone <- guard.Close() }()
	select {
	case result := <-readDone:
		if result.n != 0 || result.err == nil {
			t.Fatal("unresolved credential was emitted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pending boundary read leaked")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("response close leaked")
	}
}
