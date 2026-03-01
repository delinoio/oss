//go:build !darwin

package menubar

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestRunReturnsUnsupportedError(t *testing.T) {
	err := Run(context.Background(), slog.Default())
	if err == nil {
		t.Fatal("expected unsupported platform error")
	}
	if !strings.Contains(err.Error(), "menubar is only supported on darwin") {
		t.Fatalf("unexpected error: %v", err)
	}
}
