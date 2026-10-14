// SPDX-License-Identifier: Apache-2.0
package userservice

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func fixtureTLSReferences(t *testing.T, dir string) []byte {
	t.Helper()
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	certificate := server.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"cert.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), "key.pem": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return certificate.Certificate[0]
}

func TestServerServiceTLSReferencesRetainOriginalInstallDirectory(t *testing.T) {
	m, _ := fixture(t)
	m.Kind = Server
	first, _ := filepath.EvalSymlinks(t.TempDir())
	second := t.TempDir()
	expected := fixtureTLSReferences(t, first)
	t.Chdir(first)
	m.Options = ServerOptions{Listen: "127.0.0.1:0", TLSCertificate: "cert.pem", TLSKey: "key.pem"}
	installed := control(t, m, Install, 0)
	r, err := m.load()
	if err != nil {
		t.Fatal(err)
	}
	if r.Spec.Options.TLSCertificate != filepath.Join(first, "cert.pem") || r.Spec.Options.TLSKey != filepath.Join(first, "key.pem") {
		t.Fatal("installed references remain relative")
	}
	// Record a controlled explicit Start without invoking a native service manager.
	requestID := domain.NewID()
	r.Revision++
	r.Desired = Running
	r.Receipts = append(r.Receipts, Receipt{ID: requestID, Digest: strings.Repeat("ab", 32), Done: true, Settled: true})
	r.Events = append(r.Events, Event{Revision: r.Revision, RequestID: requestID, Action: Start, Desired: Running})
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	t.Chdir(second)
	called := false
	err = m.Run(context.Background(), installed.Status.ID, func(_ context.Context, spec Spec, running bool) error {
		called = true
		if !running {
			t.Fatal("original Start intent lost")
		}
		pair, err := tls.LoadX509KeyPair(spec.Options.TLSCertificate, spec.Options.TLSKey)
		if err != nil {
			return err
		}
		if !bytes.Equal(pair.Certificate[0], expected) {
			t.Fatal("different TLS pair selected")
		}
		return nil
	})
	if err != nil || !called {
		t.Fatal("original TLS refs unavailable from native cwd", err)
	}
}

func TestServerServiceTLSAbsoluteAndEmptyReferencesRemainUnchanged(t *testing.T) {
	for _, options := range []ServerOptions{{}, {Listen: "127.0.0.1:0", TLSCertificate: filepath.Join(t.TempDir(), "cert.pem"), TLSKey: filepath.Join(t.TempDir(), "key.pem")}} {
		result, err := CaptureServerOptions(options)
		if err != nil || result.TLSCertificate != options.TLSCertificate || result.TLSKey != options.TLSKey {
			t.Fatal("absolute/no-TLS refs changed", err)
		}
	}
}

func TestCaptureServerOptionsAnchorsRelativeTLSToPhysicalCwdThroughSymlink(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	releaseA, releaseB := filepath.Join(root, "releaseA"), filepath.Join(root, "releaseB")
	for _, dir := range []string{releaseA, releaseB} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(root, "current")
	if err := os.Symlink(releaseA, current); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// t.Chdir also sets PWD to the symlink spelling, as a shell would.
	t.Chdir(current)
	result, err := CaptureServerOptions(ServerOptions{Listen: "127.0.0.1:0", TLSCertificate: "cert.pem", TLSKey: filepath.Join("sub", "key.pem")})
	if err != nil {
		t.Fatal(err)
	}
	// Retarget the symlink after capture; stored references must not follow it.
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(releaseB, current); err != nil {
		t.Fatal(err)
	}
	if result.TLSCertificate != filepath.Join(releaseA, "cert.pem") || result.TLSKey != filepath.Join(releaseA, "sub", "key.pem") {
		t.Fatalf("references not anchored to physical cwd: %q %q", result.TLSCertificate, result.TLSKey)
	}
}

func TestCaptureServerOptionsRejectsSymlinkRotationDuringDirectoryCapture(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	releaseA, releaseB := filepath.Join(root, "releaseA"), filepath.Join(root, "releaseB")
	for _, dir := range []string{releaseA, releaseB} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(root, "current")
	if err := os.Symlink(releaseA, current); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Chdir(current)
	result, err := captureServerOptions(ServerOptions{TLSCertificate: "cert.pem", TLSKey: "key.pem"}, func() (workingDirectory, error) {
		return physicalWorkingDirectoryWith(os.Stat, func() (string, error) { return current, nil }, func(path string) (string, error) {
			// The process stays in releaseA while the retained spelling changes.
			if err := os.Remove(current); err != nil {
				return "", err
			}
			if err := os.Symlink(releaseB, current); err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(path)
		})
	})
	if domain.SafeError(err).Code != domain.InvalidArgument || result.TLSCertificate != "" || result.TLSKey != "" {
		t.Fatalf("rotated directory published TLS references: %+v, %v", result, err)
	}
}

func TestCaptureServerOptionsRejectsResolvedDirectoryRemovedBeforeIdentityCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot rename the process working directory")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(root, "release")
	if err := os.Mkdir(release, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(release)
	result, err := captureServerOptions(ServerOptions{TLSCertificate: "cert.pem", TLSKey: "key.pem"}, func() (workingDirectory, error) {
		return physicalWorkingDirectoryWith(os.Stat, os.Getwd, func(path string) (string, error) {
			physical, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", err
			}
			if err := os.Rename(release, filepath.Join(root, "moved")); err != nil {
				return "", err
			}
			return physical, nil
		})
	})
	if domain.SafeError(err).Code != domain.InvalidArgument || result.TLSCertificate != "" || result.TLSKey != "" {
		t.Fatalf("unresolved directory published TLS references: %+v, %v", result, err)
	}
}

func TestAnchorReferenceUsesOriginalWindowsDrive(t *testing.T) {
	cwd := workingDirectory{original: `C:\current`, physical: `D:\release`}
	var absCalls []string
	ops := referencePathOps{
		volumeName: func(path string) string {
			if len(path) >= 2 && path[1] == ':' {
				return path[:2]
			}
			return ""
		},
		isPathSeparator: func(c uint8) bool { return c == '\\' || c == '/' },
		join: func(parts ...string) string {
			return strings.TrimRight(parts[0], `\`) + `\` + strings.TrimLeft(parts[1], `\`)
		},
		clean: func(path string) string { return path },
		abs: func(path string) (string, error) {
			absCalls = append(absCalls, path)
			if path != `D:cert.pem` {
				t.Fatalf("unexpected OS drive resolution: %q", path)
			}
			return `D:\other\cert.pem`, nil
		},
	}
	for _, test := range []struct{ reference, expected string }{
		{`\cert.pem`, `C:\cert.pem`},
		{`C:cert.pem`, `D:\release\cert.pem`},
		{`c:cert.pem`, `D:\release\cert.pem`},
		{`D:cert.pem`, `D:\other\cert.pem`},
		{`cert.pem`, `D:\release\cert.pem`},
	} {
		for _, name := range []string{"certificate", "key"} {
			got, err := anchorReferenceWith(cwd, test.reference, ops)
			if err != nil || got != test.expected {
				t.Fatalf("%s reference %q captured as %q, want %q: %v", name, test.reference, got, test.expected, err)
			}
		}
	}
	if len(absCalls) != 2 {
		t.Fatalf("other-drive references called OS resolver %d times, want 2", len(absCalls))
	}
}

func TestCaptureServerOptionsPreservesWindowsRootedAndDriveRelativeSemantics(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("rooted and drive-relative references exist only on Windows")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	volume := filepath.VolumeName(root)
	lowerVolume := strings.ToLower(volume)
	otherVolume := "Z:"
	if strings.EqualFold(volume, otherVolume) {
		otherVolume = "Y:"
	}
	otherExpected, err := filepath.Abs(otherVolume + "cert.pem")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, reference, expected string }{
		{"rooted on the cwd drive", `\cert.pem`, volume + `\cert.pem`},
		{"rooted subdirectory", `\sub\key.pem`, volume + `\sub\key.pem`},
		{"drive-relative on the cwd drive", volume + "cert.pem", filepath.Join(root, "cert.pem")},
		{"drive-relative with different volume case", lowerVolume + "cert.pem", filepath.Join(root, "cert.pem")},
		{"drive-relative on another drive", otherVolume + "cert.pem", otherExpected},
		{"plain relative", "cert.pem", filepath.Join(root, "cert.pem")},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := CaptureServerOptions(ServerOptions{TLSCertificate: test.reference, TLSKey: test.reference})
			if err != nil {
				t.Fatal(err)
			}
			if result.TLSCertificate != test.expected || result.TLSKey != test.expected || !filepath.IsAbs(result.TLSCertificate) {
				t.Fatalf("reference %q captured as %q, expected %q", test.reference, result.TLSCertificate, test.expected)
			}
		})
	}
}

func TestCaptureServerOptionsFailsWhenOriginalCwdIsUnresolvable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a process cannot remove its own working directory on Windows")
	}
	gone, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gone = filepath.Join(gone, "gone")
	if err := os.Mkdir(gone, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	_, err = CaptureServerOptions(ServerOptions{TLSCertificate: "cert.pem", TLSKey: "key.pem"})
	if err == nil || domain.SafeError(err).Code != domain.InvalidArgument {
		t.Fatalf("expected InvalidArgument for unresolvable cwd, got %v", err)
	}
	// Absolute references and no-TLS configuration never need the cwd.
	if _, err := CaptureServerOptions(ServerOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestHistoricalRelativeTLSRefusesStartAndRuntimeWithoutChangingReceipt(t *testing.T) {
	m, backend := fixture(t)
	m.Kind = Server
	m.Options = ServerOptions{TLSCertificate: filepath.Join(t.TempDir(), "cert.pem"), TLSKey: filepath.Join(t.TempDir(), "key.pem")}
	installed := control(t, m, Install, 0)
	r, err := m.load()
	if err != nil {
		t.Fatal(err)
	}
	r.Spec.Options.TLSCertificate, r.Spec.Options.TLSKey = "cert.pem", "key.pem"
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(m.path(".json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Control(context.Background(), Start, domain.NewID(), r.Revision, "fixture-user")
	if domain.SafeError(err).Code != domain.Unsupported || !strings.Contains(domain.SafeError(err).Guidance, "reinstall") || backend.writes[Start] != 0 {
		t.Fatal("historical references reinterpreted", err)
	}
	after, err := os.ReadFile(m.path(".json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("historical receipt or spec rewritten", err)
	}
	// Exercise the historical native launch with its original explicit Start intent.
	r.Desired = Running
	if err := m.save(r); err != nil {
		t.Fatal(err)
	}
	before, err = os.ReadFile(m.path(".json"))
	if err != nil {
		t.Fatal(err)
	}
	err = m.Run(context.Background(), installed.Status.ID, func(context.Context, Spec, bool) error { t.Fatal("historical runtime callback invoked"); return nil })
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("historical runtime admitted", err)
	}
	after, err = os.ReadFile(m.path(".json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("historical receipt or spec rewritten", err)
	}
	if _, err := m.Status(context.Background()); err != nil {
		t.Fatal("historical inspection denied", err)
	}
	stopped := control(t, m, Stop, r.Revision)
	control(t, m, Remove, stopped.Status.Revision)
}
