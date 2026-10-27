// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type NativeTrustCommandRunner func(context.Context, string, []string, []byte) ([]byte, error)

// NativeTrustExecRunner never invokes a shell or includes command output in errors.
// Callers must explicitly inject it; tests must inject a fake runner.
func NativeTrustExecRunner(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out nativeBoundedOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, nativeTrustCommandError{code: exit.ExitCode()}
		}
		return nil, errors.New("native trust command failed or was cancelled")
	}
	return out.Bytes(), nil
}

type nativeTrustCommandError struct{ code int }

func (e nativeTrustCommandError) Error() string { return "native trust command failed" }
func nativeTrustExit(err error, code int) bool {
	var exit nativeTrustCommandError
	return errors.As(err, &exit) && exit.code == code
}

type nativeBoundedOutput struct{ bytes.Buffer }

func (b *nativeBoundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxConfigBytes {
		return 0, errors.New("native trust command exceeded output bound")
	}
	return b.Buffer.Write(p)
}

type nativeOSTrustAdapter struct {
	platform, home string
	run            NativeTrustCommandRunner
}

func NewNativeTrustAdapter(platform, home string, runner NativeTrustCommandRunner) (NativeTrustAdapter, error) {
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform != "darwin" && platform != "windows" {
		return nil, errors.New("native TLS trust is unsupported on this platform")
	}
	if runner == nil || !filepath.IsAbs(home) {
		return nil, errors.New("native trust requires an explicit command runner and absolute user home")
	}
	return &nativeOSTrustAdapter{platform: platform, home: home, run: runner}, nil
}
func (a *nativeOSTrustAdapter) command(ctx context.Context, args []string, input []byte, mutation bool) ([]byte, error) {
	timeout := 10 * time.Second
	if mutation {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	executable := "/usr/bin/security"
	if a.platform == "windows" {
		// Use the Windows system executable, never a PATH-provided PowerShell.
		systemRoot := os.Getenv("SystemRoot")
		if systemRoot == "" {
			systemRoot = `C:\Windows`
		}
		executable = filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	}
	return a.run(ctx, executable, args, input)
}
func (a *nativeOSTrustAdapter) keychain() string {
	return filepath.Join(a.home, "Library", "Keychains", "login.keychain-db")
}
func (a *nativeOSTrustAdapter) Status(ctx context.Context, ca *NativeCA) (NativeTrustStatus, error) {
	if err := nativeValidatePublic(ca); err != nil {
		return NativeTrustStatus{}, err
	}
	if a.platform == "windows" {
		return a.windows(ctx, ca, "inspect")
	}
	data, err := a.command(ctx, []string{"find-certificate", "-a", "-p", a.keychain()}, nil, false)
	if err != nil {
		if nativeTrustExit(err, 44) {
			return NativeTrustStatus{}, nil
		}
		return NativeTrustStatus{}, err
	}
	present := false
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		data = rest
		if block.Type == "CERTIFICATE" && bytes.Equal(block.Bytes, ca.Certificate.Raw) {
			present = true
		}
	}
	if !present {
		return NativeTrustStatus{}, nil
	}
	// Verify a real leaf with the exact current-user keychain. Presence alone does not establish trust.
	if ca.TLSCertificate.Leaf == nil {
		return NativeTrustStatus{Present: true}, nil
	}
	var verified bool
	err = a.withSnapshot(ca.TLSCertificate.Certificate[0], func(path string) error {
		_, e := a.command(ctx, []string{"verify-cert", "-q", "-L", "-c", path, "-p", "ssl", "-n", ca.TLSCertificate.Leaf.DNSNames[0], "-k", a.keychain()}, nil, false)
		if e != nil {
			return e
		}
		verified = true
		return nil
	})
	if err != nil {
		if nativeTrustExit(err, 1) {
			return NativeTrustStatus{Present: true}, nil
		}
		return NativeTrustStatus{Present: true}, err
	}
	// Chromium ignores host-scoped trust settings. Refuse them, even if security verified the leaf.
	err = a.withSnapshot(nil, func(path string) error {
		if _, e := a.command(ctx, []string{"trust-settings-export", path}, nil, false); e != nil {
			return e
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		sum := sha1.Sum(ca.Certificate.Raw)
		needle := "<key>" + strings.ToUpper(hex.EncodeToString(sum[:])) + "</key>"
		start := strings.Index(string(data), needle)
		if start < 0 {
			return errors.New("native current-user trust entry is absent")
		}
		entry := string(data[start:])
		end := strings.Index(entry, "</array>")
		if end < 0 {
			return errors.New("native current-user trust entry is invalid")
		}
		if strings.Contains(entry[:end], "kSecTrustSettingsPolicyString") {
			return errors.New("native trust has an unsupported host-scoped policy")
		}
		return nil
	})
	return NativeTrustStatus{Present: true, Trusted: verified && err == nil}, err
}
func (a *nativeOSTrustAdapter) Install(ctx context.Context, ca *NativeCA) error {
	if err := nativeValidatePublic(ca); err != nil {
		return err
	}
	if ca.Signer == nil || !time.Now().Before(ca.Certificate.NotAfter) {
		return errors.New("native trust installation requires a current protected authority")
	}
	if a.platform == "windows" {
		_, err := a.windows(ctx, ca, "install")
		return err
	}
	return a.withSnapshot(ca.Certificate.Raw, func(path string) error {
		_, err := a.command(ctx, []string{"add-trusted-cert", "-r", "trustRoot", "-p", "ssl", "-k", a.keychain(), path}, nil, true)
		return err
	})
}
func (a *nativeOSTrustAdapter) Remove(ctx context.Context, ca *NativeCA) error {
	if err := nativeValidatePublic(ca); err != nil {
		return err
	}
	if a.platform == "windows" {
		_, err := a.windows(ctx, ca, "remove")
		return err
	}
	return a.withSnapshot(ca.Certificate.Raw, func(path string) error {
		if _, err := a.command(ctx, []string{"remove-trusted-cert", path}, nil, true); err != nil {
			return err
		}
		sum := sha1.Sum(ca.Certificate.Raw)
		_, err := a.command(ctx, []string{"delete-certificate", "-Z", strings.ToUpper(hex.EncodeToString(sum[:])), a.keychain()}, nil, true)
		return err
	})
}
func (a *nativeOSTrustAdapter) withSnapshot(der []byte, fn func(string) error) error {
	dir, err := os.MkdirTemp("", "aiprouter-native-trust-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err = (NativeProtector{}).Protect(dir, true); err != nil {
		return err
	}
	path := filepath.Join(dir, "certificate.pem")
	if der != nil {
		if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			return err
		}
	}
	return fn(path)
}
func (a *nativeOSTrustAdapter) windows(ctx context.Context, ca *NativeCA, operation string) (NativeTrustStatus, error) {
	input, _ := json.Marshal(map[string]string{"operation": operation, "certificate": base64.StdEncoding.EncodeToString(ca.Certificate.Raw), "fingerprint": ca.Fingerprint})
	out, err := a.command(ctx, []string{"-NoProfile", "-NonInteractive", "-Command", nativeWindowsTrustScript}, input, operation != "inspect")
	if err != nil {
		return NativeTrustStatus{}, err
	}
	switch strings.TrimSpace(string(out)) {
	case "trusted":
		return NativeTrustStatus{Present: true, Trusted: true}, nil
	case "absent":
		return NativeTrustStatus{}, nil
	default:
		return NativeTrustStatus{}, errors.New("native current-user trust status is unknown")
	}
}

// Only public certificate data crosses stdin. Exact DER matching prevents deleting
// another certificate, including one that shares a subject or SHA-1 thumbprint.
const nativeWindowsTrustScript = `$ErrorActionPreference='Stop'
$inputData=[Console]::In.ReadToEnd()|ConvertFrom-Json
$bytes=[Convert]::FromBase64String($inputData.certificate)
$cert=[Security.Cryptography.X509Certificates.X509Certificate2]::new($bytes)
$sha=[Security.Cryptography.SHA256]::Create()
try {$fingerprint=([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace('-','').ToLowerInvariant()}finally{$sha.Dispose()}
if($fingerprint -cne $inputData.fingerprint){throw 'Certificate changed'}
$store=[Security.Cryptography.X509Certificates.X509Store]::new('Root','CurrentUser')
$mode=if($inputData.operation -eq 'inspect'){[Security.Cryptography.X509Certificates.OpenFlags]::ReadOnly}else{[Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite}
try {
 $store.Open($mode)
 $found=@($store.Certificates.Find([Security.Cryptography.X509Certificates.X509FindType]::FindByThumbprint,$cert.Thumbprint,$false))
 foreach($match in $found){if([Convert]::ToBase64String($match.RawData) -cne $inputData.certificate){throw 'Certificate mismatch'}}
 if($inputData.operation -eq 'install'){if($found.Count -eq 0){$store.Add($cert)}}
 elseif($inputData.operation -eq 'remove'){foreach($match in $found){$store.Remove($match)}}
 elseif($inputData.operation -ne 'inspect'){throw 'Unknown operation'}
 $present=@($store.Certificates.Find([Security.Cryptography.X509Certificates.X509FindType]::FindByThumbprint,$cert.Thumbprint,$false)).Count -gt 0
 if($present){[Console]::Out.Write('trusted')}else{[Console]::Out.Write('absent')}
}finally{$store.Close();$store.Dispose();$cert.Dispose()}`
