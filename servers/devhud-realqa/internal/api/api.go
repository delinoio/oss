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
	Authentication  *authn.Interceptor
	Health          HealthChecker
	Services        service.Dependencies
	GitHubCallbacks http.Handler
	Logger          *slog.Logger
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", live)
	mux.Handle("GET /readyz", ready(dependencies.Health))
	if dependencies.GitHubCallbacks != nil {
		mux.Handle("/github/", dependencies.GitHubCallbacks)
	}
	path, handler := realqav1connect.NewRealQAPresetServiceHandler(
		service.NewPreset(dependencies.Services), options...)
	mux.Handle(path, handler)
	path, handler = realqav1connect.NewRealQATrackerServiceHandler(
		service.NewTracker(dependencies.Services), options...)
	mux.Handle(path, handler)
	path, handler = realqav1connect.NewRealQASubmissionServiceHandler(
		service.NewSubmission(dependencies.Services), options...)
	mux.Handle(path, handler)

	root := rejectBrowserOrigins(mux)
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
