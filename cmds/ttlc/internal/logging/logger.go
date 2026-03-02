package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

type Options struct {
	Level   string
	NoColor bool
}

func NewWithWriter(w io.Writer, options Options) (*slog.Logger, error) {
	if w == nil {
		w = os.Stderr
	}

	parsedLevel, err := parseLevel(options.Level)
	if err != nil {
		return nil, err
	}

	handler := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: parsedLevel,
		ReplaceAttr: func(_ []string, attribute slog.Attr) slog.Attr {
			if attribute.Key == slog.TimeKey {
				attribute.Key = "timestamp"
			}
			if attribute.Key == slog.LevelKey {
				attribute.Value = slog.StringValue(formatLevel(attribute.Value.String(), options.NoColor))
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

func formatLevel(level string, noColor bool) string {
	normalized := strings.ToUpper(strings.TrimSpace(level))
	if noColor {
		return normalized
	}

	switch normalized {
	case "DEBUG":
		return "\x1b[36mDEBUG\x1b[0m"
	case "INFO":
		return "\x1b[32mINFO\x1b[0m"
	case "WARN":
		return "\x1b[33mWARN\x1b[0m"
	case "ERROR":
		return "\x1b[31mERROR\x1b[0m"
	default:
		return normalized
	}
}
