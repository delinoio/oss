// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestDesktopStartupConflictClassification(t *testing.T) {
	for _, kind := range []startupConflict{startupOwnership, startupAdmission} {
		problem := safeDesktopFailure(classifiedStartupConflict(domain.Fail(domain.Conflict, "Known safe failure.", "Inspect original ownership."), kind))
		raw, err := json.Marshal(problem)
		if err != nil || problem.StartupConflict != kind || !bytes.Contains(raw, []byte(`"startup_conflict"`)) {
			t.Fatal("missing private classification", err)
		}
	}
	for _, err := range []error{domain.Fail(domain.Conflict, "Legacy conflict.", "Inspect."), context.Canceled, &net.OpError{Op: "private-secret-path"}} {
		problem := safeDesktopFailure(err)
		raw, _ := json.Marshal(problem)
		if problem.StartupConflict != "" || bytes.Contains(raw, []byte("private-secret-path")) {
			t.Fatal("invented classification or private error disclosure")
		}
	}
}

func TestDesktopListenerConflictRequiresPositiveOwnership(t *testing.T) {
	for _, test := range []struct {
		cause error
		kind  startupConflict
	}{
		{&net.OpError{Err: syscall.EADDRINUSE}, startupOwnership},
		{&net.OpError{Err: syscall.EACCES}, ""},
		{&net.AddrError{Err: "private-path-secret"}, ""},
	} {
		failure := safeDesktopFailure(desktopListenerFailure(test.cause))
		raw, _ := json.Marshal(failure)
		if failure.Code != domain.Conflict || failure.StartupConflict != test.kind || bytes.Contains(raw, []byte("private-path-secret")) {
			t.Fatal("unproved ownership or private error disclosure")
		}
	}
}

func TestDesktopStartupConflictPreservesOriginalOwnerAndAllowsRetry(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scope")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	original, err := security.TryLock(filepath.Join(root, "server.lock"))
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "original-state")
	if err := os.WriteFile(marker, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = waitStartupOwnership(context.Background(), root, server.Config{})
	if problem := safeDesktopFailure(err); problem.Code != domain.Conflict || problem.StartupConflict != startupOwnership {
		t.Fatal("ownership was not distinguished", problem)
	}
	if replacement, err := security.TryLock(filepath.Join(root, "server.lock")); err == nil {
		replacement.Close()
		t.Fatal("original owner was released")
	}
	raw, err := os.ReadFile(marker)
	if err != nil || string(raw) != "original" {
		t.Fatal("original state changed", err)
	}
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	retry, err := waitStartupOwnership(context.Background(), root, server.Config{})
	if err != nil {
		t.Fatal("explicit retry retained stale conflict", err)
	}
	retry.Close()
}

func TestDesktopHostOccupiedScopeKeepsOriginalHost(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scope")
	original := startResidentFixture(t, root, "127.0.0.1:0")
	var out bytes.Buffer
	code := runDesktopHostCommand(context.Background(), options{dataDir: root}, []string{"--control-version", "2", "--listen", "127.0.0.1:0"}, IO{In: bytes.NewReader(nil), Out: &out, Err: &bytes.Buffer{}})
	var reply desktopReply
	if json.Unmarshal(out.Bytes(), &reply) != nil || code == 0 || reply.Error == nil || reply.Error.StartupConflict != startupOwnership {
		t.Fatal("second host did not retain typed original ownership")
	}
	// A correlated read-only reply proves the original retained control channel alive.
	original.request(t, "server.desktop-status")
	// Normal shutdown releases only this retained original host. An explicit new
	// admission after observed exit must not retain the preceding conflict.
	if err := json.NewEncoder(original.input).Encode(desktopRequest{Version: 2, ID: domain.NewID(), Operation: desktopShutdown}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-original.done:
		if err != nil {
			t.Fatal("original shutdown failed", err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("original shutdown was not confirmed")
	}
	replacement := startResidentFixture(t, root, "127.0.0.1:0")
	replacement.request(t, "server.desktop-status")

}

func TestDesktopStartupOccupiedListenerDoesNotRemap(t *testing.T) {
	original, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	root := filepath.Join(t.TempDir(), "scope")
	var out bytes.Buffer
	code := runDesktopHostCommand(context.Background(), options{dataDir: root}, []string{"--control-version", "2", "--listen", original.Addr().String()}, IO{In: bytes.NewReader(nil), Out: &out, Err: &bytes.Buffer{}})
	var reply desktopReply
	if json.Unmarshal(out.Bytes(), &reply) != nil || code == 0 || reply.Error == nil || reply.Error.StartupConflict != startupOwnership {
		t.Fatal("occupied listener was not classified")
	}
	if replacement, err := net.Listen("tcp4", original.Addr().String()); err == nil {
		replacement.Close()
		t.Fatal("original listener was replaced")
	}
}
