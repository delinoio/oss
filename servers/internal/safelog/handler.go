package safelog

import (
	"context"
	"log/slog"

	"github.com/delinoio/oss/servers/internal/redact"
)

// RedactingHandler sanitizes all attributes before delegating to the wrapped
// handler. Use it at the root logger even when callers primarily use Record.
type RedactingHandler struct {
	next slog.Handler
}

// NewRedactingHandler wraps a slog handler.
func NewRedactingHandler(next slog.Handler) *RedactingHandler {
	if next == nil {
		next = slog.DiscardHandler
	}
	return &RedactingHandler{next: next}
}

func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *RedactingHandler) Handle(ctx context.Context, record slog.Record) error {
	safe := slog.NewRecord(record.Time, record.Level, redact.Text(record.Message), record.PC)
	record.Attrs(func(attribute slog.Attr) bool {
		safe.AddAttrs(redactAttr(attribute))
		return true
	})
	return h.next.Handle(ctx, safe)
}

func (h *RedactingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	safe := make([]slog.Attr, len(attributes))
	for index, attribute := range attributes {
		safe[index] = redactAttr(attribute)
	}
	return &RedactingHandler{next: h.next.WithAttrs(safe)}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{next: h.next.WithGroup(redact.Text(name))}
}

func redactAttr(attribute slog.Attr) slog.Attr {
	attribute.Value = attribute.Value.Resolve()
	if redact.IsSensitiveKey(attribute.Key) {
		return slog.String(attribute.Key, redact.Replacement)
	}
	switch attribute.Value.Kind() {
	case slog.KindString:
		return slog.String(attribute.Key, redact.Text(attribute.Value.String()))
	case slog.KindAny:
		return slog.Any(attribute.Key, redact.Value(attribute.Key, attribute.Value.Any()))
	case slog.KindGroup:
		children := attribute.Value.Group()
		safe := make([]any, 0, len(children))
		for _, child := range children {
			safe = append(safe, redactAttr(child))
		}
		return slog.Group(attribute.Key, safe...)
	default:
		return attribute
	}
}
