package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Only the test binary's explicit server re-exec path enters the CLI. Tests
// never start an installed harness or load a user data directory.
func init() {
	if len(os.Args) >= 5 && os.Args[1] == "--data-dir" && os.Args[3] == "server" && os.Args[4] == "run" {
		os.Exit(Run(context.Background(), os.Args[1:], IO{}))
	}
}

func startupCertificate(t *testing.T, expired bool) server.Config {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if expired {
		until = time.Now().Add(-time.Hour)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"delidev.example.test"}, NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	config := server.Config{DataDir: filepath.Join(root, "server"), Listen: "127.0.0.1:0", TLSCertificate: filepath.Join(root, "tls.pem"), TLSKey: filepath.Join(root, "key.pem")}
	if err := os.WriteFile(config.TLSCertificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.TLSKey, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestDetachedStartupReadiness(t *testing.T) {
	for _, tc := range []struct {
		listen string
		tls    bool
	}{
		{"127.0.0.1:0", false},
		{"127.0.0.1:0", true},
		{"0.0.0.0:0", true},
		{"[::]:0", true},
	} {
		t.Run(tc.listen+map[bool]string{true: "/tls", false: "/http"}[tc.tls], func(t *testing.T) {
			if tc.listen == "[::]:0" {
				listener, err := net.Listen("tcp6", tc.listen)
				if err != nil {
					t.Skip("IPv6 wildcard listener unavailable")
				}
				listener.Close()
			}
			config := startupCertificate(t, false)
			config.Listen = tc.listen
			if !tc.tls {
				config.TLSCertificate, config.TLSKey = "", ""
			}
			tlsConfig, err := startupTLS(config)
			if err != nil {
				t.Fatal(err)
			}
			o := options{dataDir: config.DataDir}
			defer func() {
				c, err := startupClient(o, strings.NewReader(""), tlsConfig)
				if err != nil {
					t.Error("cleanup client", err)
					return
				}
				defer c.transport.CloseIdleConnections()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, err = c.system.StopServer(ctx, request(c, &pb.StopServerRequest{RequestId: string(domain.NewID())}))
				if err != nil {
					t.Error("stop detached fixture", err)
				}
				for {
					if _, err := os.Stat(filepath.Join(config.DataDir, "server.json")); os.IsNotExist(err) {
						if lock, err := security.TryLock(filepath.Join(config.DataDir, "server.lock")); err == nil {
							lock.Close()
							return
						}
					}
					select {
					case <-ctx.Done():
						t.Error("detached fixture did not shut down")
						return
					case <-time.After(10 * time.Millisecond):
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			streams := IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}
			result, err := startDetached(ctx, o, config, streams)
			if err != nil {
				t.Fatal("detached readiness failed", err)
			}
			if result.(map[string]any)["started"] != true {
				t.Fatal("server did not report first startup")
			}
			first, err := server.LoadEndpoint(config.DataDir)
			if err != nil {
				t.Fatal(err)
			}
			result, err = startDetached(ctx, o, config, streams)
			if err != nil || result.(map[string]any)["reused"] != true {
				t.Fatal("healthy TLS server was not reused", err)
			}
			second, err := server.LoadEndpoint(config.DataDir)
			if err != nil || first != second {
				t.Fatal("reuse replaced the server", err)
			}
		})
	}
}

func TestStartupCertificatePin(t *testing.T) {
	for _, mode := range []string{"matching", "different", "expired", "ordinary-client"} {
		t.Run(mode, func(t *testing.T) {
			config := startupCertificate(t, mode == "expired")
			pair, err := tls.LoadX509KeyPair(config.TLSCertificate, config.TLSKey)
			if err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			peer.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
			peer.Config.ErrorLog = slog.NewLogLogger(slog.NewTextHandler(io.Discard, nil), slog.LevelError)
			peer.StartTLS()
			defer peer.Close()
			if mode == "different" {
				config = startupCertificate(t, false)
			}
			httpClient, transport := rpc.HTTPClient()
			defer transport.CloseIdleConnections()
			if mode != "ordinary-client" {
				transport.TLSClientConfig, err = startupTLS(config)
				if err != nil {
					t.Fatal(err)
				}
			}
			httpClient.Timeout = 5 * time.Second
			response, err := httpClient.Get(peer.URL)
			if response != nil {
				response.Body.Close()
			}
			if mode == "matching" {
				if err != nil || requests.Load() != 1 {
					t.Fatal("configured DNS certificate rejected", err)
				}
			} else if err == nil || requests.Load() != 0 {
				t.Fatal("certificate validation did not stop the request")
			}
		})
	}
}
