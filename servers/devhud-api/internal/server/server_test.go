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
		devhudv1.StaticCapability_STATIC_CAPABILITY_CRASH_REPORTS,
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

func TestDiagnosticsProcedureIsSafeForLogsAndMetrics(t *testing.T) {
	if got := safeProcedure(devhudv1connect.DiagnosticsServiceSubmitCrashReportProcedure); got != devhudv1connect.DiagnosticsServiceSubmitCrashReportProcedure {
		t.Fatalf("safe diagnostics procedure = %q", got)
	}
}

func TestUpdaterProcedureUsesAStableObservabilityLabel(t *testing.T) {
	for _, path := range []string{
		"/updates/stable/linux/x86_64.json",
		"/updates/stable/windows/aarch64.json",
	} {
		if got := safeProcedure(path); got != updaterManifestProcedure {
			t.Fatalf("safe updater procedure for %q = %q", path, got)
		}
	}
	for _, path := range []string{
		"/updates/stable/linux",
		"/updates/stable/linux/x86_64.json/extra",
		"/updates//linux/x86_64.json",
	} {
		if got := safeProcedure(path); got != "unmatched" {
			t.Fatalf("safe malformed updater procedure for %q = %q", path, got)
		}
	}

	handler, repository := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/updates/stable/linux/x86_64.json", nil)
	request.Header.Set("X-DevHud-Package", "linux-deb")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := repository.lastRequest(t).Procedure; got != updaterManifestProcedure {
		t.Fatalf("persisted updater procedure = %q", got)
	}
}

func TestBootstrapGRPCProtocolsPreserveSuccessResponses(t *testing.T) {
	handler, _ := testHandler(t)
	testServer := httptest.NewServer(handler)
	defer testServer.Close()
	for name, protocolOption := range map[string]connect.ClientOption{
		"gRPC":     connect.WithGRPC(),
		"gRPC-Web": connect.WithGRPCWeb(),
	} {
		t.Run(name, func(t *testing.T) {
			client := devhudv1connect.NewBootstrapServiceClient(http.DefaultClient, testServer.URL, protocolOption)
			response, err := client.GetBootstrap(context.Background(), connect.NewRequest(&devhudv1.GetBootstrapRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			headerID := response.Header().Get(correlationHeader)
			if headerID == "" || response.Msg.GetMetadata().GetCorrelationId().GetValue() != headerID {
				t.Fatalf("correlation metadata/header mismatch: header=%q message=%q", headerID, response.Msg.GetMetadata().GetCorrelationId().GetValue())
			}
		})
	}
}

func TestBootstrapSupportsNativeGRPCOverH2C(t *testing.T) {
	httpServer := testHTTPServerWithRepositoryAndVerifier(
		t,
		&fakeRepository{},
		fakeVerifier{},
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	)
	if httpServer.Protocols == nil || !httpServer.Protocols.HTTP1() || !httpServer.Protocols.UnencryptedHTTP2() {
		t.Fatalf("server protocols = %v, want HTTP/1 and unencrypted HTTP/2", httpServer.Protocols)
	}
	testServer := httptest.NewUnstartedServer(httpServer.Handler)
	testServer.Config.Protocols = httpServer.Protocols
	testServer.Start()
	defer testServer.Close()

	clientProtocols := new(http.Protocols)
	clientProtocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: clientProtocols}
	defer transport.CloseIdleConnections()
	client := devhudv1connect.NewBootstrapServiceClient(&http.Client{Transport: transport}, testServer.URL, connect.WithGRPC())
	response, err := client.GetBootstrap(context.Background(), connect.NewRequest(&devhudv1.GetBootstrapRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Msg.GetMetadata().GetCorrelationId().GetValue() == "" {
		t.Fatal("native gRPC response is missing correlation metadata")
	}
}

func TestHealthzDoesNotPersistRequestMetadata(t *testing.T) {
	handler, repository := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if repository.requestCount() != 0 {
		t.Fatalf("persisted health request logs = %d", repository.requestCount())
	}
}

func TestDatabaseBackedHandlerHasServerDeadlineWithoutClientTimeout(t *testing.T) {
	repository := &deadlineRepository{observations: make(chan deadlineObservation, 1)}
	handler := testHandlerWithRepositoryAndVerifier(t, repository, validVerifier{})
	testServer := httptest.NewServer(handler)
	defer testServer.Close()
	client := devhudv1connect.NewSettingsServiceClient(http.DefaultClient, testServer.URL)
	request := connect.NewRequest(&devhudv1.GetSettingsRequest{})
	request.Header().Set("Authorization", "Bearer valid")
	if _, err := client.GetSettings(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	observation := <-repository.observations
	if !observation.ok {
		t.Fatal("database-backed handler context has no deadline")
	}
	remaining := observation.deadline.Sub(observation.observedAt)
	if remaining < handlerExecutionTimeout-time.Second || remaining > handlerExecutionTimeout {
		t.Fatalf("handler deadline remaining = %v, want approximately %v", remaining, handlerExecutionTimeout)
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

func TestRPCStatusRecordedForEveryProtocol(t *testing.T) {
	for name, protocolOption := range map[string]connect.ClientOption{
		"Connect":  nil,
		"gRPC":     connect.WithGRPC(),
		"gRPC-Web": connect.WithGRPCWeb(),
	} {
		t.Run(name, func(t *testing.T) {
			handler, repository := testHandler(t)
			testServer := httptest.NewServer(handler)
			defer testServer.Close()
			options := []connect.ClientOption{}
			if protocolOption != nil {
				options = append(options, protocolOption)
			}
			client := devhudv1connect.NewSettingsServiceClient(http.DefaultClient, testServer.URL, options...)
			_, err := client.GetSettings(context.Background(), connect.NewRequest(&devhudv1.GetSettingsRequest{}))
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("code = %v, want Unauthenticated", connect.CodeOf(err))
			}
			record := repository.lastRequest(t)
			if record.RPCStatusCode != domain.RPCStatusCodeUnauthenticated {
				t.Fatalf("RPC status = %q, want %q", record.RPCStatusCode, domain.RPCStatusCodeUnauthenticated)
			}
			if name != "Connect" && record.HTTPStatus != http.StatusOK {
				t.Fatalf("gRPC transport status = %d, want 200", record.HTTPStatus)
			}
		})
	}
}

func TestVerificationOutagesAreLoggedAndReturnUnavailable(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	httpServer, err := New(Dependencies{
		Config: config.Config{
			Environment: config.EnvironmentDevelopment, ListenAddress: "127.0.0.1:46307", APIVersion: "test", LogtoIssuer: "https://issuer.example",
			LogtoAudience: "audience", DesktopClientID: "desktop", IOSClientID: "ios", AndroidClientID: "android",
			AdminClientID: "admin", AdminRedirectURI: "https://api.example/admin/auth/callback", PublicAssetBaseURL: "https://assets.example",
		},
		Repository: &fakeRepository{}, Verifier: unavailableVerifier{}, Clock: fixedClock{now: time.Now()}, IDs: randomIDs{},
		Logger: logger, MetricsHandler: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(httpServer.Handler)
	defer testServer.Close()
	client := devhudv1connect.NewSettingsServiceClient(http.DefaultClient, testServer.URL)
	request := connect.NewRequest(&devhudv1.GetSettingsRequest{})
	request.Header().Set("Authorization", "Bearer secret-token")
	_, err = client.GetSettings(context.Background(), request)
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", connect.CodeOf(err))
	}
	if strings.Contains(err.Error(), "JWKS timeout") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("response exposed verification details: %v", err)
	}
	for _, value := range []string{"identity verification failed", devhudv1connect.SettingsServiceGetSettingsProcedure, "JWKS timeout"} {
		if !strings.Contains(logs.String(), value) {
			t.Fatalf("log %q does not contain %q", logs.String(), value)
		}
	}
	if strings.Contains(logs.String(), "secret-token") || !strings.Contains(logs.String(), `"correlation_id":"`) {
		t.Fatalf("verification log was not safely correlated: %q", logs.String())
	}
}

func TestProvisioningFailuresAreLoggedBeforeInternalResponse(t *testing.T) {
	repository := &provisionFailureRepository{err: errors.New("database timeout")}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	httpServer, err := New(Dependencies{
		Config: config.Config{
			Environment: config.EnvironmentDevelopment, ListenAddress: "127.0.0.1:46307", APIVersion: "test", LogtoIssuer: "https://issuer.example",
			LogtoAudience: "audience", DesktopClientID: "desktop", IOSClientID: "ios", AndroidClientID: "android",
			AdminClientID: "admin", AdminRedirectURI: "https://api.example/admin/auth/callback", PublicAssetBaseURL: "https://assets.example",
		},
		Repository: repository, Verifier: validVerifier{}, Clock: fixedClock{now: time.Now()}, IDs: randomIDs{},
		Logger: logger, MetricsHandler: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(httpServer.Handler)
	defer testServer.Close()
	client := devhudv1connect.NewSettingsServiceClient(http.DefaultClient, testServer.URL)
	request := connect.NewRequest(&devhudv1.GetSettingsRequest{})
	request.Header().Set("Authorization", "Bearer valid")
	_, err = client.GetSettings(context.Background(), request)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if strings.Contains(err.Error(), "database timeout") {
		t.Fatalf("internal response exposed repository error: %v", err)
	}
	for _, value := range []string{"account provisioning failed", devhudv1connect.SettingsServiceGetSettingsProcedure, "database timeout"} {
		if !strings.Contains(logs.String(), value) {
			t.Fatalf("log %q does not contain %q", logs.String(), value)
		}
	}
	if !strings.Contains(logs.String(), `"correlation_id":"`) || strings.Contains(logs.String(), `"correlation_id":""`) {
		t.Fatalf("log is missing a nonempty correlation ID: %q", logs.String())
	}
	if strings.Contains(logs.String(), "Bearer valid") {
		t.Fatalf("log exposed authorization header: %q", logs.String())
	}
}

func TestPurgedIdentityMapsEveryAccountProcedureToAccountFailure(t *testing.T) {
	repository := &provisionFailureRepository{err: domain.ErrIdentityPurged}
	httpServer := testHTTPServerWithRepositoryAndVerifier(
		t,
		repository,
		validVerifier{},
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	)
	testServer := httptest.NewServer(httpServer.Handler)
	defer testServer.Close()

	accountClient := devhudv1connect.NewAccountServiceClient(http.DefaultClient, testServer.URL)
	settingsClient := devhudv1connect.NewSettingsServiceClient(http.DefaultClient, testServer.URL)
	tests := []struct {
		name               string
		call               func() error
		wantCode           connect.Code
		wantAccountFailure bool
	}{
		{
			name: "get account",
			call: func() error {
				request := connect.NewRequest(&devhudv1.GetAccountRequest{})
				request.Header().Set("Authorization", "Bearer valid")
				_, err := accountClient.GetAccount(context.Background(), request)
				return err
			},
			wantCode:           connect.CodeFailedPrecondition,
			wantAccountFailure: true,
		},
		{
			name: "delete account",
			call: func() error {
				request := connect.NewRequest(&devhudv1.DeleteAccountRequest{})
				request.Header().Set("Authorization", "Bearer valid")
				_, err := accountClient.DeleteAccount(context.Background(), request)
				return err
			},
			wantCode:           connect.CodeFailedPrecondition,
			wantAccountFailure: true,
		},
		{
			name: "restore account",
			call: func() error {
				request := connect.NewRequest(&devhudv1.RestoreAccountRequest{})
				request.Header().Set("Authorization", "Bearer valid")
				_, err := accountClient.RestoreAccount(context.Background(), request)
				return err
			},
			wantCode:           connect.CodeFailedPrecondition,
			wantAccountFailure: true,
		},
		{
			name: "settings remain permission denied",
			call: func() error {
				request := connect.NewRequest(&devhudv1.GetSettingsRequest{})
				request.Header().Set("Authorization", "Bearer valid")
				_, err := settingsClient.GetSettings(context.Background(), request)
				return err
			},
			wantCode: connect.CodePermissionDenied,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if connect.CodeOf(err) != test.wantCode {
				t.Fatalf("code = %v, want %v: %v", connect.CodeOf(err), test.wantCode, err)
			}
			connectError := new(connect.Error)
			if !errors.As(err, &connectError) {
				t.Fatalf("error = %v", err)
			}
			for _, detail := range connectError.Details() {
				value, valueErr := detail.Value()
				if valueErr != nil {
					t.Fatal(valueErr)
				}
				if test.wantAccountFailure {
					failure, ok := value.(*devhudv1.AccountFailure)
					if ok && failure.GetReason() == devhudv1.AccountFailureReason_ACCOUNT_FAILURE_REASON_PURGE_CLAIMED {
						return
					}
					continue
				}
				failure, ok := value.(*devhudv1.PermissionFailure)
				if ok && failure.GetReason() == devhudv1.PermissionFailureReason_PERMISSION_FAILURE_REASON_ACCOUNT_DELETION_PENDING {
					return
				}
			}
			t.Fatal("missing expected purge-completed error detail")
		})
	}
}

func TestRequestPersistenceFailuresIncludeCauseInWarning(t *testing.T) {
	repository := &requestPersistenceFailureRepository{err: errors.New("request log timeout")}
	var logs bytes.Buffer
	httpServer := testHTTPServerWithRepositoryAndVerifier(
		t,
		repository,
		fakeVerifier{},
		slog.New(slog.NewJSONHandler(&logs, nil)),
	)
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	for _, value := range []string{"request metadata persistence failed", `"error":"request log timeout"`, `"correlation_id":"`} {
		if !strings.Contains(logs.String(), value) {
			t.Fatalf("log %q does not contain %q", logs.String(), value)
		}
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

func TestMalformedProtobufErrorsCarryCorrelationDetailForEveryProtocol(t *testing.T) {
	handler, _ := testHandler(t)
	testServer := httptest.NewServer(handler)
	defer testServer.Close()
	for name, protocolOption := range map[string]connect.ClientOption{
		"Connect":  nil,
		"gRPC":     connect.WithGRPC(),
		"gRPC-Web": connect.WithGRPCWeb(),
	} {
		t.Run(name, func(t *testing.T) {
			options := []connect.ClientOption{connect.WithCodec(malformedProtoCodec{})}
			if protocolOption != nil {
				options = append(options, protocolOption)
			}
			client := devhudv1connect.NewBootstrapServiceClient(http.DefaultClient, testServer.URL, options...)
			_, err := client.GetBootstrap(context.Background(), connect.NewRequest(&devhudv1.GetBootstrapRequest{}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument: %v", connect.CodeOf(err), err)
			}
			connectError := new(connect.Error)
			if !errors.As(err, &connectError) {
				t.Fatalf("error = %v", err)
			}
			headerID := connectError.Meta().Get(correlationHeader)
			for _, detail := range connectError.Details() {
				value, valueErr := detail.Value()
				if valueErr != nil {
					t.Fatal(valueErr)
				}
				metadata, ok := value.(*devhudv1.ErrorMetadata)
				if ok && headerID != "" && metadata.GetCorrelationId().GetValue() == headerID {
					return
				}
			}
			t.Fatalf("missing matching ErrorMetadata: header=%q error=%v", headerID, err)
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
		if methods := response.Header().Get("Access-Control-Allow-Methods"); methods != "POST,OPTIONS" {
			t.Errorf("origin %q returned methods %q", origin, methods)
		}
		if strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "*") || !strings.Contains(strings.ToLower(response.Header().Get("Access-Control-Expose-Headers")), correlationHeader) {
			t.Errorf("origin %q returned invalid headers: %v", origin, response.Header())
		}
	}

	for name, mutate := range map[string]func(*http.Request){
		"origin":        func(request *http.Request) { request.Header.Set("Origin", "http://localhost:46305.evil.example") },
		"header":        func(request *http.Request) { request.Header.Set("Access-Control-Request-Headers", "X-Not-Allowed") },
		"GET method":    func(request *http.Request) { request.Header.Set("Access-Control-Request-Method", http.MethodGet) },
		"DELETE method": func(request *http.Request) { request.Header.Set("Access-Control-Request-Method", http.MethodDelete) },
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

func TestUpdaterRouteIsUnavailableToBrowserOrigins(t *testing.T) {
	handler, _ := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/updates/stable/linux/x86_64.json", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Origin", "http://tauri.localhost")
	request.Header.Set("X-DevHud-Package", "linux-deb")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	vary := strings.Join(response.Header().Values("Vary"), ",")
	if response.Code != http.StatusForbidden ||
		response.Header().Get("Access-Control-Allow-Origin") != "" ||
		response.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(vary, "Origin") ||
		!strings.Contains(vary, "X-DevHud-Package") {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
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

func TestHTTPSIsRequiredBeforeCORSPreflight(t *testing.T) {
	httpServer, err := New(Dependencies{
		Config: config.Config{
			Environment: config.EnvironmentProduction, ListenAddress: "0.0.0.0:8080", APIVersion: "test", LogtoIssuer: "https://issuer.example",
			LogtoAudience: "audience", DesktopClientID: "desktop", IOSClientID: "ios", AndroidClientID: "android",
			AdminClientID: "admin", AdminRedirectURI: "https://api.example/admin/auth/callback", PublicAssetBaseURL: "https://assets.example",
		},
		Repository: &fakeRepository{}, Verifier: fakeVerifier{}, Clock: fixedClock{now: time.Now()}, IDs: randomIDs{},
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), MetricsHandler: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]struct {
		url  string
		code int
	}{
		"plaintext": {url: devhudv1connect.SettingsServiceGetSettingsProcedure, code: http.StatusUpgradeRequired},
		"TLS":       {url: "https://api.example" + devhudv1connect.SettingsServiceGetSettingsProcedure, code: http.StatusNoContent},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, target.url, nil)
			request.RemoteAddr = "192.0.2.1:12345"
			request.Header.Set("Origin", "http://localhost:46305")
			request.Header.Set("Access-Control-Request-Method", http.MethodPost)
			request.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
			response := httptest.NewRecorder()
			httpServer.Handler.ServeHTTP(response, request)
			if response.Code != target.code {
				t.Fatalf("status = %d, want %d", response.Code, target.code)
			}
			if name == "plaintext" && response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("plaintext preflight advertised CORS: %v", response.Header())
			}
		})
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
	return testHandlerWithRepositoryAndVerifier(t, repository, verifier), repository
}

func testHandlerWithRepositoryAndVerifier(t *testing.T, repository domain.Repository, verifier auth.Verifier) http.Handler {
	return testHTTPServerWithRepositoryAndVerifier(t, repository, verifier, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))).Handler
}

func testHTTPServerWithRepositoryAndVerifier(t *testing.T, repository domain.Repository, verifier auth.Verifier, logger *slog.Logger) *http.Server {
	t.Helper()
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
		Logger:         logger,
		MetricsHandler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusOK) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return httpServer
}

type fakeVerifier struct{}

func (fakeVerifier) Verify(context.Context, string) (domain.Identity, error) {
	return domain.Identity{}, auth.ErrUnauthenticated
}

type unavailableVerifier struct{}

func (unavailableVerifier) Verify(context.Context, string) (domain.Identity, error) {
	return domain.Identity{}, errors.Join(auth.ErrVerificationUnavailable, errors.New("JWKS timeout"))
}

type panicVerifier struct{}

func (panicVerifier) Verify(context.Context, string) (domain.Identity, error) {
	panic("test panic")
}

type validVerifier struct{}

func (validVerifier) Verify(context.Context, string) (domain.Identity, error) {
	return domain.Identity{Issuer: "https://issuer.example", Subject: "subject"}, nil
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type randomIDs struct{}

func (randomIDs) New() (string, error) {
	id, err := uuid.NewV7()
	return id.String(), err
}

type malformedProtoCodec struct{}

func (malformedProtoCodec) Name() string { return "proto" }

func (malformedProtoCodec) Marshal(any) ([]byte, error) { return []byte{0xff}, nil }

func (malformedProtoCodec) Unmarshal(data []byte, message any) error {
	protobuf, ok := message.(proto.Message)
	if !ok {
		return errors.New("message does not implement proto.Message")
	}
	return proto.Unmarshal(data, protobuf)
}

type fakeRepository struct {
	mu       sync.Mutex
	requests []domain.RequestLog
}

type provisionFailureRepository struct {
	fakeRepository
	err error
}

func (repository *provisionFailureRepository) ProvisionUser(context.Context, domain.Identity) (domain.User, error) {
	return domain.User{}, repository.err
}

type requestPersistenceFailureRepository struct {
	fakeRepository
	err error
}

func (repository *requestPersistenceFailureRepository) RecordRequest(context.Context, domain.RequestLog) error {
	return repository.err
}

type deadlineObservation struct {
	deadline   time.Time
	observedAt time.Time
	ok         bool
}

type deadlineRepository struct {
	fakeRepository
	observations chan deadlineObservation
}

func (repository *deadlineRepository) ProvisionUser(ctx context.Context, _ domain.Identity) (domain.User, error) {
	deadline, ok := ctx.Deadline()
	repository.observations <- deadlineObservation{deadline: deadline, observedAt: time.Now(), ok: ok}
	return domain.User{ID: "user"}, nil
}

func (repository *fakeRepository) requestCount() int {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return len(repository.requests)
}

func (repository *fakeRepository) lastRequest(t *testing.T) domain.RequestLog {
	t.Helper()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.requests) == 0 {
		t.Fatal("no request metadata was persisted")
	}
	return repository.requests[len(repository.requests)-1]
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
func (*fakeRepository) SubmitCrashReport(_ context.Context, userID string, report domain.CrashReport) (domain.CrashReport, error) {
	report.ID = "019a3b7c-8d9e-7f01-9234-56789abcdef0"
	report.OwnerUserID = userID
	return report, nil
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
func (*fakeRepository) PruneRetention(context.Context, time.Time, int) (domain.RetentionResult, error) {
	return domain.RetentionResult{}, nil
}
