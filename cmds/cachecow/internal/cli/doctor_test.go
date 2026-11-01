// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/delinoio/oss/cmds/cachecow/internal/lease"
	"github.com/delinoio/oss/cmds/cachecow/internal/process"
)

// journal writes a scope.json fixture and returns its journal directory.
func journal(t *testing.T, fields map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	fields["id"], fields["directory"] = filepath.Base(dir), dir
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "scope.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// previousIdentity is a version 2 identity that cannot name the current boot
// (Unix) or a running supervisor (Windows: a PID that no process can have).
func previousIdentity() map[string]any {
	fields := map[string]any{"version": 2, "owner_pid": 2147483647, "started": true, "complete": false, "exit_code": 125}
	if runtime.GOOS == "windows" {
		fields["owner_created"] = int64(1)
	} else {
		fields["boot_id"] = "00000000-0000-0000-0000-000000000000"
	}
	return fields
}

func TestDoctorRecoversAndExplainsLeases(t *testing.T) {
	ctx := context.Background()
	registryDir := filepath.Join(t.TempDir(), "leases")
	if err := os.Mkdir(registryDir, 0700); err != nil {
		t.Fatal(err)
	}
	reg, err := lease.Open(ctx, registryDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	root := t.TempDir()
	legacy := journal(t, map[string]any{"version": 1, "owner_pid": 2147483647, "started": true, "complete": false, "exit_code": 125})
	fixtures := map[string]struct {
		ownership string
		reason    process.Recovery
		recovered bool
	}{
		"previous-boot": {journal(t, previousIdentity()), process.RecoveryNativeCompletion, true},
		"complete":      {journal(t, map[string]any{"version": 2, "owner_pid": 1, "started": true, "complete": true, "exit_code": 0}), process.RecoveryJournalComplete, true},
		"legacy":        {legacy, process.RecoveryNoNativeIdentity, false},
		"unreadable":    {filepath.Join(t.TempDir(), "missing"), process.RecoveryJournalUnreadable, false},
	}
	byPath := map[string]string{}
	for name, f := range fixtures {
		session, err := reg.NewSession(f.ownership)
		if err != nil {
			t.Fatal(err)
		}
		cache := filepath.Join(root, name)
		if _, err = session.Acquire(ctx, lease.Resources{Destinations: []string{cache}}, false); err != nil {
			t.Fatal(err)
		}
		byPath[session.ID()] = name
	}
	records, err := reg.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recovered, kept, err := recoverLeases(ctx, reg, records)
	if err != nil {
		t.Fatal(err)
	}
	check := func(got []leaseDiagnosis, want bool) {
		for _, d := range got {
			f := fixtures[byPath[d.ID]]
			if f.recovered != want || d.Reason != f.reason || d.Ownership != f.ownership || len(d.Paths) != 1 {
				t.Errorf("%s: unexpected diagnosis %+v", byPath[d.ID], d)
			}
		}
	}
	check(recovered, true)
	check(kept, false)
	if len(recovered) != 2 || len(kept) != 2 {
		t.Fatalf("recovered=%d kept=%d", len(recovered), len(kept))
	}
	remaining, err := reg.Records(ctx)
	if err != nil || len(remaining) != 2 {
		t.Fatalf("remaining leases: %+v %v", remaining, err)
	}
	for _, v := range remaining {
		if o := fixtures[byPath[v.ID]]; o.recovered {
			t.Errorf("recovered lease %s still registered", byPath[v.ID])
		}
	}
	// The recovered journal is durably complete with the recovery exit code.
	scope, err := process.LoadScope(fixtures["previous-boot"].ownership)
	if err != nil || !scope.Complete || scope.ExitCode != 125 {
		t.Fatalf("journal: %+v %v", scope, err)
	}
	if after, _ := process.LoadScope(legacy); after.Complete {
		t.Fatal("version 1 journal was completed")
	}
}
