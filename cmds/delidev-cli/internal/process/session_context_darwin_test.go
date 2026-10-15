package process

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestGUIProcessContextRetainsOwnedCoalitionCleanup(t *testing.T) {
	gui := fmt.Sprintf("gui/%d", os.Getuid())
	if err := launchctl("print", gui); err != nil {
		t.Skip("the executing user has no available GUI login session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c := config(t, "sleep")
	c.SessionContext = CurrentUserGUI
	h, err := Start(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	scope, err := readScope(h.Identity().ScopeDir)
	if err != nil || scope.Domain != gui || scope.Coalition == 0 || scope.OwnerID != c.OwnerID {
		t.Fatal("GUI login lost original process ownership", err)
	}
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal("GUI child cleanup was not confirmed", err)
	}
	if err := ReconcileOwner(c.Directory, c.OwnerID); err != nil {
		t.Fatal("GUI scope could not reconcile its original owner", err)
	}
}
