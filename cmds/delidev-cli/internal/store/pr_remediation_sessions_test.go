package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestInitialExecutionPreviewLeavesRoutingUntouchedAndClaimRechecks(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	sessionID, inputID := f.session(t, domain.DispatchReady)
	session := readExecutionSession(t, s, sessionID)
	var preview InitialExecutionPreview
	for range 2 {
		if err := s.Read(context.Background(), func(tx *Tx) error {
			var err error
			preview, err = tx.PreviewInitialExecution(session)
			if err != nil {
				return err
			}
			routing, _, err := tx.Routing(f.agent)
			if routing.ID != "" || preview.AccountID != f.accounts[0] {
				t.Fatal("preview consumed routing")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(session, readExecutionSession(t, s, sessionID)) {
		t.Fatal("preview changed session")
	}
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.disconnect-preview-account", nil, func(tx *Tx) (any, error) {
		r, account, err := decodeEntity[domain.Account](tx, domain.AccountKind, preview.AccountID)
		if err != nil {
			return nil, err
		}
		account.Enabled = false
		return tx.Put(r.Kind, r.ID, r.Revision, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.claim(domain.NewID(), sessionID, inputID); err != nil {
		t.Fatal(err)
	}
	claimed := readExecutionSession(t, s, sessionID)
	if claimed.InitialExecution.InitialAccountID != f.accounts[1] {
		t.Fatal("claim borrowed stale preview selection")
	}
}

func TestPRRemediationSessionsUseOriginalLinksAndActivityWithoutResuming(t *testing.T) {
	s, _ := openTest(t)
	f := newRemediationStoreFixture(t, s)
	set, err := Decode[domain.PRProblemSet](f.set)
	if err != nil {
		t.Fatal(err)
	}
	target := set.Target
	baseTime := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Hour)
	ids := []domain.ID{}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.linked-pr-sessions", nil, func(tx *Tx) (any, error) {
		for i := range 11 {
			id := domain.NewID()
			ids = append(ids, id)
			tx.now = baseTime.Add(time.Duration(i) * time.Second)
			value := domain.Session{AgentID: f.exec.agent, MachineID: f.exec.machine, ProjectID: f.project, Workspace: domain.Worktree, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchReady, Preparation: &domain.SessionPreparation{JobID: domain.NewID(), State: domain.PreparationReady}}
			link, project := target, f.project
			switch i {
			case 0: // An older UUID with recent session activity must win.
				tx.now = baseTime.Add(3 * time.Second)
			case 2: // Same millisecond as the preceding session uses UUID order.
				tx.now = baseTime.Add(time.Second)
			case 3:
				value.Dispatch = domain.DispatchPaused
			case 4:
				value.Archive = domain.ArchivePending
			case 5:
				value.Recovery = domain.NeedsRecovery
			case 6:
				value.Workspace = domain.GeneralChat
			case 7:
				link.PullRequestNodeID = "PR_foreign"
			case 8:
				link.RepositoryID = domain.NewID()
			case 9:
				project = domain.NewID()
				value.ProjectID = project
			case 10:
				value.Preparation.State = domain.PreparationPending
			}
			if _, err := tx.Put(domain.SessionKind, id, 0, id, project, value); err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.PullRequestKind, domain.NewID(), 0, id, project, link); err != nil {
				return nil, err
			}
			if i == 0 { // Duplicate corrupted links cannot multiply candidates.
				if _, err := tx.Put(domain.PullRequestKind, domain.NewID(), 0, id, project, link); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(notificationOwner(), func(tx *Tx) error {
		page, more, err := tx.PRRemediationSessions(f.project, target, PRRemediationSessionPosition{}, 2)
		if err != nil || !more || len(page) != 2 || page[0].ID != ids[0] || page[1].ID != ids[2] {
			t.Fatal("most recent original linked sessions", err)
		}
		last := page[1]
		page, more, err = tx.PRRemediationSessions(f.project, target, PRRemediationSessionPosition{UpdatedAt: last.UpdatedAt, ID: last.ID}, 2)
		if err != nil || more || len(page) != 1 || page[0].ID != ids[1] {
			t.Fatal("activity pagination changed order or included excluded state", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice})
	if err := s.Read(worker, func(tx *Tx) error {
		_, _, err := tx.PRRemediationSessions(f.project, target, PRRemediationSessionPosition{}, 2)
		return err
	}); domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal("Worker selected remediation sessions", err)
	}
	paused := readExecutionSession(t, s, ids[3])
	if paused.Dispatch != domain.DispatchPaused || paused.ActiveExecutionID != "" || paused.PendingInputs != 0 {
		t.Fatal("candidate scan resumed or enqueued input")
	}
}
