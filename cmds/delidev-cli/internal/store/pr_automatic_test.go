// SPDX-License-Identifier: Apache-2.0
package store

import (
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
