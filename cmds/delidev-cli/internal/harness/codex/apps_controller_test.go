// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestAppsControllerRejectsForeignOrReplayedRemovalBeforeNativeWrite(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	request := domain.NewID()
	selection := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{"original"}}
	controller := func() *Client {
		return &Client{mode: ThreadProtocol, version: "0.162.0", home: home, managedHome: home, thread: domain.NewID(), control: make(chan struct{}, 1), apps: &appsController{original: selection.Clone(), current: selection.Clone(), path: filepath.Join(home, "config.toml"), cwd: cwd, version: "original-version", requests: map[domain.ID]bool{request: true}}}
	}
	removed := selection.Clone()
	removed.Generation, removed.AppIDs = domain.NewID(), []string{}
	for _, change := range []func(*Client, *domain.CodexAppConfiguration, *domain.ID, *string){
		func(c *Client, next *domain.CodexAppConfiguration, id *domain.ID, dir *string) { *id = request },
		func(c *Client, next *domain.CodexAppConfiguration, id *domain.ID, dir *string) {
			next.AccountID = domain.NewID()
		},
		func(c *Client, next *domain.CodexAppConfiguration, id *domain.ID, dir *string) {
			next.SessionID = domain.NewID()
		},
		func(c *Client, next *domain.CodexAppConfiguration, id *domain.ID, dir *string) {
			next.Generation = selection.Generation
		},
		func(c *Client, next *domain.CodexAppConfiguration, id *domain.ID, dir *string) {
			next.AppIDs = []string{"foreign"}
		},
		func(c *Client, next *domain.CodexAppConfiguration, id *domain.ID, dir *string) { *dir = home },
		func(c *Client, next *domain.CodexAppConfiguration, id *domain.ID, dir *string) { c.api = &apiBinding{} },
		func(c *Client, next *domain.CodexAppConfiguration, id *domain.ID, dir *string) { c.managedHome = cwd },
		func(c *Client, next *domain.CodexAppConfiguration, id *domain.ID, dir *string) {
			c.problem = appsUncertain()
		},
	} {
		c, next, id, dir := controller(), removed.Clone(), domain.NewID(), cwd
		change(c, &next, &id, &dir)
		// No wire is installed: every invalid scope must fail before native I/O.
		if c.RevokeCodexApps(context.Background(), id, next, dir) == nil {
			t.Fatal("invalid original removal accepted")
		}
		if c.apps.current.Generation != selection.Generation || c.apps.version != "original-version" || len(c.apps.requests) != 1 {
			t.Fatal("rejected removal rewrote original controller")
		}
	}
}

func TestAppsPreparationCannotBorrowAnExistingThreadOrAccountProfile(t *testing.T) {
	selection := domain.CodexAppConfiguration{Version: 1, SessionID: domain.NewID(), AccountID: domain.NewID(), Generation: domain.NewID(), AppIDs: []string{}}
	home := t.TempDir()
	for _, c := range []*Client{
		{mode: ThreadProtocol, version: "0.162.0", home: home, managedHome: home, thread: domain.NewID()},
		{mode: ProbeProtocol, version: "0.162.0", home: home, managedHome: home},
		{mode: ThreadProtocol, version: "0.162.0", home: home},
		{mode: ThreadProtocol, version: "0.161.0", home: home, managedHome: home},
	} {
		c.control = make(chan struct{}, 1)
		if c.PrepareCodexApps(context.Background(), domain.NewID(), selection, home) == nil || c.apps != nil {
			t.Fatal("unrelated native scope acquired apps authority")
		}
	}
}
