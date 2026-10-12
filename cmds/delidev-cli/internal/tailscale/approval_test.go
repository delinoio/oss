// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"bytes"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
	"time"
)

func TestGrantRequiresBothOriginalKeysAndCompleteTranscript(t *testing.T) {
	requester, requesterPublic, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(requester)
	target, targetPublic, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(target)
	original := Approval{ID: domain.NewID(), ServerID: domain.NewID(), Origin: "https://target.example.ts.net:8443", Role: "client", Name: "Fixture", RequesterKey: requesterPublic, TargetKey: targetPublic, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	ciphertext, err := Encrypt(target, requesterPublic, original.Transcript(), []byte("private original grant"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Decrypt(requester, targetPublic, original.Transcript(), ciphertext)
	if err != nil || !bytes.Equal(plain, []byte("private original grant")) {
		t.Fatal(err)
	}
	clear(plain)
	changes := []Approval{original, original, original, original, original}
	changes[0].Origin = "https://foreign.example.ts.net:8443"
	changes[1].Role = "worker"
	changes[2].ServerID = domain.NewID()
	changes[3].ExpiresAt = original.ExpiresAt.Add(time.Second)
	changes[4].RequesterKey = targetPublic
	for _, changed := range changes {
		if _, err := Decrypt(requester, targetPublic, changed.Transcript(), ciphertext); err == nil {
			t.Fatal("changed transcript decrypted")
		}
	}
	foreign, _, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(foreign, targetPublic, original.Transcript(), ciphertext); err == nil {
		t.Fatal("foreign requester decrypted")
	}
	clear(foreign)
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := Decrypt(requester, targetPublic, original.Transcript(), ciphertext); err == nil {
		t.Fatal("corrupted grant decrypted")
	}
}
