package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	sqlite "modernc.org/sqlite"
)

// tombstoneProbeArmed makes the next fixture_tombstone_probe() evaluation fail
// exactly once, then disarm itself. Registered once for every SQLite connection.
var tombstoneProbeArmed atomic.Bool

func init() {
	sqlite.MustRegisterScalarFunction("fixture_tombstone_probe", 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		if tombstoneProbeArmed.CompareAndSwap(true, false) {
			return nil, errors.New("fixture tombstone probe failure")
		}
		// Never equals a valid UUID, so a healthy probe row never matches.
		return "", nil
	})
}

func TestIsRetryableStorageFailureKeepsDomainOutcomesTerminal(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"internal storage", &domain.Error{Code: domain.Internal, Cause: "sqlite_failure"}, true},
		{"busy", &domain.Error{Code: domain.Unavailable, Cause: "sqlite_busy"}, true},
		{"storage full", &domain.Error{Code: domain.ResourceExhausted, Cause: "sqlite_full"}, true},
		{"filesystem permission", &domain.Error{Code: domain.PermissionDenied, Cause: "filesystem_permission"}, true},
		{"unclassified error", errors.New("boom"), true},
		{"context canceled", context.Canceled, true},
		{"context deadline", context.DeadlineExceeded, true},
		{"used identity", domain.Fail(domain.Conflict, "used", ""), false},
		{"revision mismatch", domain.Fail(domain.Conflict, "changed", ""), false},
		{"deleted target", domain.Fail(domain.NotFound, "missing", ""), false},
		{"validation", domain.Fail(domain.InvalidArgument, "invalid", ""), false},
		{"inactive machine", domain.Fail(domain.PermissionDenied, "disabled", ""), false},
		{"bounded relationships", domain.Fail(domain.ResourceExhausted, "bound", ""), false},
		{"integrity recovery", domain.Fail(domain.RecoveryRequired, "corrupt", ""), false},
	}
	for _, tc := range cases {
		if got := isRetryableStorageFailure(tc.err); got != tc.retryable {
			t.Errorf("%s: retryable=%v, want %v", tc.name, got, tc.retryable)
		}
	}
}

// TestRepositoryFinalValidationStorageFailureRollsBackForRetry injects a read
// failure into the final tombstone query and proves the original child success
// report rolls back, then settles exactly once after storage recovers.
func TestRepositoryFinalValidationStorageFailureRollsBackForRetry(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "state")
	db, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	machine, id := domain.NewID(), domain.NewID()
	_, err = db.Mutate(ctx, domain.NewID(), "fixture.machine", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64", Version: "0.1.0", WorkerCapabilities: []domain.WorkerCapability{domain.RepositoryInspectionMetadataV1}})
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := domain.Repository{RemoteURL: "https://github.com/fixture/repo.git", Name: "repo", PreferredRemote: "origin", Checkouts: []domain.Checkout{{MachineID: machine, Path: "/tmp/repo"}}}
	raw, _ := json.Marshal(repository)
	accepted, err := SaveConfiguration(ctx, db, ConfigurationMutation{RequestID: domain.NewID(), ID: id, Kind: domain.RepositoryKind, Document: raw})
	if err != nil {
		t.Fatal(err)
	}
	var parent store.Record
	if err = domain.Decode(accepted.Data, &parent); err != nil {
		t.Fatal(err)
	}
	var children []store.Record
	if err = db.Read(ctx, func(tx *store.Tx) error {
		var err error
		children, err = tx.Jobs("", parent.ID, "", "", 10)
		return err
	}); err != nil || len(children) != 1 {
		t.Fatal(children, err)
	}
	report := func(armed bool) error {
		_, err := db.Mutate(ctx, domain.NewID(), "fixture.original.inspection", nil, func(tx *store.Tx) (any, error) {
			current, err := tx.Get(domain.JobKind, children[0].ID)
			if err != nil {
				return nil, err
			}
			child, err := store.Decode[domain.Job](current)
			if err != nil {
				return nil, err
			}
			child.State = domain.JobSucceeded
			now := time.Now().UTC()
			child.FinishedAt = &now
			child.Output, _ = json.Marshal(workspace.Inspection{Root: "/tmp/canonical", Name: "repo", Remotes: []string{"origin"}, DefaultRefs: map[string]string{}})
			if _, err = tx.PutJob(children[0].ID, current.Revision, "", "", child); err != nil {
				return nil, err
			}
			// Arm after the child write so the one-shot fault lands on the
			// final tombstone query inside finishRepositorySave.
			tombstoneProbeArmed.Store(armed)
			return nil, finishRepositorySave(tx, parent.ID)
		})
		tombstoneProbeArmed.Store(false)
		return err
	}
	assertUnsettled := func() {
		t.Helper()
		parentRow, err := db.Get(ctx, domain.JobKind, parent.ID)
		if err != nil {
			t.Fatal(err)
		}
		job, err := store.Decode[domain.Job](parentRow)
		if err != nil || job.State != domain.JobQueued || job.Problem != nil || parentRow.Revision != parent.Revision {
			t.Fatal("parent state committed despite storage failure", job, err)
		}
		childRow, err := db.Get(ctx, domain.JobKind, children[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if childRow.Revision != children[0].Revision {
			t.Fatal("child success committed despite storage failure", childRow.Revision)
		}
		if _, err = db.Get(ctx, domain.RepositoryKind, id); domain.SafeError(err).Code != domain.NotFound {
			t.Fatal("repository published despite storage failure", err)
		}
	}

	fixture, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	// Wrap the tombstone table in a view whose extra probe row can fail once.
	// legacy_alter_table keeps the rename from rewriting unrelated references.
	for _, statement := range []string{
		`PRAGMA legacy_alter_table=ON`,
		`ALTER TABLE tombstones RENAME TO tombstones_fixture_real`,
		`CREATE VIEW tombstones AS SELECT id,kind,created_at FROM tombstones_fixture_real UNION ALL SELECT fixture_tombstone_probe(),'fixture',0`,
	} {
		if _, err = fixture.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	failure := report(true)
	if failure == nil {
		t.Fatal("storage failure was settled as a terminal result")
	}
	if code := domain.SafeError(failure).Code; code == domain.Conflict || code == domain.NotFound || code == domain.InvalidArgument {
		t.Fatal("storage failure was reported as a terminal domain code", code)
	}
	assertUnsettled()

	if err = report(false); err != nil {
		t.Fatal("healthy retry did not settle", err)
	}
	parentRow, err := db.Get(ctx, domain.JobKind, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](parentRow)
	if err != nil || job.State != domain.JobSucceeded || job.Problem != nil {
		t.Fatal("parent not succeeded after healthy retry", job, err)
	}
	var output repositorySaveOutput
	if err = domain.Decode(job.Output, &output); err != nil || output.ID != id || output.Revision != 1 {
		t.Fatal("repository publication output changed", output, err)
	}
	row, err := db.Get(ctx, domain.RepositoryKind, id)
	if err != nil || row.Revision != 1 {
		t.Fatal("repository not published exactly once", row.Revision, err)
	}
}
