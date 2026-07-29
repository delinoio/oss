// Package api assembles Deck's health and generated Connect handlers.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1/deckv1connect"
	"github.com/delinoio/oss/servers/devhud-deck/internal/authn"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/service"
	"github.com/delinoio/oss/servers/internal/httpserver"
	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safeerr"
	"github.com/delinoio/oss/servers/internal/safelog"
)

const readinessTimeout = 2 * time.Second

type HealthChecker interface {
	Ping(context.Context) error
}

type Dependencies struct {
	DeckAuthentication     authn.Validator
	DelibaseAuthentication authn.Validator
	Directory              contracts.Directory
	LifecycleClientID      string
	Health                 HealthChecker
	Services               service.Dependencies
	Logger                 *slog.Logger
}

func New(dependencies Dependencies) (http.Handler, error) {
	if dependencies.Health == nil || dependencies.Logger == nil {
		return nil, errors.New("deck api: health checker and logger are required")
	}
	authentication, err := authn.New(authn.Dependencies{
		DeckValidator:            dependencies.DeckAuthentication,
		DelibaseValidator:        dependencies.DelibaseAuthentication,
		Directory:                dependencies.Directory,
		LifecycleClientID:        dependencies.LifecycleClientID,
		RequireLifecycleClientID: true,
	})
	if err != nil {
		return nil, err
	}
	options := []connect.HandlerOption{
		connect.WithInterceptors(
			requestmeta.Interceptor{},
			safeerr.Interceptor{},
			authentication,
		),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", live)
	mux.Handle("GET /readyz", ready(dependencies.Health))
	path, handler := deckv1connect.NewDeckViewServiceHandler(
		service.NewView(dependencies.Services), options...)
	mux.Handle(path, handler)
	path, handler = deckv1connect.NewDeckIntegrationServiceHandler(
		service.NewIntegration(dependencies.Services), options...)
	mux.Handle(path, handler)
	path, handler = deckv1connect.NewDeckDeviceServiceHandler(
		service.NewDevice(dependencies.Services), options...)
	mux.Handle(path, handler)

	corsConfig := httpserver.DefaultCORSConfig()
	corsConfig.AllowedHeaders = append(corsConfig.AllowedHeaders,
		authn.ForwardedDelibaseTokenHeader)
	cors, err := httpserver.CORS(corsConfig)
	if err != nil {
		return nil, err
	}
	handler = cors(browserBoundary(mux))
	handler = requestLogger(dependencies.Logger)(handler)
	handler = requestmeta.Middleware(nil)(handler)
	return handler, nil
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

func browserBoundary(next http.Handler) http.Handler {
	integrationPrefix := "/" + deckv1connect.DeckIntegrationServiceName + "/"
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin != "" && (origin != httpserver.DeliDevOrigin ||
			!strings.HasPrefix(request.URL.Path, integrationPrefix)) {
			http.Error(writer, "browser origin is not allowed for this procedure",
				http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.status == 0 {
		recorder.status = status
		recorder.ResponseWriter.WriteHeader(status)
	}
}

func (recorder *responseRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return recorder.ResponseWriter.Write(body)
}

func (recorder *responseRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			recorder := &responseRecorder{ResponseWriter: writer}
			next.ServeHTTP(recorder, request)
			result := safelog.ResultSuccess
			level := slog.LevelInfo
			if recorder.status >= http.StatusBadRequest {
				result = safelog.ResultFailure
				level = slog.LevelWarn
			}
			safelog.Record(request.Context(), logger, level,
				safelog.EventRequest, safelog.Fields{
					Method: request.Method, Procedure: request.URL.Path,
					Result: result,
				})
		})
	}
}
