package logging

import (
	"bytes"
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

	outputWriter := w
	if !options.NoColor {
		outputWriter = &ansiLevelWriter{next: w}
	}

	handler := slog.NewTextHandler(outputWriter, &slog.HandlerOptions{
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

func formatLevel(level string) string {
	normalized := strings.ToUpper(strings.TrimSpace(level))
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

type ansiLevelWriter struct {
	next io.Writer
}

func (w *ansiLevelWriter) Write(payload []byte) (int, error) {
	coloredPayload := colorizeLevelPayload(payload)
	if len(payload) != len(coloredPayload) {
		_, err := w.next.Write(coloredPayload)
		if err != nil {
			return 0, err
		}
		return len(payload), nil
	}
	return w.next.Write(coloredPayload)
}

func colorizeLevelPayload(payload []byte) []byte {
	updated := append([]byte(nil), payload...)
	updated = bytes.ReplaceAll(updated, []byte("level=DEBUG"), []byte("level="+formatLevel("DEBUG")))
	updated = bytes.ReplaceAll(updated, []byte("level=INFO"), []byte("level="+formatLevel("INFO")))
	updated = bytes.ReplaceAll(updated, []byte("level=WARN"), []byte("level="+formatLevel("WARN")))
	updated = bytes.ReplaceAll(updated, []byte("level=ERROR"), []byte("level="+formatLevel("ERROR")))
	return updated
}
