package security

import (
	"bytes"
	"testing"
)

func TestEnvelopeCipherUsesFreshDataKeysAndAuthenticatesLabels(t *testing.T) {
	t.Parallel()
	cipher, err := NewCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	first, err := cipher.Seal("view-query", []byte("repo:private/project"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := cipher.Seal("view-query", []byte("repo:private/project"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) || bytes.Contains(first, []byte("private")) {
		t.Fatal("envelope ciphertext was deterministic or plaintext-bearing")
	}
	plaintext, err := cipher.Open("view-query", first)
	if err != nil || string(plaintext) != "repo:private/project" {
		t.Fatalf("round trip = %q, %v", plaintext, err)
	}
	if _, err := cipher.Open("pr-snapshot", first); err == nil {
		t.Fatal("ciphertext label substitution succeeded")
	}
	first[len(first)-1] ^= 1
	if _, err := cipher.Open("view-query", first); err == nil {
		t.Fatal("tampered ciphertext succeeded")
	}
}
