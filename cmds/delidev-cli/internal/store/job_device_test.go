package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestJobDeviceBindsOnlyAtOriginalClaim(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	id, device := domain.NewID(), domain.NewID()
	job := domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobQueued, MachineID: domain.NewID(), Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()}
	var current Record
	put := func(next domain.Job) error {
		_, err := s.Mutate(ctx, domain.NewID(), "fixture.device-claim", nil, func(tx *Tx) (any, error) {
			r, err := tx.PutJob(id, current.Revision, "", "", next)
			if err == nil {
				current = r
			}
			return nil, err
		})
		return err
	}
	if err := put(job); err != nil {
		t.Fatal(err)
	}
	job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, domain.NewID(), device
	if err := put(job); err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []domain.ID{"", domain.NewID()} {
		changed := job
		changed.State, changed.AssignedDeviceID = domain.JobUncertain, wrong
		if err := put(changed); err == nil {
			t.Fatal("changed original Worker device")
		}
	}
	job.State = domain.JobUncertain
	if err := put(job); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		original, err := tx.JobAssignment(id)
		if err != nil {
			return err
		}
		claim, err := Decode[domain.Job](original)
		if err != nil || claim.State != domain.JobClaimed || claim.AssignedDeviceID != device {
			t.Fatal("assignment lost its original paired device", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
