package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/protos/gen/go/devhud/v1/devhudv1connect"
	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/config"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func TestBootstrapCorrelationAndFoundationCapabilities(t *testing.T) {
	handler, repository := testHandler(t)
	testServer := httptest.NewServer(handler)
	defer testServer.Close()
	client := devhudv1connect.NewBootstrapServiceClient(http.DefaultClient, testServer.URL)
	response, err := client.GetBootstrap(context.Background(), connect.NewRequest(&devhudv1.GetBootstrapRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	headerID := response.Header().Get(correlationHeader)
	parsedID, parseErr := uuid.Parse(headerID)
	if parseErr != nil || parsedID.Version() != 7 || response.Msg.Metadata.GetCorrelationId().GetValue() != headerID {
		t.Fatalf("correlation metadata/header mismatch: header=%q message=%q error=%v", headerID, response.Msg.Metadata.GetCorrelationId().GetValue(), parseErr)
	}
	wantCapabilities := []devhudv1.StaticCapability{
		devhudv1.StaticCapability_STATIC_CAPABILITY_SETTINGS_SYNC,
		devhudv1.StaticCapability_STATIC_CAPABILITY_ACCOUNT_RECOVERY,
	}
	if len(response.Msg.Capabilities) != len(wantCapabilities) {
		t.Fatalf("capabilities = %v", response.Msg.Capabilities)
	}
	for index := range wantCapabilities {
		if response.Msg.Capabilities[index] != wantCapabilities[index] {
			t.Fatalf("capabilities = %v", response.Msg.Capabilities)
		}
	}
	if response.Msg.LogtoRedirects.GetNative() != config.NativeRedirectURI || response.Msg.UploadLimits.GetMaxObjectBytes() != 50*1024*1024 {
		t.Fatalf("unexpected bootstrap: %v", response.Msg)
	}
	if repository.requestCount() != 1 {
		t.Fatalf("persisted request logs = %d", repository.requestCount())
	}
}

func TestSettingsRequiresAuthenticationWithCorrelationDetail(t *testing.T) {
	handler, _ := testHandler(t)
	testServer := httptest.NewServer(handler)
	defer testServer.Close()
	client := devhudv1connect.NewSettingsServiceClient(http.DefaultClient, testServer.URL)
	_, err := client.GetSettings(context.Background(), connect.NewRequest(&devhudv1.GetSettingsRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want unauthenticated", connect.CodeOf(err))
	}
	var connectError *connect.Error
	if !errors.As(err, &connectError) || len(connectError.Details()) == 0 {
		t.Fatalf("missing Connect error details: %v", err)
	}
}

func TestFrameworkConnectErrorsCarryCorrelationDetail(t *testing.T) {
	handler, _ := testHandler(t)
	testServer := httptest.NewServer(handler)
	defer testServer.Close()
	for name, configure := range map[string]func(*http.Request){
		"malformed JSON":       func(*http.Request) {},
		"unsupported encoding": func(request *http.Request) { request.Header.Set("Content-Encoding", "unsupported") },
	} {
		t.Run(name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, testServer.URL+devhudv1connect.BootstrapServiceGetBootstrapProcedure, strings.NewReader("{"))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			configure(request)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			var wireError connectWireError
			if err := json.Unmarshal(body, &wireError); err != nil {
				t.Fatalf("decode Connect error: %v: %s", err, body)
			}
			headerID := response.Header.Get(correlationHeader)
			var metadata devhudv1.ErrorMetadata
			found := false
			for _, detail := range wireError.Details {
				if detail.Type != "devhud.v1.ErrorMetadata" {
					continue
				}
				encoded, err := base64.RawStdEncoding.DecodeString(detail.Value)
				if err != nil || proto.Unmarshal(encoded, &metadata) != nil {
					t.Fatalf("decode ErrorMetadata: %v", err)
				}
				found = metadata.GetCorrelationId().GetValue() == headerID
			}
			if !found {
				t.Fatalf("missing matching ErrorMetadata: header=%q body=%s", headerID, body)
			}
		})
	}
}

func TestRecoveredConnectPanicCarriesCorrelationDetail(t *testing.T) {
	handler, _ := testHandlerWithVerifier(t, panicVerifier{})
	testServer := httptest.NewServer(handler)
	defer testServer.Close()
	client := devhudv1connect.NewSettingsServiceClient(http.DefaultClient, testServer.URL)
	request := connect.NewRequest(&devhudv1.GetSettingsRequest{})
	request.Header().Set("Authorization", "Bearer panic")
	_, err := client.GetSettings(context.Background(), request)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want internal", connect.CodeOf(err))
	}
	var connectError *connect.Error
	if !errors.As(err, &connectError) {
		t.Fatalf("error = %v", err)
	}
	headerID := connectError.Meta().Get(correlationHeader)
	found := false
	for _, detail := range connectError.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		metadata, ok := value.(*devhudv1.ErrorMetadata)
		if ok && metadata.GetCorrelationId().GetValue() == headerID {
			found = true
		}
	}
	if headerID == "" || !found {
		t.Fatalf("missing matching panic correlation metadata: header=%q error=%v", headerID, err)
	}
}

func TestExactCORSPreflightContract(t *testing.T) {
	handler, _ := testHandler(t)
	for _, origin := range []string{
		"http://localhost:46305", "http://127.0.0.1:46305", "http://localhost:46306",
		"http://127.0.0.1:46306", "http://tauri.localhost",
	} {
		request := httptest.NewRequest(http.MethodOptions, devhudv1connect.SettingsServiceGetSettingsProcedure, nil)
		request.Host = "localhost:46307"
		request.RemoteAddr = "127.0.0.1:12345"
		request.Header.Set("Origin", origin)
		request.Header.Set("Access-Control-Request-Method", http.MethodPost)
		request.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Errorf("origin %q: status=%d allow-origin=%q", origin, response.Code, response.Header().Get("Access-Control-Allow-Origin"))
		}
		if strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "*") || !strings.Contains(strings.ToLower(response.Header().Get("Access-Control-Expose-Headers")), correlationHeader) {
			t.Errorf("origin %q returned invalid headers: %v", origin, response.Header())
		}
	}

	for name, mutate := range map[string]func(*http.Request){
		"origin": func(request *http.Request) { request.Header.Set("Origin", "http://localhost:46305.evil.example") },
		"header": func(request *http.Request) { request.Header.Set("Access-Control-Request-Headers", "X-Not-Allowed") },
		"method": func(request *http.Request) { request.Header.Set("Access-Control-Request-Method", http.MethodDelete) },
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, devhudv1connect.SettingsServiceGetSettingsProcedure, nil)
			request.Host = "localhost:46307"
			request.RemoteAddr = "127.0.0.1:12345"
			request.Header.Set("Origin", "http://localhost:46305")
			request.Header.Set("Access-Control-Request-Method", http.MethodPost)
			request.Header.Set("Access-Control-Request-Headers", "Content-Type")
			mutate(request)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}
}

func TestHTTPSRequiredForNonLoopback(t *testing.T) {
	handler, _ := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Host = "localhost:46307"
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHTTPSAcceptsOnlyTrustedForwarding(t *testing.T) {
	repository := &fakeRepository{}
	_, trustedNetwork, err := net.ParseCIDR("192.0.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	httpServer, err := New(Dependencies{
		Config: config.Config{
			Environment: config.EnvironmentProduction, ListenAddress: "0.0.0.0:8080", APIVersion: "test", LogtoIssuer: "https://issuer.example",
			LogtoAudience: "audience", DesktopClientID: "desktop", IOSClientID: "ios", AndroidClientID: "android",
			AdminClientID: "admin", AdminRedirectURI: "https://api.example/admin/auth/callback", PublicAssetBaseURL: "https://assets.example",
			TrustedProxyCIDRs: []*net.IPNet{trustedNetwork},
		},
		Repository: repository, Verifier: fakeVerifier{}, Clock: fixedClock{now: time.Now()}, IDs: randomIDs{},
		Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), MetricsHandler: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "192.0.2.12:12345"
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}

	for name, protocols := range map[string][]string{
		"client prepended": {"https,http"},
		"proxy prepended":  {"http,https"},
		"repeated fields":  {"https", "http"},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			request.RemoteAddr = "192.0.2.12:12345"
			for _, protocol := range protocols {
				request.Header.Add("X-Forwarded-Proto", protocol)
			}
			response := httptest.NewRecorder()
			httpServer.Handler.ServeHTTP(response, request)
			if response.Code != http.StatusUpgradeRequired {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}
}

func TestProductionLoopbackRequiresTrustedForwardedHTTPS(t *testing.T) {
	repository := &fakeRepository{}
	_, loopbackNetwork, err := net.ParseCIDR("127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	httpServer, err := New(Dependencies{
		Config: config.Config{
			Environment: config.EnvironmentProduction, ListenAddress: "127.0.0.1:8080", APIVersion: "test", LogtoIssuer: "https://issuer.example",
			LogtoAudience: "audience", DesktopClientID: "desktop", IOSClientID: "ios", AndroidClientID: "android",
			AdminClientID: "admin", AdminRedirectURI: "https://api.example/admin/auth/callback", PublicAssetBaseURL: "https://assets.example",
			TrustedProxyCIDRs: []*net.IPNet{loopbackNetwork},
		},
		Repository: repository, Verifier: fakeVerifier{}, Clock: fixedClock{now: time.Now()}, IDs: randomIDs{},
		Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), MetricsHandler: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, protocol := range map[string]string{"missing": "", "insecure": "http", "secure": "https"} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			request.RemoteAddr = "127.0.0.1:12345"
			if protocol != "" {
				request.Header.Set("X-Forwarded-Proto", protocol)
			}
			response := httptest.NewRecorder()
			httpServer.Handler.ServeHTTP(response, request)
			want := http.StatusUpgradeRequired
			if protocol == "https" {
				want = http.StatusOK
			}
			if response.Code != want {
				t.Fatalf("status = %d, want %d", response.Code, want)
			}
		})
	}
}

func testHandler(t *testing.T) (http.Handler, *fakeRepository) {
	return testHandlerWithVerifier(t, fakeVerifier{})
}

func testHandlerWithVerifier(t *testing.T, verifier auth.Verifier) (http.Handler, *fakeRepository) {
	t.Helper()
	repository := &fakeRepository{}
	httpServer, err := New(Dependencies{
		Config: config.Config{
			Environment: config.EnvironmentDevelopment, ListenAddress: "127.0.0.1:46307", APIVersion: "test", LogtoIssuer: "https://issuer.example",
			LogtoAudience: "audience", DesktopClientID: "desktop", IOSClientID: "ios", AndroidClientID: "android",
			AdminClientID: "admin", AdminRedirectURI: "https://api.example/admin/auth/callback", PublicAssetBaseURL: "https://assets.example",
		},
		Repository:     repository,
		Verifier:       verifier,
		Clock:          fixedClock{now: time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)},
		IDs:            randomIDs{},
		Logger:         slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		MetricsHandler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusOK) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return httpServer.Handler, repository
}

type fakeVerifier struct{}

func (fakeVerifier) Verify(context.Context, string) (domain.Identity, error) {
	return domain.Identity{}, errors.New("invalid token")
}

type panicVerifier struct{}

func (panicVerifier) Verify(context.Context, string) (domain.Identity, error) {
	panic("test panic")
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type randomIDs struct{}

func (randomIDs) New() (string, error) {
	id, err := uuid.NewV7()
	return id.String(), err
}

type fakeRepository struct {
	mu       sync.Mutex
	requests []domain.RequestLog
}

func (repository *fakeRepository) requestCount() int {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return len(repository.requests)
}

func (*fakeRepository) SchemaCurrent(context.Context) (bool, error) { return true, nil }
func (*fakeRepository) Ping(context.Context) error                  { return nil }
func (*fakeRepository) ProvisionUser(context.Context, domain.Identity) (domain.User, error) {
	return domain.User{}, nil
}
func (*fakeRepository) GetSettings(context.Context, string) (*domain.Settings, error) {
	return nil, nil
}
func (*fakeRepository) ReplaceSettings(context.Context, string, uint32, []byte, uint64, time.Time) (domain.Settings, error) {
	return domain.Settings{}, nil
}
func (*fakeRepository) GetAccount(context.Context, string) (domain.User, error) {
	return domain.User{}, nil
}
func (*fakeRepository) DeleteAccount(context.Context, string, time.Time) (domain.User, error) {
	return domain.User{}, nil
}
func (*fakeRepository) RestoreAccount(context.Context, string, time.Time) (domain.User, error) {
	return domain.User{}, nil
}
func (repository *fakeRepository) RecordRequest(_ context.Context, request domain.RequestLog) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.requests = append(repository.requests, request)
	return nil
}
func (*fakeRepository) RecordAudit(context.Context, domain.AuditEvent) error { return nil }
func (*fakeRepository) ClaimPurgeBatch(context.Context, time.Time, int) ([]domain.User, error) {
	return nil, nil
}
func (*fakeRepository) CompleteAccountPurge(context.Context, domain.User, time.Time) error {
	return nil
}
func (*fakeRepository) PruneRetention(context.Context, time.Time) (domain.RetentionResult, error) {
	return domain.RetentionResult{}, nil
}
