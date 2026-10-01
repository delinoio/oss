// SPDX-License-Identifier: Apache-2.0
package store

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

// The source control can change after every outside-lock preflight. The final
// store gate must roll back the ordinary input/account claim and attempt count.
func TestAutomaticPRSourceControlsWinAtExecutionCommit(t *testing.T) {
	for _, change := range []string{"pause", "archive", "unlink", "link-revision"} {
		t.Run(change, func(t *testing.T) {
			s, _ := openTest(t)
			f := newRemediationStoreFixture(t, s)
			f.observation.Items[0].HeadRepository = &domain.PRHeadRepositoryObservation{State: domain.PRHeadRepositoryAvailable, Repository: &f.observation.Repository}
			target, err := domain.NewPRGitTarget(f.observation)
			if err != nil {
				t.Fatal(err)
			}
			a, err := f.reserve(t, domain.PRRemediationAutomatic)
			if err != nil {
				t.Fatal(err)
			}
			sourceID, _ := f.exec.session(t, domain.DispatchReady)
			var source Record
			_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.automatic-source", nil, func(tx *Tx) (any, error) {
				r, err := tx.Get(domain.SessionKind, sourceID)
				if err != nil {
					return nil, err
				}
				v, err := Decode[domain.Session](r)
				if err != nil {
					return nil, err
				}
				v.ProjectID = f.project
				if _, err := tx.Put(r.Kind, r.ID, r.Revision, r.ID, f.project, v); err != nil {
					return nil, err
				}
				source, err = tx.Put(domain.PullRequestKind, domain.NewID(), 0, sourceID, f.project, target.Target)
				if err != nil {
					return nil, err
				}
				a, err = tx.BindPRFixTarget(a.ID, a.Revision, f.project, target)
				if err != nil {
					return nil, err
				}
				a, err = tx.BindAutomaticPRSource(a.ID, a.Revision, source)
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			a = f.bind(t, a)
			_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.automatic-source-control", nil, func(tx *Tx) (any, error) {
				if change == "unlink" {
					return nil, tx.Delete(source.Kind, source.ID, source.Revision)
				}
				if change == "link-revision" {
					link, _ := Decode[domain.SessionPullRequest](source)
					link.Title = "Changed association"
					return tx.Put(source.Kind, source.ID, source.Revision, source.SessionID, source.ProjectID, link)
				}
				r, err := tx.Get(domain.SessionKind, sourceID)
				if err != nil {
					return nil, err
				}
				v, err := Decode[domain.Session](r)
				if err != nil {
					return nil, err
				}
				if change == "pause" {
					v.Dispatch = domain.DispatchPaused
				} else {
					v.Archive = domain.Archived
				}
				return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, v)
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.start(t, a, domain.NewID(), f.observation); err == nil {
				t.Fatal("source control lost at execution commit")
			}
			if f.chain(t).AutomaticAttempts != 0 || f.chain(t).ActiveAttemptID != a.ID {
				t.Fatal("changed source charged or released attempt")
			}
			err = s.Read(notificationOwner(), func(tx *Tx) error {
				_, original, err := tx.GetPRRemediationAttempt(a.ID)
				if err != nil {
					return err
				}
				r, err := tx.Get(domain.QueueKind, original.InputID)
				if err != nil {
					return err
				}
				queued, err := Decode[domain.QueuedInput](r)
				if err == nil && queued.Delivery != domain.InputQueued {
					t.Fatal("ordinary input claim escaped rollback")
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Prove replacement from the original execution session through retained
// assignment/failed-completion/unchanged-push fixtures, not a generic pause.
func TestAutomaticPRFailedDiscoverySessionRetainsOnlyProvedReplacementAuthority(t *testing.T) {
	s, _ := openTest(t)
	f := newRemediationStoreFixture(t, s)
	f.outcome = domain.ExecutionFailed
	f.observation.Items[0].HeadRepository = &domain.PRHeadRepositoryObservation{State: domain.PRHeadRepositoryAvailable, Repository: &f.observation.Repository}
	target, err := domain.NewPRGitTarget(f.observation)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.source-write-permission", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.AgentKind, f.exec.agent)
		if err != nil {
			return nil, err
		}
		a, err := Decode[domain.Agent](r)
		if err != nil {
			return nil, err
		}
		a.Options.Permission = domain.PermissionWorkspaceWrite
		return tx.Put(r.Kind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, inputID := f.exec.session(t, domain.DispatchReady)
	a, err := f.reserve(t, domain.PRRemediationAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	var source Record
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.same-source-bind", nil, func(tx *Tx) (any, error) {
		sr, err := tx.Get(domain.SessionKind, sessionID)
		if err != nil {
			return nil, err
		}
		v, err := Decode[domain.Session](sr)
		if err != nil {
			return nil, err
		}
		v.Workspace, v.ProjectID = domain.Worktree, f.project
		if _, err := tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, f.project, v); err != nil {
			return nil, err
		}
		ir, err := tx.Get(domain.QueueKind, inputID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Put(ir.Kind, ir.ID, ir.Revision, sr.ID, f.project, ir.Data); err != nil {
			return nil, err
		}
		source, err = tx.Put(domain.PullRequestKind, domain.NewID(), 0, sessionID, f.project, target.Target)
		if err != nil {
			return nil, err
		}
		a, err = tx.BindPRFixTarget(a.ID, a.Revision, f.project, target)
		if err != nil {
			return nil, err
		}
		a, err = tx.BindAutomaticPRSource(a.ID, a.Revision, source)
		if err != nil {
			return nil, err
		}
		a, err = tx.BindPRRemediation(a.ID, a.Revision, sessionID, inputID)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _, err = f.start(t, a, domain.NewID(), f.observation)
	if err != nil {
		t.Fatal(err)
	}
	a = f.finish(t, a, true, func(j *domain.Job) {
		var input domain.ExecutionJobInput
		var done domain.ExecutionCompletion
		domain.Decode(j.Input, &input)
		domain.Decode(j.Output, &done)
		selection := domain.PRFixExecution{AttemptID: a.ID, Target: target, Strategy: f.policy.ConflictStrategy, Conflict: true}
		input.Remediation = &selection
		done.PRPush = &domain.PRPushProof{Version: 1, AttemptID: a.ID, ExecutionID: done.ExecutionID, SelectionDigest: selection.Digest(), State: domain.PRPushUnchanged, PreviousHead: target.HeadSHA, ResultHead: target.HeadSHA, ObservedAt: *j.FinishedAt}
		j.Input, _ = json.Marshal(input)
		j.Output, _ = json.Marshal(done)
	})
	settled, _ := Decode[domain.PRRemediationAttempt](a)
	if settled.State != domain.PRRemediationFinished {
		t.Fatal("failed completion was not proved")
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.pause-failed-source", nil, func(tx *Tx) (any, error) {
		sr, err := tx.Get(domain.SessionKind, sessionID)
		if err != nil {
			return nil, err
		}
		v, err := Decode[domain.Session](sr)
		if err != nil {
			return nil, err
		}
		v.Dispatch = domain.DispatchPaused
		return tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, v)
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := f.reserve(t, domain.PRRemediationAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.proved-source-replacement", nil, func(tx *Tx) (any, error) {
		next, err = tx.BindPRFixTarget(next.ID, next.Revision, f.project, target)
		if err != nil {
			return nil, err
		}
		next, err = tx.BindAutomaticPRSource(next.ID, next.Revision, source)
		return nil, err
	})
	if err != nil {
		t.Fatal("settled failure blocked replacement", err)
	}
	for _, change := range []string{"unchanged", "stop", "cleanup", "execution", "archive", "recovery"} {
		t.Run(change, func(t *testing.T) {
			err := s.Read(notificationOwner(), func(tx *Tx) error {
				sr, err := tx.Get(domain.SessionKind, sessionID)
				if err != nil {
					return err
				}
				v, err := Decode[domain.Session](sr)
				if err != nil {
					return err
				}
				switch change {
				case "stop":
					v.AutomaticRemediationStopped = true
				case "cleanup":
					v.Execution.CleanupVerified = false
				case "execution":
					v.Execution.ExecutionID = domain.NewID()
				case "archive":
					v.Archive = domain.Archived
				case "recovery":
					v.Recovery = domain.NeedsRecovery
				}
				return tx.RequireAutomaticPRSourceSession(sr, v, target.Target)
			})
			if (err == nil) != (change == "unchanged") {
				t.Fatal("incorrect source proof gate", err)
			}
		})
	}
	if f.chain(t).AutomaticAttempts != 1 {
		t.Fatal("replacement reset lifetime budget")
	}
	next = f.bind(t, next)
	if _, _, err := f.start(t, next, domain.NewID(), f.observation); err != nil {
		t.Fatal("proved paused source blocked the replacement claim", err)
	}
	if f.chain(t).AutomaticAttempts != 2 {
		t.Fatal("replacement did not preserve and charge the lifetime budget")
	}
	sr, err := s.Get(notificationOwner(), domain.SessionKind, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	failed, _ := Decode[domain.Session](sr)
	if failed.Dispatch != domain.DispatchPaused {
		t.Fatal("replacement resumed the old queue")
	}
}
