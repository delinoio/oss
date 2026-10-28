// SPDX-License-Identifier: Apache-2.0
package core

import (
	"bytes"
	"testing"
)

func TestEnvelopeContextAndTamper(t *testing.T) {
	e, err := NewEnvelope("v1", map[string][]byte{"v1": make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	one, err := e.Encrypt([]byte("secret"), "org/user/state")
	if err != nil {
		t.Fatal(err)
	}
	two, _ := e.Encrypt([]byte("secret"), "org/user/state")
	if bytes.Equal(one, two) {
		t.Fatal("nonce reused")
	}
	if _, err = e.Decrypt(one, "other/user/state"); err == nil {
		t.Fatal("cross-scope ciphertext accepted")
	}
	one[len(one)-4] ^= 1
	if _, err = e.Decrypt(one, "org/user/state"); err == nil {
		t.Fatal("tamper accepted")
	}
}
