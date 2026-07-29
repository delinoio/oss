// Package logging constructs Deck's root redacting structured logger.
package logging

import (
	"io"
	"log/slog"

	"github.com/delinoio/oss/servers/internal/safelog"
)

func New(output io.Writer, level slog.Leveler) *slog.Logger {
	if output == nil {
		output = io.Discard
	}
	if level == nil {
		level = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level})
	return slog.New(safelog.NewRedactingHandler(handler))
}

func Startup(logger *slog.Logger, address string) {
	if logger != nil {
		logger.Info("Deck server started",
			"event", "startup",
			"listen_address", address,
			"api_origin", "https://deck.deli.dev")
	}
}

func Shutdown(logger *slog.Logger) {
	if logger != nil {
		logger.Info("Deck server stopped", "event", "shutdown")
	}
}
