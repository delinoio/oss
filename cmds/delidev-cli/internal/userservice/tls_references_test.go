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
