// Package runtime owns RealQA's HTTP serve and graceful shutdown lifecycle.
package runtime

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/delinoio/oss/servers/internal/httpserver"
)

func Serve(
	ctx context.Context,
	listener net.Listener,
	handler http.Handler,
	logger *slog.Logger,
	shutdownTimeout time.Duration,
) error {
	if listener == nil || handler == nil || shutdownTimeout <= 0 {
		return errors.New("realqa runtime: invalid server dependencies")
	}
	server := httpserver.Server(listener.Addr().String(), handler,
		httpserver.DefaultTimeouts())
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	if logger != nil {
		logger.Info("RealQA server started", "event", "startup",
			"listen_address", listener.Addr().String())
	}
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("realqa runtime: HTTP server stopped unexpectedly")
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return errors.New("realqa runtime: graceful shutdown timed out")
		}
		if err := <-result; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.New("realqa runtime: HTTP server stopped unexpectedly")
		}
		if logger != nil {
			logger.Info("RealQA server stopped", "event", "shutdown")
		}
		return nil
	}
}
