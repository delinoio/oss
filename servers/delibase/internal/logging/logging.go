// Package logging constructs delibase's root structured logger.
package logging

import (
	"io"
	"log/slog"

	"github.com/delinoio/oss/servers/internal/safelog"
)

// New returns a JSON logger with mandatory defense-in-depth redaction.
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

// Startup emits only non-secret process metadata.
func Startup(logger *slog.Logger, address string) {
	if logger == nil {
		return
	}
	logger.Info(
		"delibase server started",
		"event", "startup",
		"listen_address", address,
		"api_origin", "https://delibase.deli.dev",
	)
}

func Shutdown(logger *slog.Logger) {
	if logger != nil {
		logger.Info("delibase server stopped", "event", "shutdown")
	}
}
