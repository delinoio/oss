// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func queuedRetryPreclaimFixture(t *testing.T) (*continuationFixture, domain.SidechatRetry) {
	t.Helper()
	_, child := completedRetrySidechatFixture(t)
	// Stop observation before accepting the retry, so no claim races the vector.
	child.service.connectionsMu.Lock()
	stream := child.service.workerStreams[domain.ID(child.machine.Id)]
	child.service.connectionsMu.Unlock()
	stream.Cancel()
	<-stream.Done
	child.workerStream.Close()
	request := retryRequestFixture(t, child)
	if _, err := sessionClient(child.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(child.identity, request)); err != nil {
		t.Fatal(err)
	}
	view := retryViewFixture(t, child, request.Mutation.RequestId)
	return child, view.Generations[len(view.Generations)-1]
}

func TestSidechatQuestionRetryPreclaimRejectionReleasesExactOwner(t *testing.T) {
	for _, vector := range []string{"worker-instance", "worker-capability"} {
		t.Run(vector, func(t *testing.T) {
			child, generation := queuedRetryPreclaimFixture(t)
			ctx := context.Background()
			var capturedInstance domain.ID
			var capabilities []domain.WorkerCapability
			_, err := child.service.Store.Mutate(ctx, domain.NewID(), "test.preclaim-rejection", nil, func(tx *store.Tx) (any, error) {
				r, err := tx.Get(domain.JobKind, generation.ForkJobID)
				if err != nil {
					return nil, err
				}
				job, err := store.Decode[domain.Job](r)
				if err != nil {
					return nil, err
				}
				var input domain.ForkJobInput
				if err := domain.Decode(job.Input, &input); err != nil {
					return nil, err
				}
				if job.State != domain.JobQueued || job.InstanceID != "" || job.AssignedDeviceID != "" {
					t.Fatal("fixture acquired native authority")
				}
				capturedInstance = input.Retry.WorkerInstanceID
				if vector == "worker-instance" {
					if err := tx.SetWorkerInstance(job.MachineID, domain.NewID(), time.Now().UTC()); err != nil {
						return nil, err
					}
				} else {
					mr, machine, err := activeMachine(tx, job.MachineID)
					if err != nil {
						return nil, err
					}
					capabilities = append(capabilities, machine.WorkerCapabilities...)
					machine.WorkerCapabilities = nil
					if _, err := tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine); err != nil {
						return nil, err
					}
				}
				problem := validateForkAuthority(tx, input)
				if problem == nil {
					t.Fatal("drift accepted before claim")
				}
				return rejectUnclaimedFork(tx, r, job, input, domain.SafeError(problem))
			})
			if err != nil {
				t.Fatal(err)
			}
			cr := child.refresh(t)
			session, err := store.Decode[domain.Session](cr)
			if err != nil {
				t.Fatal(err)
			}
			if session.SidechatActiveRetry != "" || len(session.SidechatRetries) != 1 || session.SidechatRetries[0].ID != generation.ID || session.SidechatRetries[0].ExecutionJobID != "" {
				t.Fatal("original generation was lost or remained active")
			}
			r, err := child.service.Store.Get(ctx, domain.JobKind, generation.ForkJobID)
			if err != nil {
				t.Fatal(err)
			}
			job, err := store.Decode[domain.Job](r)
			if err != nil {
				t.Fatal(err)
			}
			if job.State != domain.JobFailed || job.Problem == nil || job.FinishedAt == nil || job.InstanceID != "" || job.AssignedDeviceID != "" {
				t.Fatal("original job did not fail without claim")
			}
			_, err = child.service.Store.Mutate(ctx, domain.NewID(), "test.restore-preclaim-authority", nil, func(tx *store.Tx) (any, error) {
				if vector == "worker-instance" {
					return nil, tx.SetWorkerInstance(job.MachineID, capturedInstance, time.Now().UTC())
				}
				mr, machine, err := activeMachine(tx, job.MachineID)
				if err != nil {
					return nil, err
				}
				machine.WorkerCapabilities = capabilities
				return tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
			})
			if err != nil {
				t.Fatal(err)
			}
			request := retryRequestFixture(t, child)
			if _, err := sessionClient(child.accountFixture).RetrySidechatQuestion(ctx, ownerRequest(child.identity, request)); err != nil {
				t.Fatal("fresh explicit retry blocked", err)
			}
			view := retryViewFixture(t, child, request.Mutation.RequestId)
			if len(view.Generations) != 2 || view.Generations[0].ID != generation.ID || view.CurrentAnswer != child.input.ExecutionID {
				t.Fatal("retry history or previous answer changed")
			}
		})
	}
}

func TestSidechatQuestionRetryPreclaimRejectionPreservesPossibleNativeOwners(t *testing.T) {
	for _, state := range []domain.JobState{domain.JobClaimed, domain.JobUncertain} {
		t.Run(string(state), func(t *testing.T) {
			child, generation := queuedRetryPreclaimFixture(t)
			_, err := child.service.Store.Mutate(context.Background(), domain.NewID(), "test.possible-native-owner", nil, func(tx *store.Tx) (any, error) {
				r, err := tx.Get(domain.JobKind, generation.ForkJobID)
				if err != nil {
					return nil, err
				}
				job, err := store.Decode[domain.Job](r)
				if err != nil {
					return nil, err
				}
				var input domain.ForkJobInput
				if err := domain.Decode(job.Input, &input); err != nil {
					return nil, err
				}
				job.State, job.InstanceID = domain.JobClaimed, input.Retry.WorkerInstanceID
				job.AssignedDeviceID = input.Retry.WorkerDeviceID
				r, err = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
				if err != nil {
					return nil, err
				}
				if state == domain.JobUncertain {
					job.State = state
					job.Problem = domain.Fail(domain.RecoveryRequired, "Unknown original outcome.", "Preserve cleanup ownership.")
					r, err = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
					if err != nil {
						return nil, err
					}
				}
				return rejectUnclaimedFork(tx, r, job, input, retryConflict())
			})
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](child.refresh(t))
			if err != nil {
				t.Fatal(err)
			}
			if session.SidechatActiveRetry != generation.ID {
				t.Fatal("possible original runtime lost its fence")
			}
			r, err := child.service.Store.Get(context.Background(), domain.JobKind, generation.ForkJobID)
			if err != nil {
				t.Fatal(err)
			}
			job, err := store.Decode[domain.Job](r)
			if err != nil || job.State != state {
				t.Fatal("possible original runtime was settled as never claimed", err)
			}
		})
	}
}

func TestSidechatQuestionRetryPreclaimSettlementRollsBackWithJobFailure(t *testing.T) {
	child, generation := queuedRetryPreclaimFixture(t)
	ctx := context.Background()
	_, err := child.service.Store.Mutate(ctx, domain.NewID(), "test.atomic-preclaim-failure", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, generation.ForkJobID)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		var input domain.ForkJobInput
		if err := domain.Decode(job.Input, &input); err != nil {
			return nil, err
		}
		// Force the final job validation to fail after the matching session update.
		job.MachineID = ""
		return rejectUnclaimedFork(tx, r, job, input, retryConflict())
	})
	if err == nil {
		t.Fatal("invalid final job was accepted")
	}
	session, err := store.Decode[domain.Session](child.refresh(t))
	if err != nil {
		t.Fatal(err)
	}
	if session.SidechatActiveRetry != generation.ID {
		t.Fatal("job failure committed a partial owner release")
	}
	r, err := child.service.Store.Get(ctx, domain.JobKind, generation.ForkJobID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](r)
	if err != nil || job.State != domain.JobQueued || job.FinishedAt != nil || job.Problem != nil {
		t.Fatal("failed transaction modified the original job", err)
	}
}
