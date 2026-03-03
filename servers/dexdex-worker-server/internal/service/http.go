package service

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	v1connect "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
)

func MountExecutionService(mux *http.ServeMux, executionService *ExecutionService) {
	executionPath, executionHandler := v1connect.NewExecutionServiceHandler(executionService)
	mux.Handle(executionPath, executionHandler)
}

func AuthMiddleware(next http.Handler, token string, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
		writer.Header().Set("X-Request-Id", requestID)

		if token == "" {
			next.ServeHTTP(writer, request)
			return
		}

		rawAuthorization := strings.TrimSpace(request.Header.Get("Authorization"))
		if !strings.HasPrefix(rawAuthorization, "Bearer ") {
			logger.Warn("auth.denied", "request_id", requestID, "reason", "missing_bearer")
			http.Error(writer, "missing bearer token", http.StatusUnauthorized)
			return
		}

		provided := strings.TrimSpace(strings.TrimPrefix(rawAuthorization, "Bearer "))
		if provided != token {
			logger.Warn("auth.denied", "request_id", requestID, "reason", "token_mismatch")
			http.Error(writer, "invalid bearer token", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(writer, request)
	})
}
