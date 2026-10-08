// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type tlsCaptureServiceFixture struct {
	serviceFixture
	capturedMu sync.Mutex
	captured   userservice.Spec
}

func (f *tlsCaptureServiceFixture) Install(ctx context.Context, spec userservice.Spec) error {
	if err := f.serviceFixture.Install(ctx, spec); err != nil {
		return err
	}
	f.capturedMu.Lock()
	f.captured = spec
	f.capturedMu.Unlock()
	return nil
}

func TestRPCServiceInstallCapturesRunningServersOriginalTLSDirectory(t *testing.T) {
	original, _ := filepath.EvalSymlinks(t.TempDir())
	later := t.TempDir()
	fixture := httptest.NewTLSServer(nil)
	certificate := fixture.TLS.Certificates[0]
	leaf := fixture.Certificate()
	fixture.Close()
	key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string][]byte{"cert.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), "key.pem": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})} {
		if err := os.WriteFile(filepath.Join(original, name), contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(original)
	backend := &tlsCaptureServiceFixture{}
	root := filepath.Join(t.TempDir(), "server")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan Endpoint, 1)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{disableKnownModelMaintenance: true, DataDir: root, Listen: "127.0.0.1:0", TLSCertificate: "cert.pem", TLSKey: "key.pem", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), userServiceBackend: backend}, func(endpoint Endpoint) { ready <- endpoint })
	}()
	var endpoint Endpoint
	select {
	case endpoint = <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("readiness timed out")
	}
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	// Installation happens after the server's original startup cwd is gone.
	t.Chdir(later)
	identity, err := security.LoadIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	defer transport.CloseIdleConnections()
	client := delidevv1connect.NewSystemServiceClient(&http.Client{Transport: transport}, endpoint.URL)
	request := &pb.ControlUserServiceRequest{Kind: pb.UserServiceKind_USER_SERVICE_KIND_SERVER, Action: pb.UserServiceAction_USER_SERVICE_ACTION_INSTALL, RequestId: string(domain.NewID())}
	if _, err := client.ControlUserService(ctx, ownerRequest(identity, request)); err != nil {
		t.Fatal(err)
	}
	replay, err := client.ControlUserService(ctx, ownerRequest(identity, request))
	if err != nil || !replay.Msg.Replayed {
		t.Fatal("original receipt lost", err)
	}
	backend.capturedMu.Lock()
	spec := backend.captured
	backend.capturedMu.Unlock()
	if spec.Options.TLSCertificate != filepath.Join(original, "cert.pem") || spec.Options.TLSKey != filepath.Join(original, "key.pem") {
		t.Fatal("RPC installer recaptured later cwd")
	}
	pair, err := tls.LoadX509KeyPair(spec.Options.TLSCertificate, spec.Options.TLSKey)
	if err != nil || !bytes.Equal(pair.Certificate[0], certificate.Certificate[0]) {
		t.Fatal("RPC service selected another TLS pair", err)
	}
}
