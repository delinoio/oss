// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type nativeTestTrust struct {
	status          NativeTrustStatus
	install, remove int
	failure         bool
	before          func()
}

func (f *nativeTestTrust) Status(context.Context, *NativeCA) (NativeTrustStatus, error) {
	return f.status, nil
}
func (f *nativeTestTrust) Install(context.Context, *NativeCA) error {
	f.install++
	if f.before != nil {
		f.before()
	}
	f.status = NativeTrustStatus{Trusted: true, Present: true}
	if f.failure {
		return errors.New("crashed after OS mutation")
	}
	return nil
}
func (f *nativeTestTrust) Remove(context.Context, *NativeCA) error {
	f.remove++
	if f.before != nil {
		f.before()
	}
	f.status = NativeTrustStatus{}
	if f.failure {
		return errors.New("crashed after OS mutation")
	}
	return nil
}
func TestNativeTrustPendingRecovery(t *testing.T) {
	ca, _, _ := nativeTestCA(t)
	root := realTemp(t)
	fake := &nativeTestTrust{failure: true}
	m := NativeTrustManager{Root: root, Adapter: fake}
	ctx := context.Background()
	fake.before = func() {
		b, e := os.ReadFile(filepath.Join(root, "trust.json"))
		if e != nil || !bytes.Contains(b, []byte("pending")) {
			t.Fatal("OS operation before durable pending record")
		}
	}
	if e := m.Install(ctx, ca); e == nil {
		t.Fatal("reported success after mutation failure")
	}
	status, e := m.Status(ctx, ca)
	if e != nil || !status.Pending || !status.Owned || status.Trusted {
		t.Fatalf("pending activated: %+v %v", status, e)
	}
	fake.failure = false
	if e = m.Install(ctx, ca); e != nil {
		t.Fatal(e)
	}
	if fake.install != 1 {
		t.Fatal("recovery duplicated completed OS install")
	}
	status, e = m.Status(ctx, ca)
	if e != nil || !status.Trusted || status.Pending {
		t.Fatalf("recovery not committed: %+v %v", status, e)
	}
	fake.failure = true
	if e = m.Remove(ctx, ca); e == nil {
		t.Fatal("reported cleanup after failure")
	}
	fake.failure = false
	if e = m.Remove(ctx, ca); e != nil {
		t.Fatal(e)
	}
	if fake.remove != 1 {
		t.Fatal("recovery duplicated completed removal")
	}
	status, e = m.Status(ctx, ca)
	if e != nil || status.Owned || status.Pending || status.Trusted {
		t.Fatalf("cleanup not committed: %+v %v", status, e)
	}
}
func TestNativeTrustPreservesUnownedAndChangedCertificates(t *testing.T) {
	ca, _, _ := nativeTestCA(t)
	other, _, _ := nativeTestCA(t)
	fake := &nativeTestTrust{status: NativeTrustStatus{Present: true, Trusted: true}}
	m := NativeTrustManager{Root: realTemp(t), Adapter: fake}
	ctx := context.Background()
	if e := m.Install(ctx, ca); e == nil {
		t.Fatal("adopted unowned trust")
	}
	if e := m.Remove(ctx, ca); e == nil {
		t.Fatal("removed unowned trust")
	}
	if fake.install+fake.remove != 0 {
		t.Fatal("unowned OS mutation")
	}
	fake.status = NativeTrustStatus{}
	if e := m.Install(ctx, ca); e != nil {
		t.Fatal(e)
	}
	if e := m.Remove(ctx, other); e == nil {
		t.Fatal("changed CA accepted")
	}
	if fake.remove != 0 {
		t.Fatal("changed CA removed")
	}
}
func TestNativeTrustWindowsCommandsAreCurrentUserAndExactCertificate(t *testing.T) {
	ca, _, _ := nativeTestCA(t)
	present := false
	calls := 0
	run := func(ctx context.Context, name string, args []string, input []byte) ([]byte, error) {
		calls++
		if !strings.HasSuffix(name, "powershell.exe") || len(args) != 4 || !strings.Contains(args[3], "'Root','CurrentUser'") || strings.Contains(args[3], "LocalMachine") {
			t.Fatal("unsafe Windows invocation")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("command deadline missing")
		}
		var request map[string]string
		if e := json.Unmarshal(input, &request); e != nil {
			t.Fatal(e)
		}
		if request["fingerprint"] != ca.Fingerprint || !strings.Contains(args[3], "$match.RawData") {
			t.Fatal("exact identity missing")
		}
		if request["operation"] == "install" {
			present = true
		}
		if request["operation"] == "remove" {
			present = false
		}
		if present {
			return []byte("trusted"), nil
		}
		return []byte("absent"), nil
	}
	adapter, e := NewNativeTrustAdapter("windows", realTemp(t), run)
	if e != nil {
		t.Fatal(e)
	}
	m := NativeTrustManager{Root: realTemp(t), Adapter: adapter}
	if e = m.Install(context.Background(), ca); e != nil {
		t.Fatal(e)
	}
	if e = m.Remove(context.Background(), ca); e != nil {
		t.Fatal(e)
	}
	if calls < 6 {
		t.Fatal("missing readback")
	}
}
func TestNativeTrustMacCommandsUseSnapshotsAndUserKeychain(t *testing.T) {
	ca, _, _ := nativeTestCA(t)
	home := realTemp(t)
	present := false
	run := func(_ context.Context, name string, args []string, _ []byte) ([]byte, error) {
		if name != "/usr/bin/security" {
			t.Fatal("PATH executable")
		}
		for _, arg := range args {
			if arg == "-d" || strings.Contains(arg, "System.keychain") {
				t.Fatal("system trust touched")
			}
		}
		switch args[0] {
		case "find-certificate":
			if args[len(args)-1] != filepath.Join(home, "Library", "Keychains", "login.keychain-db") {
				t.Fatal("wrong keychain")
			}
			if present {
				return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Certificate.Raw}), nil
			}
			return nil, nil
		case "add-trusted-cert", "remove-trusted-cert":
			path := args[len(args)-1]
			if path == ca.CertificatePath {
				t.Fatal("mutable source passed to security")
			}
			data, e := os.ReadFile(path)
			block, _ := pem.Decode(data)
			if e != nil || block == nil || !bytes.Equal(block.Bytes, ca.Certificate.Raw) {
				t.Fatal("snapshot differs")
			}
			if args[0] == "add-trusted-cert" {
				present = true
			}
			return nil, nil
		case "verify-cert":
			return nil, nil
		case "trust-settings-export":
			sum := sha1.Sum(ca.Certificate.Raw)
			return nil, os.WriteFile(args[1], []byte("<key>"+strings.ToUpper(hex.EncodeToString(sum[:]))+"</key><dict><array></array></dict>"), 0600)
		case "delete-certificate":
			sum := sha1.Sum(ca.Certificate.Raw)
			if args[2] != strings.ToUpper(hex.EncodeToString(sum[:])) {
				t.Fatal("broad deletion")
			}
			present = false
			return nil, nil
		default:
			t.Fatalf("unexpected command %v", args)
			return nil, nil
		}
	}
	adapter, e := NewNativeTrustAdapter("darwin", home, run)
	if e != nil {
		t.Fatal(e)
	}
	m := NativeTrustManager{Root: realTemp(t), Adapter: adapter}
	ctx := context.Background()
	if e = m.Install(ctx, ca); e != nil {
		t.Fatal(e)
	}
	if e = m.Remove(ctx, ca); e != nil {
		t.Fatal(e)
	}
}
func TestNativeTrustRefusesUnknownAdapterResponses(t *testing.T) {
	ca, _, _ := nativeTestCA(t)
	run := func(context.Context, string, []string, []byte) ([]byte, error) { return []byte("unknown"), nil }
	adapter, e := NewNativeTrustAdapter("windows", realTemp(t), run)
	if e != nil {
		t.Fatal(e)
	}
	m := NativeTrustManager{Root: realTemp(t), Adapter: adapter}
	if e = m.Install(context.Background(), ca); e == nil {
		t.Fatal("unknown trust accepted")
	}
	if _, e = NewNativeTrustAdapter("linux", realTemp(t), run); e == nil {
		t.Fatal("unsupported platform accepted")
	}
	if _, e = NewNativeTrustAdapter("darwin", realTemp(t), nil); e == nil {
		t.Fatal("implicit real command runner accepted")
	}
}
