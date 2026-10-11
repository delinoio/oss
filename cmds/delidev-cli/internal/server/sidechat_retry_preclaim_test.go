// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func stopRetryWorker(t *testing.T, f *continuationFixture) {
	t.Helper()
	f.service.connectionsMu.Lock()
	stream, exists := f.service.workerStreams[domain.ID(f.machine.Id)]
	f.service.connectionsMu.Unlock()
	if exists {
		stream.Cancel()
		select {
		case <-stream.Done:
		case <-time.After(time.Second):
			t.Fatal("worker stream did not stop")
		}
	}
	f.workerStream.Close()
}

func pendingRetryFixture(t *testing.T) (*continuationFixture, *pb.RetrySidechatQuestionRequest, store.Record, domain.Job) {
	t.Helper()
	_, f := completedRetrySidechatFixture(t)
	stopRetryWorker(t, f)
	request := retryRequestFixture(t, f)
	if _, err := sessionClient(f.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(f.identity, request)); err != nil {
		t.Fatal(err)
	}
	view := retryViewFixture(t, f, request.Mutation.RequestId)
	r, err := f.service.Store.Get(context.Background(), domain.JobKind, view.Generations[0].ForkJobID)
	if err != nil {
		t.Fatal(err)
	}
	j, err := store.Decode[domain.Job](r)
	if err != nil || j.State != domain.JobQueued || r.Revision != 1 {
		t.Fatal("retry was claimed before the fixture boundary", err)
	}
	return f, request, r, j
}

func TestSidechatQuestionRetryPreclaimRejectionAllowsFreshRetry(t *testing.T) {
	for _, changed := range []string{"capability", "instance"} {
		t.Run(changed, func(t *testing.T) {
			f, request, original, _ := pendingRetryFixture(t)
			before, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil {
				t.Fatal(err)
			}
			var capabilities []domain.WorkerCapability
			instance := domain.ID(f.workerInstance)
			_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "test.retry-preclaim-authority", nil, func(tx *store.Tx) (any, error) {
				if changed == "instance" {
					instance = domain.NewID()
					return nil, tx.SetWorkerInstance(domain.ID(f.machine.Id), instance, time.Now().UTC())
				}
				r, err := tx.Get(domain.MachineKind, domain.ID(f.machine.Id))
				if err != nil {
					return nil, err
				}
				m, err := store.Decode[domain.Machine](r)
				if err != nil {
					return nil, err
				}
				capabilities = append(capabilities, m.WorkerCapabilities...)
				m.WorkerCapabilities = nil
				for _, c := range capabilities {
					if c != domain.SidechatQuestionRetryV1 {
						m.WorkerCapabilities = append(m.WorkerCapabilities, c)
					}
				}
				return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := f.workerClient.WatchWork(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkRequest{MachineId: f.machine.Id, InstanceId: string(instance)}))
			if err != nil {
				t.Fatal(err)
			}
			if !stream.Receive() || !stream.Msg().Heartbeat {
				t.Fatal("missing worker heartbeat", stream.Err())
			}
			for {
				r, err := f.service.Store.Get(ctx, domain.JobKind, original.ID)
				if err != nil {
					t.Fatal(err)
				}
				job, err := store.Decode[domain.Job](r)
				if err != nil {
					t.Fatal(err)
				}
				if job.State == domain.JobFailed {
					if job.Problem == nil || job.FinishedAt == nil || job.InstanceID != "" || job.AssignedDeviceID != "" {
						t.Fatal("preclaim failure acquired native authority or lost failure metadata")
					}
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("retry was not rejected before claim", ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			cancel()
			stream.Close()
			_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "test.restore-retry-authority", nil, func(tx *store.Tx) (any, error) {
				if changed == "instance" {
					return nil, tx.SetWorkerInstance(domain.ID(f.machine.Id), domain.ID(f.workerInstance), time.Now().UTC())
				}
				r, err := tx.Get(domain.MachineKind, domain.ID(f.machine.Id))
				if err != nil {
					return nil, err
				}
				m, err := store.Decode[domain.Machine](r)
				if err != nil {
					return nil, err
				}
				m.WorkerCapabilities = capabilities
				return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
			})
			if err != nil {
				t.Fatal(err)
			}
			view := retryViewFixture(t, f, request.Mutation.RequestId)
			after, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil || !view.Eligible || after.SidechatActiveRetry != "" || view.CurrentAnswer != before.SidechatCurrentAnswer || len(view.Generations) != 1 || view.Generations[0].Fork != nil {
				t.Fatal("original answer or generation was changed, or retry remained fenced", err)
			}
			replay, err := sessionClient(f.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(f.identity, request))
			if err != nil || !replay.Msg.Replayed {
				t.Fatal("original admission receipt was lost", err)
			}
			fresh := retryRequestFixture(t, f)
			if _, err := sessionClient(f.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(f.identity, fresh)); err != nil {
				t.Fatal("restored authority did not allow a fresh explicit retry", err)
			}
		})
	}
}

func TestSidechatQuestionRetryPreclaimRejectionRollsBack(t *testing.T) {
	f, request, r, job := pendingRetryFixture(t)
	before := f.refresh(t)
	rollback := errors.New("rollback after both rejection writes")
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "test.retry-preclaim-rollback", nil, func(tx *store.Tx) (any, error) {
		_, released, err := failForkBeforeClaim(tx, r, job, retryConflict())
		if err != nil {
			return nil, err
		}
		if released != domain.ID(request.Mutation.RequestId) {
			t.Fatal("matching never-claimed owner was not released in transaction")
		}
		return nil, rollback
	})
	if err == nil {
		t.Fatal("rollback unexpectedly committed")
	}
	after := f.refresh(t)
	current, err := f.service.Store.Get(context.Background(), domain.JobKind, r.ID)
	if err != nil || after.Revision != before.Revision || current.Revision != r.Revision {
		t.Fatal("rollback changed the child or job", err)
	}
}

func TestSidechatQuestionRetryPreclaimChildWriteRollsBackWithStaleJob(t *testing.T) {
	f, _, original, job := pendingRetryFixture(t)
	before := f.refresh(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "test.retry-job-revision", nil, func(tx *store.Tx) (any, error) {
		return tx.PutJob(original.ID, original.Revision, original.SessionID, original.ProjectID, job)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "test.retry-stale-rejection", nil, func(tx *store.Tx) (any, error) {
		_, _, err := failForkBeforeClaim(tx, original, job, retryConflict())
		return nil, err
	})
	if err == nil || f.refresh(t).Revision != before.Revision {
		t.Fatal("stale job rejection committed the child release", err)
	}
	current, err := f.service.Store.Get(context.Background(), domain.JobKind, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := store.Decode[domain.Job](current)
	if err != nil || current.Revision != original.Revision+1 || unchanged.State != domain.JobQueued {
		t.Fatal("stale rejection changed the current job", err)
	}
}

func TestSidechatQuestionRetryPreclaimRejectionKeepsUnprovedOwners(t *testing.T) {
	for _, invalid := range []string{"claimed", "uncertain", "retained-assignment", "revision", "assigned-device", "output", "duplicate", "foreign", "native-fork"} {
		t.Run(invalid, func(t *testing.T) {
			f, request, r, job := pendingRetryFixture(t)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "test.retry-preclaim-unproved", nil, func(tx *store.Tx) (any, error) {
				switch invalid {
				case "claimed":
					job.State = domain.JobClaimed
				case "uncertain":
					job.State = domain.JobUncertain
				case "retained-assignment":
					claimed := job
					claimed.State, claimed.InstanceID, claimed.AssignedDeviceID = domain.JobClaimed, domain.ID(f.workerInstance), f.workerDevice
					claimedRecord, err := tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, claimed)
					if err != nil {
						return nil, err
					}
					// Simulate a restored mutable envelope; the durable first claim
					// must still prevent treating it as a never-claimed retry.
					r, err = tx.Put(domain.JobKind, r.ID, claimedRecord.Revision, r.SessionID, r.ProjectID, job)
					if err != nil {
						return nil, err
					}
					assigned, err := tx.HasJobAssignment(r.ID)
					if err != nil || !assigned {
						t.Fatal("original claim was not retained", err)
					}
				case "revision":
					r.Revision++
				case "assigned-device":
					job.AssignedDeviceID = domain.NewID()
				case "output":
					job.Output = []byte(`{}`)
				default:
					cr, child, err := sessionRecord(tx, r.SessionID)
					if err != nil {
						return nil, err
					}
					if invalid == "duplicate" {
						child.SidechatRetries = append(child.SidechatRetries, child.SidechatRetries[0])
					} else if invalid == "foreign" {
						child.SidechatActiveRetry = domain.NewID()
					} else {
						child.SidechatRetries[0].Fork = &domain.SessionDeletionFork{JobID: r.ID, RuntimeID: child.SidechatRetries[0].RuntimeID}
					}
					if _, err := tx.Put(domain.SessionKind, cr.ID, cr.Revision, cr.ID, cr.ProjectID, child); err != nil {
						return nil, err
					}
				}
				_, released, err := failForkBeforeClaim(tx, r, job, retryConflict())
				if released != "" {
					t.Fatal("unproved owner was released", invalid)
				}
				return nil, err
			})
			// Rejected writes roll back; successful failures retain the owner.
			_ = err
			current, decodeErr := store.Decode[domain.Session](f.refresh(t))
			if decodeErr != nil || current.SidechatActiveRetry == "" || len(current.SidechatRetries) == 0 || current.SidechatRetries[0].ID != domain.ID(request.Mutation.RequestId) {
				t.Fatal("unproved owner or its history was lost", decodeErr)
			}
		})
	}
}
