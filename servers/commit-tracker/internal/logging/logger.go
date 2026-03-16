package logging

import (
	"log/slog"
	"os"
)

// New creates a structured slog logger with the [commit-tracker] prefix group.
func New() *slog.Logger {
	return slog.New(
		slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}),
	).WithGroup("commit-tracker")
}
