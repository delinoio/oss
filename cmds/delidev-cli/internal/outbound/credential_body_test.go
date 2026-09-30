// SPDX-License-Identifier: Apache-2.0
package outbound

import (
	"encoding/base64"
	"io"
	"strings"
	"testing"

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
