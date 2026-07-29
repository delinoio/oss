// Package logging constructs RealQA's redacted structured logger.
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
