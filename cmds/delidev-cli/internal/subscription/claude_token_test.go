// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestClaudeTokenEnvelopeOriginalBinding(t *testing.T) {
	server, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	raw, e := SealClaudeToken(server, account, generation, []byte("opaque.synthetic-token"))
	if e != nil {
		t.Fatal(e)
	}
	defer clear(raw)
	token, e := ParseClaudeToken(raw, server, account, generation)
	if e != nil || token.Token != "opaque.synthetic-token" {
		t.Fatal("original envelope", e)
	}
	for _, scope := range [][3]domain.ID{{domain.NewID(), account, generation}, {server, domain.NewID(), generation}, {server, account, domain.NewID()}} {
		if _, e := ParseClaudeToken(raw, scope[0], scope[1], scope[2]); e == nil {
			t.Fatal("foreign binding accepted")
		}
	}
	if _, e := SealClaudeToken(server, account, generation, []byte("a token")); e == nil {
		t.Fatal("invalid token accepted")
	}
	if _, e := ParseClaudeToken(bytes.Replace(raw, []byte(`"version":1`), []byte(`"unknown":1,"version":1`), 1), server, account, generation); e == nil {
		t.Fatal("unknown envelope field accepted")
	}
}
