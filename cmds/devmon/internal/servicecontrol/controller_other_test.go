//go:build !darwin

package servicecontrol

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

func TestUnsupportedManagerReturnsDeterministicErrors(t *testing.T) {
	manager, err := NewManager(slog.Default())
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	if err := manager.Install(context.Background()); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform for install, got=%v", err)
	}
	if err := manager.Uninstall(context.Background()); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform for uninstall, got=%v", err)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform for start, got=%v", err)
	}
	if err := manager.Stop(context.Background()); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform for stop, got=%v", err)
	}

	summary, err := manager.Status(context.Background())
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform for status, got=%v", err)
	}
	if summary.DaemonHealth != DaemonHealthError {
		t.Fatalf("expected DaemonHealthError for unsupported status, got=%s", summary.DaemonHealth)
	}
}
