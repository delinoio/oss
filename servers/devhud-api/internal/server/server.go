package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/protos/gen/go/devhud/v1/devhudv1connect"
	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/config"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/delinoio/oss/servers/devhud-api/internal/rpc"
	uploadmanager "github.com/delinoio/oss/servers/devhud-api/internal/upload"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const handlerExecutionTimeout = 25 * time.Second

type Dependencies struct {
	Config         config.Config
	Repository     domain.Repository
	Verifier       auth.Verifier
	Clock          domain.Clock
	IDs            domain.IDGenerator
	Logger         *slog.Logger
	MetricsHandler http.Handler
	Uploads        *uploadmanager.Service
}

func New(dependencies Dependencies) (*http.Server, error) {
	mux := http.NewServeMux()
	authInterceptor := rpc.NewAuthInterceptor(dependencies.Verifier, dependencies.Repository, dependencies.Logger)
	handlerOptions := []connect.HandlerOption{recoverConnectPanics(dependencies.Logger), connect.WithInterceptors(authInterceptor)}

	bootstrapPath, bootstrapHandler := devhudv1connect.NewBootstrapServiceHandler(rpc.NewBootstrapService(rpc.BootstrapConfig{
		APIVersion:         dependencies.Config.APIVersion,
		LogtoIssuer:        dependencies.Config.LogtoIssuer,
		LogtoAudience:      dependencies.Config.LogtoAudience,
		DesktopClientID:    dependencies.Config.DesktopClientID,
		IOSClientID:        dependencies.Config.IOSClientID,
		AndroidClientID:    dependencies.Config.AndroidClientID,
		AdminClientID:      dependencies.Config.AdminClientID,
		AdminRedirectURI:   dependencies.Config.AdminRedirectURI,
		PublicAssetBaseURL: dependencies.Config.PublicAssetBaseURL,
		OfficialUploads:    dependencies.Uploads != nil,
	}), handlerOptions...)
	settingsPath, settingsHandler := devhudv1connect.NewSettingsServiceHandler(rpc.NewSettingsService(dependencies.Repository, dependencies.Clock, dependencies.Logger), handlerOptions...)
	accountPath, accountHandler := devhudv1connect.NewAccountServiceHandler(rpc.NewAccountService(dependencies.Repository, dependencies.Clock, dependencies.Logger), handlerOptions...)
	diagnosticsPath, diagnosticsHandler := devhudv1connect.NewDiagnosticsServiceHandler(rpc.NewDiagnosticsService(dependencies.Repository, dependencies.Clock, dependencies.Logger), handlerOptions...)
	mux.Handle(bootstrapPath, bootstrapHandler)
	mux.Handle(settingsPath, settingsHandler)
	mux.Handle(accountPath, accountHandler)
	mux.Handle(diagnosticsPath, diagnosticsHandler)
	if dependencies.Uploads != nil {
		uploadPath, uploadHandler := devhudv1connect.NewUploadServiceHandler(rpc.NewUploadService(dependencies.Uploads, dependencies.Logger), handlerOptions...)
		mux.Handle(uploadPath, uploadHandler)
	}

	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, map[string]string{"status": "alive"})
	})
	mux.HandleFunc("GET /readyz", func(response http.ResponseWriter, request *http.Request) {
		current, err := dependencies.Repository.SchemaCurrent(request.Context())
		if err != nil || !current || dependencies.Repository.Ping(request.Context()) != nil {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		writeJSON(response, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.Handle("GET /metrics", dependencies.MetricsHandler)

	connectPaths := map[string]struct{}{
		devhudv1connect.BootstrapServiceGetBootstrapProcedure:        {},
		devhudv1connect.SettingsServiceGetSettingsProcedure:          {},
		devhudv1connect.SettingsServiceReplaceSettingsProcedure:      {},
		devhudv1connect.AccountServiceGetAccountProcedure:            {},
		devhudv1connect.AccountServiceDeleteAccountProcedure:         {},
		devhudv1connect.AccountServiceRestoreAccountProcedure:        {},
		devhudv1connect.DiagnosticsServiceSubmitCrashReportProcedure: {},
	}
	if dependencies.Uploads != nil {
		connectPaths[devhudv1connect.UploadServiceCreateUploadProcedure] = struct{}{}
		connectPaths[devhudv1connect.UploadServiceFinalizeUploadProcedure] = struct{}{}
		connectPaths[devhudv1connect.UploadServiceListUploadsProcedure] = struct{}{}
		connectPaths[devhudv1connect.UploadServiceDeleteUploadProcedure] = struct{}{}
	}
	requestMetrics, err := newRequestMetrics()
	if err != nil {
		return nil, err
	}

	var handler http.Handler = withHandlerExecutionDeadline(mux, handlerExecutionTimeout)
	handler = connectErrorMetadata(connectPaths, handler)
	handler = cors(handler, connectPaths)
	handler = requireHTTPS(dependencies.Config.Environment, dependencies.Config.TrustedProxyCIDRs, handler)
	handler = recoverPanics(dependencies.Logger, handler)
	handler = observeRequests(dependencies.Logger, dependencies.Repository, dependencies.Clock, dependencies.IDs, requestMetrics, handler)
	// Keep request observation inside the HTTP span so metric recordings carry
	// trace context (and trace-based exemplars when the exporter supports them).
	handler = otelhttp.NewHandler(handler, "devhud-api")
	handler = correlation(dependencies.IDs, handler)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	return &http.Server{
		Addr:              dependencies.Config.ListenAddress,
		Handler:           handler,
		Protocols:         protocols,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}, nil
}

func withHandlerExecutionDeadline(next http.Handler, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), timeout)
		defer cancel()
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
