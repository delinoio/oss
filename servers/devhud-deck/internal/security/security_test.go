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

func TestEnvelopeCipherRetainsVersionedKeysAndRewrapsWithoutPlaintext(t *testing.T) {
	t.Parallel()
	oldKey := bytes.Repeat([]byte{1}, 32)
	newKey := bytes.Repeat([]byte{2}, 32)
	oldCipher, err := NewVersionedCipher(
		"managed-v1", map[string][]byte{"managed-v1": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	original, err := oldCipher.Seal("github-user-access-token", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	rotatedCipher, err := NewVersionedCipher("managed-v2", map[string][]byte{
		"managed-v1": oldKey,
		"managed-v2": newKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	rewrapped, changed, err := rotatedCipher.Rewrap(
		"github-user-access-token", original)
	if err != nil || !changed || bytes.Contains(rewrapped, []byte("secret")) {
		t.Fatalf("rewrap changed=%v plaintext-bearing=%v err=%v",
			changed, bytes.Contains(rewrapped, []byte("secret")), err)
	}
	keyID, err := rotatedCipher.KeyID(rewrapped)
	if err != nil || keyID != "managed-v2" {
		t.Fatalf("rewrapped key ID = %q, %v", keyID, err)
	}
	plaintext, err := rotatedCipher.Open(
		"github-user-access-token", rewrapped)
	if err != nil || string(plaintext) != "secret" {
		t.Fatalf("rewrapped round trip = %q, %v", plaintext, err)
	}
	if _, err := oldCipher.Open(
		"github-user-access-token", rewrapped); err == nil {
		t.Fatal("rewrapped ciphertext remained decryptable by the retired keyring")
	}
	again, changed, err := rotatedCipher.Rewrap(
		"github-user-access-token", rewrapped)
	if err != nil || changed || !bytes.Equal(again, rewrapped) {
		t.Fatalf("active rewrap changed=%v err=%v", changed, err)
	}
}
