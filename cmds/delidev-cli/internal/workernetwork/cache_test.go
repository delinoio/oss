// SPDX-License-Identifier: Apache-2.0
package workernetwork

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type isolatedStore struct {
	values  map[credentials.Ref][]byte
	locked  bool
	failPut bool
}

func (s *isolatedStore) Get(_ context.Context, r credentials.Ref) ([]byte, error) {
	if s.locked {
		return nil, domain.Fail(domain.Unavailable, "Fixture store locked.", "")
	}
	value, ok := s.values[r]
	if !ok {
		return nil, os.ErrNotExist
	}
	return bytes.Clone(value), nil
}
func (s *isolatedStore) Put(_ context.Context, r credentials.Ref, v []byte) (string, error) {
	if s.locked || s.failPut {
		return "", domain.Fail(domain.Unavailable, "Fixture store unavailable.", "")
	}
	if old, ok := s.values[r]; ok && !bytes.Equal(old, v) {
		return "", errors.New("immutable fixture reference changed")
	}
	s.values[r] = bytes.Clone(v)
	return "fixture-protected-reference", nil
}
func (s *isolatedStore) Delete(_ context.Context, r credentials.Ref) error {
	delete(s.values, r)
	return nil
}
func (s *isolatedStore) UnremovedReferences(_ context.Context, owner domain.ID) ([]credentials.Ref, error) {
	var refs []credentials.Ref
	for r := range s.values {
		if r.Owner == owner {
			refs = append(refs, r)
		}
	}
	return refs, nil
}
func fixture(t *testing.T) (string, *isolatedStore, Authority, Recipient, Bundle) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private")
	vault := &isolatedStore{values: map[credentials.Ref][]byte{}}
	authority := Authority{ServerID: domain.NewID(), Endpoint: "https://server.example:443", MachineID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID()}
	recipient, err := Prepare(context.Background(), root, vault, authority)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bundle := Bundle{Version: 1, Authority: authority, KeyID: recipient.KeyID, Recipient: recipient.PublicKey, ExportID: domain.NewID(), RouteID: domain.NewID(), Generation: 1, Route: domain.NetworkRoute{MachineID: authority.MachineID, ProfileID: domain.NewID(), ProfileRevision: 1, Profile: domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "fixture", Mode: domain.ProxyHTTP, Host: "proxy.example", Port: 8080}, CredentialGeneration: domain.NewID()}}, Credential: &domain.ProxyCredential{Username: "network_user_sentinel", Password: "network_password_sentinel"}, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	return root, vault, authority, recipient, bundle
}
func TestRecipientEncryptedImportFencesScopeDigestAndGeneration(t *testing.T) {
	root, vault, authority, recipient, bundle := fixture(t)
	ctx := context.Background()
	ciphertext, digest, err := Encrypt(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(bundle.Credential.Password)) {
		t.Fatal("unencrypted credential in transfer")
	}
	for _, mode := range []string{"digest", "tamper", "truncated", "identity", "recipient", "expired"} {
		t.Run(mode, func(t *testing.T) {
			bad := bytes.Clone(ciphertext)
			expected := digest
			selected := authority
			switch mode {
			case "digest":
				expected = Digest([]byte("foreign"))
			case "tamper":
				bad[len(bad)-1] ^= 1
				expected = Digest(bad)
			case "truncated":
				bad = bad[:len(bad)-1]
				expected = Digest(bad)
			case "identity":
				selected.DeviceID = domain.NewID()
			case "recipient":
				other := bundle
				otherIdentity, err := age.GenerateX25519Identity()
				if err != nil {
					t.Fatal(err)
				}
				other.Recipient = otherIdentity.Recipient().String()
				bad, expected, _ = Encrypt(ctx, other)
			case "expired":
				expired := bundle
				expired.IssuedAt = time.Now().Add(-10 * time.Minute)
				expired.ExpiresAt = expired.IssuedAt.Add(5 * time.Minute)
				if _, _, err := Encrypt(ctx, expired); err == nil {
					t.Fatal("expired export allowed")
				}
				return
			}
			if _, err := Import(ctx, root, vault, selected, bad, expected); err == nil {
				t.Fatal("untrusted import published")
			}
			if _, err := os.Stat(filepath.Join(root, "network-cache.json")); !os.IsNotExist(err) {
				t.Fatal("refused import changed cache")
			}
		})
	}
	metadata, err := Import(ctx, root, vault, authority, ciphertext, digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"network-cache.json", "network-recipient.json"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(bundle.Credential.Password)) || bytes.Contains(raw, []byte("AGE-SECRET-KEY")) {
			t.Fatal("protected material entered ordinary JSON")
		}
	}
	// A new transfer nonce of identical authority may not replace the original
	// generation reference or require another native credential write.
	reexport := bundle
	reexport.ExportID = domain.NewID()
	same, sameDigest, err := Encrypt(ctx, reexport)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := Import(ctx, root, vault, authority, same, sameDigest)
	if err != nil || replayed.Reference != metadata.Reference || replayed.Generation != metadata.Generation || replayed.CiphertextDigest != sameDigest {
		t.Fatal("same generation changed original cache", err)
	}
	conflict := bundle
	conflict.Credential = &domain.ProxyCredential{Username: bundle.Credential.Username, Password: "changed_sentinel"}
	conflict.ExportID = domain.NewID()
	conflicting, changedDigest, _ := Encrypt(ctx, conflict)
	if _, err := Import(ctx, root, vault, authority, conflicting, changedDigest); err == nil {
		t.Fatal("conflicting same generation published")
	}
	next := bundle
	next.Generation = 2
	next.ExportID = domain.NewID()
	next.Route.Profile.Bypass = []domain.ProxyBypass{{Host: "server.example", Port: 443}}
	newCipher, newDigest, _ := Encrypt(ctx, next)
	vault.failPut = true
	if _, err := Import(ctx, root, vault, authority, newCipher, newDigest); err == nil {
		t.Fatal("failed protected publication accepted")
	}
	vault.failPut = false
	retained, value, err := Load(ctx, root, vault, authority)
	if err != nil || retained.Reference != metadata.Reference || retained.Generation != metadata.Generation || value.Generation != 1 {
		t.Fatal("failed import tore old cache", err)
	}
	updated, err := Import(ctx, root, vault, authority, newCipher, newDigest)
	if err != nil || updated.Generation != 2 {
		t.Fatal(err)
	}
	if _, err := Import(ctx, root, vault, authority, ciphertext, digest); err == nil {
		t.Fatal("old generation regained authority")
	}
	if _, ok := vault.values[metadata.Reference]; ok {
		t.Fatal("old derivative remains reloadable")
	}
	vault.locked = true
	if _, _, err := Load(ctx, root, vault, authority); err == nil {
		t.Fatal("locked protected cache chose fallback")
	}
	if _, err := Prepare(ctx, root, vault, authority); err == nil {
		t.Fatal("locked original key was regenerated")
	}
	vault.locked = false
	if prepared, err := Prepare(ctx, root, vault, authority); err != nil || prepared != recipient {
		t.Fatal("original recipient changed", err)
	}
}
func TestMissingProtectedDerivativeNeverReplacesGeneration(t *testing.T) {
	root, vault, authority, _, bundle := fixture(t)
	ctx := context.Background()
	cipher, digest, _ := Encrypt(ctx, bundle)
	metadata, err := Import(ctx, root, vault, authority, cipher, digest)
	if err != nil {
		t.Fatal(err)
	}
	delete(vault.values, metadata.Reference)
	replacement := bundle
	replacement.Generation++
	replacement.ExportID = domain.NewID()
	cipher, digest, _ = Encrypt(ctx, replacement)
	if _, err := Import(ctx, root, vault, authority, cipher, digest); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("missing derivative was treated as absent cache", err)
	}
}
