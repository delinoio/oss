// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"context"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/store"
	"strings"
	"testing"
)

type recoveryFixture struct {
	proof     string
	confirmed bool
	fail      bool
}

func (f *recoveryFixture) ListAdmissionInstances(context.Context) ([]store.AdmissionInstance, error) {
	return []store.AdmissionInstance{{ID: "boot", State: "draining", ActiveAdmissions: 2}}, nil
}
func (f *recoveryFixture) DrainAdmissionInstance(context.Context, string) (string, error) {
	return "fixture-private-proof", nil
}
func (f *recoveryFixture) ReconcileTerminatedInstance(_ context.Context, id, proof string, confirmed bool) (int64, error) {
	f.proof = proof
	f.confirmed = confirmed
	if f.fail {
		return 0, errors.New(proof)
	}
	return 2, nil
}
func TestAdmissionMaintenanceProofBoundary(t *testing.T) {
	ctx := context.Background()
	f := &recoveryFixture{}
	var out bytes.Buffer
	if e := admissionMaintenance(ctx, f, "admission-status", nil, strings.NewReader(""), &out); e != nil || strings.Contains(out.String(), "proof") || !strings.Contains(out.String(), `"active_admissions":2`) {
		t.Fatalf("status: %s %v", out.String(), e)
	}
	out.Reset()
	if e := admissionMaintenance(ctx, f, "admission-drain", []string{"boot"}, strings.NewReader(""), &out); e != nil || out.String() != "fixture-private-proof\n" {
		t.Fatalf("drain boundary: %v", e)
	}
	out.Reset()
	if e := admissionMaintenance(ctx, f, "admission-reconcile", []string{"boot"}, strings.NewReader("fixture-private-proof"), &out); e == nil || f.confirmed {
		t.Fatal("missing confirmation accepted")
	}
	if e := admissionMaintenance(ctx, f, "admission-reconcile", []string{"boot", "--upstreams-stopped"}, strings.NewReader("fixture-private-proof\n"), &out); e != nil || !f.confirmed || f.proof != "fixture-private-proof" || strings.Contains(out.String(), f.proof) {
		t.Fatalf("reconcile boundary: %v", e)
	}
	f.fail = true
	out.Reset()
	if e := admissionMaintenance(ctx, f, "admission-reconcile", []string{"boot", "--upstreams-stopped"}, strings.NewReader("fixture-private-proof"), &out); e == nil || strings.Contains(e.Error(), "fixture-private-proof") {
		t.Fatal("proof leaked via error")
	}
}
