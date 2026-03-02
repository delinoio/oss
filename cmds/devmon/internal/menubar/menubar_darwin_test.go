//go:build darwin

package menubar

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/devmon/internal/servicecontrol"
)

func TestStatusTitle(t *testing.T) {
	app := &menuApp{}

	if title := app.statusTitle(servicecontrol.Summary{DaemonHealth: servicecontrol.DaemonHealthRunning}); title != "Status: running" {
		t.Fatalf("expected running status title, got=%s", title)
	}

	if title := app.statusTitle(servicecontrol.Summary{DaemonHealth: servicecontrol.DaemonHealthStopped}); title != "Status: stopped" {
		t.Fatalf("expected stopped status title, got=%s", title)
	}

	errorTitle := app.statusTitle(servicecontrol.Summary{
		DaemonHealth: servicecontrol.DaemonHealthError,
		Message:      "heartbeat stale",
	})
	if !strings.Contains(errorTitle, "Status: error (heartbeat stale)") {
		t.Fatalf("unexpected error status title: %s", errorTitle)
	}
}

func TestTruncateForMenu(t *testing.T) {
	short := "short status"
	if truncated := truncateForMenu(short); truncated != short {
		t.Fatalf("expected short text to remain unchanged, got=%s", truncated)
	}

	long := "this is a very long status message that should be truncated for menu"
	truncated := truncateForMenu(long)
	if len(truncated) != 48 {
		t.Fatalf("expected truncated length=48, got=%d (%s)", len(truncated), truncated)
	}
	if !strings.HasSuffix(truncated, "...") {
		t.Fatalf("expected truncated suffix ..., got=%s", truncated)
	}
}

func TestHandleActionErrorUpdatesState(t *testing.T) {
	app := &menuApp{
		logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		state:  &menuState{},
	}

	firstError := errors.New("start failed")
	app.handleActionError("start", firstError)
	if app.state.lastErr == nil || app.state.lastErr.Error() != "start failed" {
		t.Fatalf("expected lastErr to be start failed, got=%v", app.state.lastErr)
	}

	secondError := errors.New("stop failed")
	app.handleActionError("stop", secondError)
	if app.state.lastErr == nil || app.state.lastErr.Error() != "stop failed" {
		t.Fatalf("expected lastErr to be overwritten with stop failed, got=%v", app.state.lastErr)
	}
}
