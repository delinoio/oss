// SPDX-License-Identifier: Apache-2.0
package userservice

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestLaunchAdmissionCanonicalizesRelativeScope(t *testing.T) {
	m, _ := fixture(t)
	m.Kind = Server
	control(t, m, Install, 0)
	// Windows CI can place the checkout and temporary scope on different drives.
	// Use the fixture's parent so the scope has a relative spelling on every host.
	t.Chdir(filepath.Dir(m.Root))
	relative := filepath.Base(m.Root)
	admission, err := AdmitLaunch(context.Background(), relative)
	if err != nil {
		t.Fatal("relative admission", err)
	}
	defer admission.Close()
	if admission.root != m.Root {
		t.Fatal("relative admission changed scope identity")
	}
	if managed, stopped, err := admission.Managed(); err != nil || !managed || !stopped {
		t.Fatal("relative admission lost native registration", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := AdmitLaunch(ctx, m.Root); err == nil || ctx.Err() == nil {
		t.Fatal("absolute spelling crossed the relative admission lock", err)
	}
}

func TestLaunchAdmissionPinsConcurrentServiceControl(t *testing.T) {
	m, f := fixture(t)
	m.Kind = Server
	for _, action := range []Action{Install, Start, Remove} {
		admission, err := AdmitLaunch(context.Background(), m.Root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Control(context.Background(), action, domain.NewID(), 0, "fixture-owner"); err == nil {
			t.Fatal("service control crossed launch admission", action)
		}
		if len(f.writes) != 0 {
			t.Fatal("blocked control wrote native state")
		}
		if managed, _, err := admission.Managed(); err != nil || managed {
			t.Fatal("blocked control changed registration", err)
		}
		admission.Close()
	}
	control(t, m, Install, 0)
	for _, action := range []Action{Start, Remove} {
		admission, err := AdmitLaunch(context.Background(), m.Root)
		if err != nil {
			t.Fatal(err)
		}
		if managed, stopped, err := admission.Managed(); err != nil || !managed || !stopped {
			t.Fatal("registered scope lost ownership", err)
		}
		if _, err := m.Control(context.Background(), action, domain.NewID(), 1, "fixture-owner"); err == nil {
			t.Fatal("registered control crossed launch admission")
		}
		admission.Close()
	}
	// A launch also waits for an existing native controller, with cancellation.
	lock, err := AdmitLaunch(context.Background(), m.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := AdmitLaunch(ctx, m.Root); err == nil || ctx.Err() == nil {
		t.Fatal("overlapping launch did not honor cancellation")
	}
	lock.Close()
	control(t, m, Remove, 1)
	controlCtx, controlCancel := context.WithTimeout(context.Background(), time.Second)
	defer controlCancel()
	after, err := AdmitLaunch(controlCtx, m.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	if managed, _, err := after.Managed(); err != nil || managed {
		t.Fatal("removed registration claimed scope", err)
	}
}

func TestWorkerLaunchAdmissionPreservesServiceOwnership(t *testing.T) {
	m, native := fixture(t)
	m.Kind = Worker
	control(t, m, Install, 0)
	writes := len(native.writes)
	admission, err := AdmitKindLaunch(context.Background(), m.Root, Worker)
	if err != nil {
		t.Fatal(err)
	}
	defer admission.Close()
	if managed, stopped, err := admission.Managed(); err != nil || !managed || !stopped {
		t.Fatal("installed Worker service lost ownership", managed, stopped, err)
	}
	if _, err := m.Control(context.Background(), Start, domain.NewID(), 1, "fixture-owner"); err == nil {
		t.Fatal("Worker service control crossed desktop admission")
	}
	if len(native.writes) != writes {
		t.Fatal("admission changed native service state")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := AdmitKindLaunch(ctx, m.Root, Worker); err == nil || ctx.Err() == nil {
		t.Fatal("competing Worker admission crossed control lock")
	}
}
