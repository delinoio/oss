// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Synthetic encryption tests the envelope boundary without calling a real OS store.
type nativeTestKeyProtection struct{ aead cipher.AEAD }

func (p nativeTestKeyProtection) Protect(b []byte) ([]byte, error) {
	n := make([]byte, p.aead.NonceSize())
	if _, e := rand.Read(n); e != nil {
		return nil, e
	}
	return p.aead.Seal(n, n, b, nil), nil
}
func (p nativeTestKeyProtection) Unprotect(b []byte) ([]byte, error) {
	n := p.aead.NonceSize()
	if len(b) < n {
		return nil, errors.New("short fixture")
	}
	return p.aead.Open(nil, b[:n], b[n:], nil)
}
func nativeTestOptions(t *testing.T) NativeCAOptions {
	t.Helper()
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		t.Fatal(e)
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		t.Fatal(e)
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		t.Fatal(e)
	}
	return NativeCAOptions{Platform: "darwin", DNSNames: []string{"api.anthropic.com", "claude.ai"}, Protection: nativeTestKeyProtection{aead}, FileProtection: NativeProtector{}}
}
func nativeTestCA(t *testing.T) (*NativeCA, string, NativeCAOptions) {
	t.Helper()
	root := realTemp(t)
	opts := nativeTestOptions(t)
	ca, e := EnsureNativeCAWithOptions(root, opts)
	if e != nil {
		t.Fatal(e)
	}
	return ca, root, opts
}
func TestNativeCAPersistenceAndConstraints(t *testing.T) {
	ca, root, opts := nativeTestCA(t)
	again, e := EnsureNativeCAWithOptions(root, opts)
	if e != nil {
		t.Fatal(e)
	}
	if ca.Fingerprint != again.Fingerprint || bytes.Equal(ca.TLSCertificate.Certificate[0], again.TLSCertificate.Certificate[0]) {
		t.Fatal("CA must persist while leaf keys rotate")
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Certificate)
	for _, host := range opts.DNSNames {
		if _, e = ca.TLSCertificate.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: host}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = ca.TLSCertificate.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "example.com"}); e == nil {
		t.Fatal("off-host accepted")
	}
	offhost, e := nativeIssueLeaf(ca, []string{"example.com"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = offhost.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "example.com"}); e == nil {
		t.Fatal("CA name constraints not enforced")
	}
	if !ca.Certificate.MaxPathLenZero || len(ca.Certificate.ExcludedIPRanges) != 2 {
		t.Fatal("CA constraints missing")
	}
	public, e := ReadNativeCAPublic(root)
	if e != nil {
		t.Fatal(e)
	}
	if public.Signer != nil || public.TLSCertificate.PrivateKey != nil || public.TLSCertificate.Leaf == nil || public.Fingerprint != ca.Fingerprint {
		t.Fatal("public status must have leaf without private keys")
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{"authority.json", "ca.pem", "leaf.pem"} {
			st, e := os.Stat(filepath.Join(root, name))
			if e != nil || st.Mode().Perm() != 0600 {
				t.Fatalf("unprotected file %s", name)
			}
		}
	}
}
func TestNativeCARejectsChangedEnvelopeAndPublicFile(t *testing.T) {
	for _, kind := range []string{"sealed", "certificate", "public", "leaf", "policy", "missing-authority"} {
		t.Run(kind, func(t *testing.T) {
			_, root, opts := nativeTestCA(t)
			path := filepath.Join(root, "authority.json")
			original, _ := os.ReadFile(path)
			switch kind {
			case "public":
				os.WriteFile(filepath.Join(root, "ca.pem"), []byte("user edit"), 0600)
			case "leaf":
				os.WriteFile(filepath.Join(root, "leaf.pem"), []byte("user edit"), 0600)
			case "policy":
				opts.DNSNames = []string{"chatgpt.com"}
			case "missing-authority":
				os.Remove(path)
			default:
				var env nativeCAEnvelope
				json.Unmarshal(original, &env)
				if kind == "sealed" {
					env.Sealed[len(env.Sealed)-1] ^= 1
				} else {
					env.Certificate[len(env.Certificate)-1] ^= 1
				}
				changed, _ := json.Marshal(env)
				os.WriteFile(path, changed, 0600)
			}
			before, _ := os.ReadFile(path)
			if _, e := EnsureNativeCAWithOptions(root, opts); e == nil {
				t.Fatal("changed ownership accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("invalid authority overwritten")
			}
		})
	}
}
func TestNativeCARefusesUnsupportedAndUnsafePaths(t *testing.T) {
	opts := nativeTestOptions(t)
	opts.Platform = "linux"
	root := filepath.Join(realTemp(t), "new")
	if _, e := EnsureNativeCAWithOptions(root, opts); e == nil {
		t.Fatal("Linux accepted")
	}
	if _, e := os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("unsupported platform wrote files")
	}
	opts.Platform = "darwin"
	opts.DNSNames = []string{"example.com"}
	if _, e := EnsureNativeCAWithOptions(root, opts); e == nil {
		t.Fatal("unknown host accepted")
	}
	opts.DNSNames = []string{"claude.ai"}
	if _, e := EnsureNativeCAWithOptions("relative", opts); e == nil {
		t.Fatal("relative state accepted")
	}
	if runtime.GOOS != "windows" {
		target := realTemp(t)
		link := filepath.Join(realTemp(t), "linked")
		if e := os.Symlink(target, link); e != nil {
			t.Fatal(e)
		}
		if _, e := EnsureNativeCAWithOptions(link, opts); e == nil {
			t.Fatal("symlink accepted")
		}
	}
}
func TestNativeCAPublicRecoveryDoesNotNeedKeyDecryption(t *testing.T) {
	ca, root, _ := nativeTestCA(t)
	path := filepath.Join(root, "authority.json")
	data, _ := os.ReadFile(path)
	var env nativeCAEnvelope
	json.Unmarshal(data, &env)
	env.Sealed = []byte("unavailable DPAPI payload")
	data, _ = json.Marshal(env)
	os.WriteFile(path, data, 0600)
	loaded, e := ReadNativeCAPublic(root)
	if e != nil || loaded.Fingerprint != ca.Fingerprint {
		t.Fatalf("public recovery failed: %v", e)
	}
}

func TestNativeCAPublicRejectsChangedPublicCertificate(t *testing.T) {
	_, root, _ := nativeTestCA(t)
	if e := os.WriteFile(filepath.Join(root, "ca.pem"), []byte("user edit"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadNativeCAPublic(root); e == nil {
		t.Fatal("changed public CA accepted for readiness")
	}
}
