package userservice

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestMissingUserBusDoesNotAutolaunch(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if _, err := userBus(context.Background()); domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal("missing existing user bus was accepted", err)
	}
}

func TestUnresponsiveUserBusAuthenticationIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bus")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+path)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := userBus(ctx); domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal("unresponsive bus authorized service control", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("bus authentication ignored cancellation")
	}
}
