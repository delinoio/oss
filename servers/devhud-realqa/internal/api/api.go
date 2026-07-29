// Package api assembles RealQA's generated Connect and health handlers.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1/realqav1connect"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/authn"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/imageassets"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/rqerr"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/service"
	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safelog"
)

const readinessTimeout = 2 * time.Second

type HealthChecker interface {
	Ping(context.Context) error
}

type Dependencies struct {
	Authentication *authn.Interceptor
	Health         HealthChecker
	Services       service.Dependencies
	Logger         *slog.Logger
}

func New(dependencies Dependencies) (http.Handler, error) {
	if dependencies.Authentication == nil || dependencies.Health == nil {
		return nil, errors.New("realqa api: authentication and health are required")
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.New(slog.DiscardHandler)
	}
	options := []connect.HandlerOption{
		connect.WithInterceptors(
			requestmeta.Interceptor{},
			rqerr.Interceptor{},
			dependencies.Authentication,
		),
	}
	business := http.NewServeMux()
	business.HandleFunc("GET /healthz", live)
	business.Handle("GET /readyz", ready(dependencies.Health))
	path, handler := realqav1connect.NewRealQAPresetServiceHandler(
		service.NewPreset(dependencies.Services), options...)
	business.Handle(path, handler)
	path, handler = realqav1connect.NewRealQATrackerServiceHandler(
		service.NewTracker(dependencies.Services), options...)
	business.Handle(path, handler)
	submissions := service.NewSubmission(dependencies.Services)
	path, handler = realqav1connect.NewRealQASubmissionServiceHandler(
		submissions, options...)
	business.Handle(path, handler)
	if len(dependencies.Services.WebhookSecret) >= 32 {
		business.Handle("POST /webhooks/github/issues",
			issueDeletionWebhook(
				submissions, dependencies.Services.WebhookSecret))
	}

	rootMux := http.NewServeMux()
	if dependencies.Services.Store != nil &&
		dependencies.Services.Objects != nil &&
		dependencies.Services.UploadSigner != nil {
		rootMux.Handle("/uploads/", imageassets.UploadHandler(
			dependencies.Services.UploadSigner,
			submissions.LookupUploadGrant,
			submissions.StoreUploaded,
			time.Now,
		))
		rootMux.Handle("/i/", imageassets.PublicHandler(
			dependencies.Services.UploadSigner,
			dependencies.Services.Objects, submissions.PublicAsset))
	}
	rootMux.Handle("/", rejectBrowserOrigins(business))

	var root http.Handler = rootMux
	root = requestLogger(dependencies.Logger)(root)
	root = requestmeta.Middleware(nil)(root)
	return root, nil
}

func live(writer http.ResponseWriter, _ *http.Request) {
	writeHealth(writer, http.StatusOK, "ok")
}

func ready(checker HealthChecker) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), readinessTimeout)
		defer cancel()
		if err := checker.Ping(ctx); err != nil {
			writeHealth(writer, http.StatusServiceUnavailable, "not_ready")
			return
		}
		writeHealth(writer, http.StatusOK, "ready")
	})
}

func writeHealth(writer http.ResponseWriter, status int, state string) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"status": state})
}

func rejectBrowserOrigins(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Origin") != "" {
			writer.Header().Set("Cache-Control", "no-store")
			http.Error(writer, "browser origins are not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (value *recorder) WriteHeader(status int) {
	if value.status == 0 {
		value.status = status
		value.ResponseWriter.WriteHeader(status)
	}
}

func (value *recorder) Write(body []byte) (int, error) {
	if value.status == 0 {
		value.WriteHeader(http.StatusOK)
	}
	return value.ResponseWriter.Write(body)
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			response := &recorder{ResponseWriter: writer}
			next.ServeHTTP(response, request)
			result := safelog.ResultSuccess
			if response.status >= 400 {
				result = safelog.ResultFailure
			}
			safelog.Record(request.Context(), logger, slog.LevelInfo,
				safelog.EventRequest, safelog.Fields{
					Method: request.Method, Result: result,
				})
		})
	}
}
