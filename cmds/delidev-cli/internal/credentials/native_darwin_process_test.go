//go:build darwin

// SPDX-License-Identifier: Apache-2.0
package credentials

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/ebitengine/purego"
)

// A linker-set fixture value creates two different executables, not two copies
// of one signature. Only temporary keychains and synthetic material are used.
var fixtureBuild = "first"

func TestMacCredentialProcessHelper(t *testing.T) {
	if os.Getenv("DELIDEV_CREDENTIAL_PROCESS_FIXTURE") != "1" {
		t.Skip("owned subprocess fixture only")
	}
	a, err := loadMac()
	if err != nil {
		t.Fatal(err)
	}
	var open func(string, *uintptr) int32
	purego.RegisterLibFunc(&open, a.security, "SecKeychainOpen")
	var keychain uintptr
	if status := open(os.Getenv("DELIDEV_FIXTURE_KEYCHAIN"), &keychain); status != 0 {
		t.Fatalf("temporary keychain open: status %d", status)
	}
	defer a.release(keychain)
	s := &macStore{api: a, keychain: keychain}
	ctx := context.Background()
	name := os.Getenv("DELIDEV_FIXTURE_REFERENCE")
	material := bytes.Repeat([]byte{0x37}, nativeMaterialSize)
	defer clear(material)
	switch os.Getenv("DELIDEV_FIXTURE_ACTION") {
	case "put", "replace":
		if err := s.create(ctx, name, material); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("DELIDEV_FIXTURE_ACTION") == "replace" {
			fmt.Println("fixture-ready")
			if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			_, err := s.get(ctx, name)
			wantCode(t, err, domain.RecoveryRequired)
			if domain.SafeError(err).Cause != "credential_executable_changed" {
				t.Fatal("changed executable was not classified independently of keychain lock")
			}
		}
	case "get":
		value, err := s.get(ctx, name)
		if err != nil || !bytes.Equal(value, material) {
			t.Fatalf("signed fixture read: %v", err)
		}
		clear(value)
	default:
		t.Fatal("invalid fixture action")
	}
	// Keep the linker-set value reachable without exposing native content.
	if fixtureBuild != "first" && fixtureBuild != "second" {
		t.Fatal("invalid compiled fixture variant")
	}
}

func TestMacCodeIdentityAcrossReplacementAndSignedBuilds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	a, err := loadMac()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "fixture.keychain-db")
	passwordBytes := make([]byte, 32)
	if _, err := rand.Read(passwordBytes); err != nil {
		t.Fatal(err)
	}
	password := []byte(hex.EncodeToString(passwordBytes))
	defer clear(passwordBytes)
	defer clear(password)
	var create func(string, uint32, *byte, uint8, uintptr, *uintptr) int32
	var remove func(uintptr) int32
	purego.RegisterLibFunc(&create, a.security, "SecKeychainCreate")
	purego.RegisterLibFunc(&remove, a.security, "SecKeychainDelete")
	var keychain uintptr
	if status := create(path, uint32(len(password)), &password[0], 0, 0, &keychain); status != 0 {
		t.Fatalf("temporary keychain creation: status %d", status)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal("temporary keychain file missing")
	}
	defer func() {
		if status := remove(keychain); status != 0 {
			t.Errorf("temporary keychain cleanup: status %d", status)
		}
		a.release(keychain)
	}()
	run := func(command string, args ...string) []byte {
		t.Helper()
		out, err := exec.CommandContext(ctx, command, args...).CombinedOutput()
		if err != nil {
			// Do not publish native output, temporary paths or key content.
			classification := "unknown"
			for _, code := range []string{"CSSMERR_TP_NOT_TRUSTED", "errSecInternalComponent", "no identity found", "unable to build chain", "item could not be found"} {
				if bytes.Contains(out, []byte(code)) {
					classification = code
					break
				}
			}
			t.Fatalf("fixture command %s failed: %v, classification %s", filepath.Base(command), err, classification)
		}
		return out
	}
	original, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(root, "first")
	copyExecutable := func(source, target string) {
		t.Helper()
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	copyExecutable(original, first)
	run("/usr/bin/codesign", "--force", "--sign", "-", "--identifier", "io.delino.delidev.fixture.first", first)
	name := string(domain.NewID())
	helper := func(binary, action string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestMacCredentialProcessHelper$", "-test.count=1")
		cmd.Env = []string{"DELIDEV_CREDENTIAL_PROCESS_FIXTURE=1", "DELIDEV_FIXTURE_KEYCHAIN=" + path, "DELIDEV_FIXTURE_REFERENCE=" + name, "DELIDEV_FIXTURE_ACTION=" + action}
		return cmd
	}
	child := helper(first, "replace")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child.ProcessState == nil {
			child.Process.Kill()
			child.Wait()
		}
	})
	reader := bufio.NewReader(output)
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "fixture-ready" {
		t.Fatalf("fixture readiness: %v", err)
	}
	replacement := filepath.Join(root, "replacement")
	copyExecutable(original, replacement)
	run("/usr/bin/codesign", "--force", "--sign", "-", "--identifier", "io.delino.delidev.fixture.changed", replacement)
	if err := os.Rename(replacement, first); err != nil {
		t.Fatal(err)
	}
	io.WriteString(input, "inspect\n")
	input.Close()
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("replacement fixture failed: %v, outcome %q", err, remaining)
	}

	// This certificate/private key is imported only into the explicitly named
	// disposable keychain. No default keychain, trust setting or search list changes.
	config := filepath.Join(root, "openssl.conf")
	if err := os.WriteFile(config, []byte("[req]\nprompt=no\ndistinguished_name=dn\nx509_extensions=ext\n[dn]\nCN=DeliDev Local Development\n[ext]\nbasicConstraints=critical,CA:TRUE\nkeyUsage=critical,digitalSignature,keyCertSign,cRLSign\nextendedKeyUsage=codeSigning\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, certificate := filepath.Join(root, "fixture.key"), filepath.Join(root, "fixture.pem")
	run("/usr/bin/openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-config", config, "-keyout", key, "-out", certificate)
	pkcs12 := filepath.Join(root, "fixture.p12")
	// This fixed password protects only synthetic temporary test material.
	run("/usr/bin/openssl", "pkcs12", "-export", "-inkey", key, "-in", certificate, "-out", pkcs12, "-passout", "pass:delidev-fixture-password")
	run("/usr/bin/security", "import", pkcs12, "-k", path, "-P", "delidev-fixture-password", "-T", original)
	certificatePEM, err := os.ReadFile(certificate)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certificatePEM)
	if block == nil {
		t.Fatal("missing fixture certificate")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha1.Sum(parsed.Raw)
	second := filepath.Join(root, "second")
	run("go", "test", "-c", "-o", second, "-ldflags", "-X github.com/delinoio/oss/cmds/delidev-cli/internal/credentials.fixtureBuild=second", ".")
	copyExecutable(original, first)
	for _, binary := range []string{first, second} {
		signFixture(t, a, keychain, binary)
		requirement := fmt.Sprintf(`=identifier "io.delino.delidev.development.server" and certificate leaf = H"%X"`, fingerprint)
		run("/usr/bin/codesign", "--verify", "--strict", "-R", requirement, binary)
	}
	name = string(domain.NewID())
	for i, binary := range []string{first, second} {
		action := "put"
		if i == 1 {
			action = "get"
		}
		if out, err := helper(binary, action).CombinedOutput(); err != nil {
			t.Fatalf("signed build fixture failed: %v, outcome %q", err, out)
		}
	}
	// The fixture creator removes its complete temporary keychain. It does not
	// assume authorization to delete another executable's individual key ACLs.
	t.Log("Changed executable classification and original key access from two certificate-signed builds passed using a temporary keychain; no provider/account acceptance implied")
}

// The signing API takes the fixture identity directly. The codesign CLI's
// discovery requires a keychain on the user's search list; this test must not
// change that list or introduce trust for a temporary certificate.
func signFixture(t *testing.T, a *macAPI, keychain uintptr, binary string) {
	t.Helper()
	var search func(uintptr, uint32, *uintptr) int32
	var next func(uintptr, *uintptr) int32
	var signerCreate func(uintptr, uint32, *uintptr) int32
	var signerAdd func(uintptr, uintptr, uint32) int32
	var staticCreate func(uintptr, uint32, *uintptr) int32
	for _, item := range []struct {
		p    any
		name string
	}{{&search, "SecIdentitySearchCreate"}, {&next, "SecIdentitySearchCopyNext"}, {&signerCreate, "SecCodeSignerCreate"}, {&signerAdd, "SecCodeSignerAddSignature"}, {&staticCreate, "SecStaticCodeCreateWithPath"}} {
		purego.RegisterLibFunc(item.p, a.security, item.name)
	}
	var cursor, identity uintptr
	if status := search(keychain, 0, &cursor); status != 0 {
		t.Fatalf("fixture identity search: status %d", status)
	}
	defer a.release(cursor)
	if status := next(cursor, &identity); status != 0 {
		t.Fatalf("fixture identity: status %d", status)
	}
	defer a.release(identity)
	parameters := a.dictionary(0, 0, a.keyCallbacks, a.valueCallbacks)
	defer a.release(parameters)
	identifier := a.stringValue(0, "io.delino.delidev.development.server", 0x08000100)
	defer a.release(identifier)
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(cf)
	var numberCreate func(uintptr, int64, *uint32) uintptr
	purego.RegisterLibFunc(&numberCreate, cf, "CFNumberCreate")
	flags := uint32(0x10000)             // kSecCodeSignatureRuntime, matching the development signer.
	number := numberCreate(0, 3, &flags) // kCFNumberSInt32Type
	defer a.release(number)
	for name, value := range map[string]uintptr{"kSecCodeSignerIdentity": identity, "kSecCodeSignerIdentifier": identifier, "kSecCodeSignerFlags": number} {
		address, err := purego.Dlsym(a.security, name)
		if err != nil {
			t.Fatal(err)
		}
		a.set(parameters, dereferenceCFGlobal(address), value)
	}
	var signer uintptr
	if status := signerCreate(parameters, 0, &signer); status != 0 {
		t.Fatalf("fixture signer: status %d", status)
	}
	defer a.release(signer)
	var urlCreate func(uintptr, *byte, int64, uint8) uintptr
	purego.RegisterLibFunc(&urlCreate, cf, "CFURLCreateFromFileSystemRepresentation")
	pathBytes := []byte(binary)
	url := urlCreate(0, &pathBytes[0], int64(len(pathBytes)), 0)
	defer a.release(url)
	var code uintptr
	if status := staticCreate(url, 0, &code); status != 0 {
		t.Fatalf("fixture static code: status %d", status)
	}
	defer a.release(code)
	if status := signerAdd(signer, code, 0); status != 0 {
		t.Fatalf("fixture signature: status %d", status)
	}
}
