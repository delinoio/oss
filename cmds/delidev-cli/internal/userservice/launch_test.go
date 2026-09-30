// SPDX-License-Identifier: Apache-2.0
package userservice

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

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
	controlCtx, controlCancel := context.WithTimeout(context.Background(), time.Second)
	defer controlCancel()
	lock.Close()
	control(t, m, Remove, 1)
	after, err := AdmitLaunch(controlCtx, m.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	if managed, _, err := after.Managed(); err != nil || managed {
		t.Fatal("removed registration claimed scope", err)
	}
}
