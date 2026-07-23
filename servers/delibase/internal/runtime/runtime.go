// Package runtime owns the HTTP serve and graceful shutdown lifecycle.
package runtime

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/logging"
	"github.com/delinoio/oss/servers/internal/httpserver"
)

// Serve blocks until the server exits or context cancellation initiates
// graceful shutdown. The supplied listener makes startup testable without
// hidden global ports.
func Serve(
	ctx context.Context,
	listener net.Listener,
	handler http.Handler,
	logger *slog.Logger,
	shutdownTimeout time.Duration,
) error {
	if listener == nil || handler == nil {
		return errors.New("runtime: listener and handler are required")
	}
	if shutdownTimeout <= 0 {
		return errors.New("runtime: shutdown timeout must be positive")
	}
	server := httpserver.Server(listener.Addr().String(), handler, httpserver.DefaultTimeouts())
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- server.Serve(listener)
	}()
	logging.Startup(logger, listener.Addr().String())

	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("runtime: HTTP server stopped unexpectedly")
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return errors.New("runtime: graceful shutdown timed out")
		}
		err := <-serveResult
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.New("runtime: HTTP server stopped unexpectedly")
		}
		logging.Shutdown(logger)
		return nil
	}
}
