package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

func NewWithWriter(w io.Writer, level string) (*slog.Logger, error) {
	if w == nil {
		w = os.Stderr
	}

	parsedLevel, err := parseLevel(level)
	if err != nil {
		return nil, err
	}

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: parsedLevel,
		ReplaceAttr: func(_ []string, attribute slog.Attr) slog.Attr {
			if attribute.Key == slog.TimeKey {
				attribute.Key = "timestamp"
			}
			return attribute
		},
	})

	return slog.New(handler), nil
}

func Event(logger *slog.Logger, level slog.Level, event string, attrs ...slog.Attr) {
	if logger == nil {
		return
	}

	allAttributes := make([]slog.Attr, 0, len(attrs)+1)
	allAttributes = append(allAttributes, slog.String("event", event))
	allAttributes = append(allAttributes, attrs...)
	logger.LogAttrs(context.Background(), level, event, allAttributes...)
}

func parseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unsupported log level: %s", level)
	}
}
