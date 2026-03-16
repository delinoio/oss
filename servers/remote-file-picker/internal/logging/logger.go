package logging

import (
	"log/slog"
	"os"
)

// NewLogger creates a structured JSON logger writing to stderr.
func NewLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}
