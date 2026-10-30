// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"
)

// NativeCA separates the signing authority from the listener's leaf certificate.
// Private material must never enter controller journals or diagnostic output.
type NativeCA struct {
	Certificate     *x509.Certificate
	Signer          crypto.Signer
	CertificatePath string
	Fingerprint     string
	TLSCertificate  tls.Certificate
}
type NativeKeyProtection interface {
	Protect([]byte) ([]byte, error)
	Unprotect([]byte) ([]byte, error)
}
type NativeCAOptions struct {
	Platform       string
	DNSNames       []string
	Protection     NativeKeyProtection
	FileProtection Protector
}
type nativeCAEnvelope struct {
	Version     int    `json:"version"`
	Platform    string `json:"platform"`
	Certificate []byte `json:"certificate"`
	Sealed      []byte `json:"sealed"`
}
type nativeCASecret struct {
	Fingerprint string `json:"fingerprint"`
	Key         []byte `json:"key"`
}

var errNativeCA = errors.New("native authority is invalid or changed; preserve its files and remove owned trust before renewal")

func EnsureNativeCA(root string) (*NativeCA, error) {
	return EnsureNativeCAWithOptions(root, NativeCAOptions{DNSNames: []string{"claude.ai"}})
}

func EnsureNativeCAWithOptions(root string, options NativeCAOptions) (*NativeCA, error) {
	if options.Platform == "" {
		options.Platform = runtime.GOOS
	}
	if options.Platform != "darwin" && options.Platform != "windows" {
		return nil, errors.New("native TLS authority is unsupported on this platform")
	}
	hosts := slices.Clone(options.DNSNames)
	slices.Sort(hosts)
	hosts = slices.Compact(hosts)
	if len(hosts) == 0 {
		return nil, errors.New("native TLS authority requires explicit supported DNS names")
	}
	for _, host := range hosts {
		if host != "claude.ai" && host != "api.anthropic.com" && host != "chatgpt.com" {
			return nil, errors.New("native TLS authority DNS name is unsupported")
		}
	}
	if options.Protection == nil {
		var err error
		options.Protection, err = nativePlatformKeyProtection(options.Platform)
		if err != nil {
			return nil, err
		}
	}
	if options.FileProtection == nil {
		options.FileProtection = NativeProtector{}
	}
	d := Disk{Root: root, Protection: options.FileProtection}
	unlock, err := d.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	path := filepath.Join(root, "authority.json")
	data, present, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	if present {
		if err = d.Protection.Protect(path, false); err != nil {
			return nil, err
		}
	}
	if !present {
		// A surviving public certificate is evidence of prior ownership, not permission to rotate.
		if _, exists, e := readConfig(filepath.Join(root, "ca.pem")); e != nil || exists {
			return nil, errNativeCA
		}
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return nil, e
		}
		serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if e != nil {
			return nil, e
		}
		now := time.Now()
		template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "TaiRouter Native " + serial.Text(16)}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(30 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, PermittedDNSDomainsCritical: true, PermittedDNSDomains: hosts, ExcludedIPRanges: []*net.IPNet{{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)}, {IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)}}}
		der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if e != nil {
			return nil, e
		}
		keyDER, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return nil, e
		}
		secret, e := json.Marshal(nativeCASecret{Fingerprint: digest(der), Key: keyDER})
		clear(keyDER)
		if e != nil {
			return nil, e
		}
		sealed, e := options.Protection.Protect(secret)
		clear(secret)
		if e != nil {
			return nil, errors.New("cannot protect native authority key")
		}
		data, e = json.Marshal(nativeCAEnvelope{Version: 1, Platform: options.Platform, Certificate: der, Sealed: sealed})
		clear(sealed)
		if e != nil {
			return nil, e
		}
		// Validate the protected round trip before publishing the only durable authority.
		if _, e = nativeDecodeCA(data, options, hosts); e != nil {
			return nil, e
		}
		if e = nativeDurableWrite(d, path, data, compareFile(path, nil, false)); e != nil {
			return nil, e
		}
	}
	ca, err := nativeDecodeCA(data, options, hosts)
	if err != nil {
		return nil, err
	}
	ca.CertificatePath = filepath.Join(root, "ca.pem")
	public := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Certificate.Raw})
	existing, exists, err := readConfig(ca.CertificatePath)
	if err != nil {
		return nil, err
	}
	if exists && !bytes.Equal(existing, public) {
		return nil, errNativeCA
	}
	if !exists {
		if err = nativeDurableWrite(d, ca.CertificatePath, public, compareFile(ca.CertificatePath, nil, false)); err != nil {
			return nil, err
		}
	}
	// Persist only the leaf's public bytes so status never has to decrypt the CA.
	// Its private key is ephemeral and stays in TLSCertificate for this process.
	leafPath := filepath.Join(root, "leaf.pem")
	old, oldExists, e := readConfig(leafPath)
	if e != nil {
		return nil, e
	}
	if oldExists {
		if _, e = nativePublicLeaf(old, ca.Certificate); e != nil {
			return nil, e
		}
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.TLSCertificate.Certificate[0]})
	if e = nativeDurableWrite(d, leafPath, leafPEM, compareFile(leafPath, old, oldExists)); e != nil {
		return nil, e
	}
	return ca, nil
}

func nativeDecodeCA(data []byte, options NativeCAOptions, hosts []string) (*NativeCA, error) {
	return nativeDecodeCAWithExpiry(data, options, hosts, false)
}

func nativeDecodeCAWithExpiry(data []byte, options NativeCAOptions, hosts []string, allowExpired bool) (*NativeCA, error) {
	var envelope nativeCAEnvelope
	if json.Unmarshal(data, &envelope) != nil || envelope.Version != 1 || envelope.Platform != options.Platform || len(envelope.Sealed) == 0 {
		return nil, errNativeCA
	}
	cert, err := x509.ParseCertificate(envelope.Certificate)
	if err != nil || !cert.IsCA || cert.CheckSignatureFrom(cert) != nil || !cert.PermittedDNSDomainsCritical || !slices.Equal(cert.PermittedDNSDomains, hosts) || !cert.MaxPathLenZero || !slices.Equal(cert.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) || len(cert.ExcludedIPRanges) != 2 {
		return nil, errNativeCA
	}
	if !cert.ExcludedIPRanges[0].Contains(net.ParseIP("1.2.3.4")) || !cert.ExcludedIPRanges[1].Contains(net.ParseIP("2001:db8::1")) {
		return nil, errNativeCA
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || (!allowExpired && !now.Before(cert.NotAfter)) {
		return nil, errors.New("native authority expired or not yet valid; remove owned trust before renewal")
	}
	secret, err := options.Protection.Unprotect(envelope.Sealed)
	if err != nil {
		return nil, errors.New("cannot unprotect native authority key")
	}
	defer clear(secret)
	var saved nativeCASecret
	if json.Unmarshal(secret, &saved) != nil || saved.Fingerprint != digest(cert.Raw) {
		return nil, errNativeCA
	}
	defer clear(saved.Key)
	parsed, err := x509.ParsePKCS8PrivateKey(saved.Key)
	if err != nil {
		return nil, errNativeCA
	}
	signer, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, errNativeCA
	}
	public, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil || !bytes.Equal(public, cert.RawSubjectPublicKeyInfo) {
		return nil, errNativeCA
	}
	ca := &NativeCA{Certificate: cert, Signer: signer, Fingerprint: digest(cert.Raw)}
	if allowExpired && !now.Before(cert.NotAfter) {
		return ca, nil
	}
	ca.TLSCertificate, err = nativeIssueLeaf(ca, hosts)
	return ca, err
}
func nativeIssueLeaf(ca *NativeCA, hosts []string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	expires := time.Now().Add(24 * time.Hour)
	if ca.Certificate.NotAfter.Before(expires) {
		expires = ca.Certificate.NotAfter
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: hosts[0]}, DNSNames: hosts, NotBefore: time.Now().Add(-time.Minute), NotAfter: expires, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.Certificate, &key.PublicKey, ca.Signer)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.Certificate.Raw}, PrivateKey: key, Leaf: leaf}, nil
}
func nativeDurableWrite(d Disk, path string, data []byte, validators ...func() error) error {
	if err := safeParents(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".native-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = d.Protection.Protect(f.Name(), false); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = safeParents(path); err != nil {
		return err
	}
	for _, validate := range validators {
		if err = validate(); err != nil {
			return err
		}
	}
	if err = nativePublishFile(f.Name(), path); err != nil {
		return err
	}
	return nativeSyncDirectory(filepath.Dir(path))
}

// Owner-only files protect macOS private material, as in the pinned runtime.
type nativePermissionKeyProtection struct{}

func (nativePermissionKeyProtection) Protect(b []byte) ([]byte, error)   { return bytes.Clone(b), nil }
func (nativePermissionKeyProtection) Unprotect(b []byte) ([]byte, error) { return bytes.Clone(b), nil }

// ReadNativeCAPublic does not decrypt keys or write files, and permits expired CA cleanup.
func ReadNativeCAPublic(root string) (*NativeCA, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("native authority directory must be absolute")
	}
	data, present, err := readConfig(filepath.Join(root, "authority.json"))
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, os.ErrNotExist
	}
	var envelope nativeCAEnvelope
	if json.Unmarshal(data, &envelope) != nil || envelope.Version != 1 {
		return nil, errNativeCA
	}
	cert, err := x509.ParseCertificate(envelope.Certificate)
	if err != nil || !cert.IsCA || cert.CheckSignatureFrom(cert) != nil {
		return nil, errNativeCA
	}
	ca := &NativeCA{Certificate: cert, CertificatePath: filepath.Join(root, "ca.pem"), Fingerprint: digest(cert.Raw)}
	public, exists, err := readConfig(ca.CertificatePath)
	if err != nil {
		return nil, err
	}
	expected := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if !exists || !bytes.Equal(public, expected) {
		return nil, errNativeCA
	}
	leafData, exists, err := readConfig(filepath.Join(root, "leaf.pem"))
	if err != nil {
		return nil, err
	}
	if exists {
		leaf, e := nativePublicLeaf(leafData, cert)
		if e != nil {
			return nil, e
		}
		ca.TLSCertificate = tls.Certificate{Certificate: [][]byte{leaf.Raw, cert.Raw}, Leaf: leaf}
	}
	return ca, nil
}

func nativePublicLeaf(data []byte, cert *x509.Certificate) (*x509.Certificate, error) {
	block, rest := pem.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || block.Type != "CERTIFICATE" {
		return nil, errNativeCA
	}
	leaf, e := x509.ParseCertificate(block.Bytes)
	if e != nil || leaf.CheckSignatureFrom(cert) != nil || leaf.IsCA || !slices.Equal(leaf.DNSNames, cert.PermittedDNSDomains) {
		return nil, errNativeCA
	}
	return leaf, nil
}
