package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestSuccessfulTitleReportsRequireBothDurableRelayClaims(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, test := range []struct {
		name              string
		send, http        bool
		wantAuthorization bool
	}{
		{name: "no claims"},
		{name: "send only", send: true},
		{name: "HTTP only", http: true},
		{name: "both claims", send: true, http: true, wantAuthorization: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			jobID := domain.NewID()
			job := domain.Job{
				Type: domain.GenerateSessionTitleJob, State: domain.JobClaimed,
				MachineID: domain.NewID(), InstanceID: domain.NewID(), AssignedDeviceID: domain.NewID(),
				Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC(),
			}
			if _, err := db.Mutate(ctx, domain.NewID(), "test.title-job", jobID, func(tx *store.Tx) (any, error) {
				return tx.PutJob(jobID, 0, "", "", job)
			}); err != nil {
				t.Fatal(err)
			}
			if test.send || test.http {
				if _, err := db.Mutate(ctx, domain.NewID(), "test.title-claims", struct {
					JobID domain.ID
					Send  bool
					HTTP  bool
				}{jobID, test.send, test.http}, func(tx *store.Tx) (any, error) {
					if test.send {
						if _, err := tx.ClaimTitleInference(jobID); err != nil {
							return nil, err
						}
					}
					if test.http {
						if _, err := tx.ClaimTitleHTTPRequest(jobID); err != nil {
							return nil, err
						}
					}
					return nil, nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			err := db.Read(ctx, func(tx *store.Tx) error { return requireSessionTitleRelayProof(tx, jobID) })
			if test.wantAuthorization && err != nil {
				t.Fatalf("both relay claims were rejected: %v", err)
			}
			if !test.wantAuthorization && domain.SafeError(err).Code != domain.PermissionDenied {
				t.Fatalf("missing relay proof error = %v, want permission denied", err)
			}
		})
	}
}
