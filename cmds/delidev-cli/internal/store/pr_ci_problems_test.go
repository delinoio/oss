package store

import (
	"context"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func ciStoreObservationFixture() domain.RepositoryQueryResult {
	observed := problemObservationFixture()
	observed.Query.Operation = domain.RepositoryCI
	observed.Feedback = nil
	item := observed.Items[0]
	no := false
	observed.Items[0].Mergeable = &no
	conclusion := "FAILURE"
	app := "37"
	rules := []domain.ActiveRepositoryRule{{Type: "required_status_checks", RulesetID: "71", SourceKind: domain.RulesetRepository, NativeSourceKind: "Repository", Source: "fixture-owner/repo", Digest: strings.Repeat("a", 64), RequiredChecks: &domain.RequiredRuleChecks{Checks: []domain.RequiredRuleCheck{{Context: "Required CI", IntegrationID: &app}}}}}
	context := domain.CIContext{Evidence: &domain.CIContextEvidence{SuiteNodeID: "SUITE_1", StartedAt: &observed.ObservedAt, CompletedAt: &observed.ObservedAt}, Kind: domain.CICheckRun, NodeID: "CHECK_1", Name: "Required CI", CommitSHA: item.HeadSHA, Required: true, NativeStatus: "COMPLETED", NativeConclusion: &conclusion, Application: &domain.CheckApplication{ID: app, NodeID: "APP_37", Slug: "fixture-app"}}
	ci := domain.PullRequestCI{Rules: domain.PullRequestRules{BaseRef: item.BaseRef, BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Rules: rules, Digest: domain.ActiveRulesDigest(rules)}, Head: domain.CIRollup{CommitSHA: item.HeadSHA, TotalCount: "1", Contexts: []domain.CIContext{context}}, NativeMergeability: "CONFLICTING"}
	ci.Result = ci.Evaluate(observed.Items[0])
	observed.CI = &ci
	return observed
}

func collectCIStoreFixture(t *testing.T, s *Store, revision uint64, observed domain.RepositoryQueryResult, conflict bool) Record {
	t.Helper()
	var row Record
	_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.ci-conflict", nil, func(tx *Tx) (any, error) {
		var err error
		if conflict {
			row, _, err = tx.ObservePRConflict(revision, observed)
		} else {
			row, _, err = tx.ObservePRCI(revision, observed)
		}
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestPRCIRetainsOriginalProofAndDismissalAcrossCurrentEvaluationChanges(t *testing.T) {
	s, root := openTest(t)
	observed := ciStoreObservationFixture()
	set := collectCIStoreFixture(t, s, 0, observed, false)
	records := readProblemFixture(t, s, set.ID)
	if len(records) != 1 {
		t.Fatal("missing required failure")
	}
	original, _ := Decode[domain.PRProblem](records[0])
	if original.CI == nil {
		t.Fatal("missing original proof link")
	}
	// Exercise the old plain-node index while preserving original proof bytes.
	_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.legacy-ci-index", nil, func(tx *Tx) (any, error) {
		_, err := tx.tx.ExecContext(tx.ctx, "UPDATE pr_problem_records SET native_node=? WHERE id=?", original.NativeNode(), records[0].ID)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.dismiss-ci", nil, func(tx *Tx) (any, error) {
		return tx.DismissPRProblem(records[0].ID, records[0].Revision, original.ContentVersion)
	})
	if err != nil {
		t.Fatal(err)
	}
	set, _ = s.Get(context.Background(), domain.ProblemKind, set.ID)
	// Unknown applicability blocks this kind without handling the original.
	observed.CI.InMergeQueue = true
	observed.CI.Result = observed.CI.Evaluate(observed.Items[0])
	set = collectCIStoreFixture(t, s, set.Revision, observed, false)
	records = readProblemFixture(t, s, set.ID)
	prior, _ := Decode[domain.PRProblem](records[0])
	if prior.Current || prior.State != domain.PRProblemDismissed {
		t.Fatal("unknown cleared local handling or remained current")
	}
	observed.CI.InMergeQueue = false
	observed.CI.Result = observed.CI.Evaluate(observed.Items[0])
	set = collectCIStoreFixture(t, s, set.Revision, observed, false)
	records = readProblemFixture(t, s, set.ID)
	prior, _ = Decode[domain.PRProblem](records[0])
	if !prior.Current || prior.State != domain.PRProblemDismissed || prior.CI.ObservationID != original.CI.ObservationID {
		t.Fatal("unchanged result resurrected or rewrote proof")
	}
	changed := "New failing provider output"
	observed.CI.Head.Contexts[0].Evidence.Text = &changed
	set = collectCIStoreFixture(t, s, set.Revision, observed, false)
	records = readProblemFixture(t, s, set.ID)
	if len(records) != 2 {
		t.Fatal("edited native result did not retain a new version")
	}
	s.Close()
	s, err = Open(notificationOwner(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Read(notificationOwner(), func(tx *Tx) error {
		_, proof, err := tx.GetPRCIObservation(original.CI.ObservationID)
		if err == nil && proof.CI.Head.Contexts[0].Evidence.Text != nil {
			t.Error("original provider output overwritten")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func queueCIStoreObservationFixture() domain.RepositoryQueryResult {
	o := ciStoreObservationFixture()
	ci := o.CI
	entry := domain.CIMergeQueueEntry{NodeID: "ENTRY_E", PullRequestNodeID: o.Items[0].NodeID, PullRequestNumber: o.Items[0].Number, Position: "1", BaseSHA: o.Items[0].BaseSHA, HeadSHA: strings.Repeat("f", 40), State: "UNMERGEABLE"}
	run := ci.Head.Contexts[0]
	run.NodeID, run.CommitSHA = "QUEUE_CHECK", entry.HeadSHA
	ci.InMergeQueue = true
	ci.MergeQueue = &domain.CIMergeQueue{NodeID: "QUEUE_Q", RepositoryNodeID: o.Repository.NodeID, Strategy: domain.CIQueueAllGreen, Entry: entry, Entries: []domain.CIMergeQueueEntry{entry}, TotalCount: "1", Rollup: &domain.CIRollup{CommitSHA: entry.HeadSHA, TotalCount: "1", Contexts: []domain.CIContext{run}}}
	ci.Result = ci.Evaluate(o.Items[0])
	return o
}

func TestQueueCIFailureRetainsOriginalEntryAndLosesCurrentAuthorityOnRemoval(t *testing.T) {
	s, root := openTest(t)
	o := queueCIStoreObservationFixture()
	ci := o.CI
	set := collectCIStoreFixture(t, s, 0, o, false)
	rows := readProblemFixture(t, s, set.ID)
	if len(rows) != 1 {
		t.Fatal("missing queue failure")
	}
	problem, err := Decode[domain.PRProblem](rows[0])
	if err != nil || problem.CI.Source != domain.CIMergeQueueCommit || problem.CI.QueueNodeID != "QUEUE_Q" || problem.CI.QueueEntryNodeID != "ENTRY_E" {
		t.Fatal(problem, err)
	}
	ci.InMergeQueue, ci.MergeQueue = false, nil
	ci.Head.Contexts, ci.Head.TotalCount = []domain.CIContext{}, "0"
	ci.Result = ci.Evaluate(o.Items[0])
	collectCIStoreFixture(t, s, set.Revision, o, false)
	rows = readProblemFixture(t, s, set.ID)
	retained, err := Decode[domain.PRProblem](rows[0])
	if err != nil || retained.Current || retained.State != domain.PRProblemUnhandled || retained.CI.ObservationID != problem.CI.ObservationID {
		t.Fatal("removed queue failure retained current authority", retained, err)
	}
	s.Close()
	s, err = Open(notificationOwner(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Read(notificationOwner(), func(tx *Tx) error {
		_, proof, err := tx.GetPRCIObservation(problem.CI.ObservationID)
		if err == nil && !problem.CI.Matches(proof) {
			t.Error("historical queue proof changed across restart")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestQueueCIFailureSeparatesReplacementIdentityWithReusedNativeResult(t *testing.T) {
	for _, mode := range []string{"entry", "queue", "legacy-entry", "legacy-queue"} {
		t.Run(mode, func(t *testing.T) {
			s, root := openTest(t)
			defer func() { s.Close() }()
			o := queueCIStoreObservationFixture()
			set := collectCIStoreFixture(t, s, 0, o, false)
			rows := readProblemFixture(t, s, set.ID)
			if len(rows) != 1 {
				t.Fatal("missing original queue failure")
			}
			originalRow := rows[0]
			original, err := Decode[domain.PRProblem](originalRow)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "legacy-") {
				_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.legacy-queue-index", nil, func(tx *Tx) (any, error) {
					_, err := tx.tx.ExecContext(tx.ctx, "UPDATE pr_problem_records SET native_node=? WHERE id=?", original.NativeNode(), originalRow.ID)
					return nil, err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.dismiss-queue", nil, func(tx *Tx) (any, error) {
				return tx.DismissPRProblem(originalRow.ID, originalRow.Revision, original.ContentVersion)
			})
			if err != nil {
				t.Fatal(err)
			}
			set, err = s.Get(context.Background(), domain.ProblemKind, set.ID)
			if err != nil {
				t.Fatal(err)
			}
			set = collectCIStoreFixture(t, s, set.Revision, o, false)
			rows = readProblemFixture(t, s, set.ID)
			if len(rows) != 1 || rows[0].ID != originalRow.ID {
				t.Fatal("unchanged entry duplicated original history")
			}
			removed := ciStoreObservationFixture()
			removed.CI.Head.Contexts, removed.CI.Head.TotalCount = []domain.CIContext{}, "0"
			removed.CI.Result = removed.CI.Evaluate(removed.Items[0])
			set = collectCIStoreFixture(t, s, set.Revision, removed, false)
			if strings.HasSuffix(mode, "entry") {
				o.CI.MergeQueue.Entry.NodeID = "ENTRY_REPLACEMENT"
				o.CI.MergeQueue.Entries[0].NodeID = "ENTRY_REPLACEMENT"
			} else {
				o.CI.MergeQueue.NodeID = "QUEUE_REPLACEMENT"
			}
			o.CI.Result = o.CI.Evaluate(o.Items[0])
			set = collectCIStoreFixture(t, s, set.Revision, o, false)
			rows = readProblemFixture(t, s, set.ID)
			if len(rows) != 2 {
				t.Fatal("replacement queue identity reused original failure", len(rows))
			}
			s.Close()
			s, err = Open(notificationOwner(), root)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Read(notificationOwner(), func(tx *Tx) error {
				for _, row := range rows {
					_, value, err := tx.GetPRProblem(row.ID)
					if err != nil {
						return err
					}
					if value.ContentVersion != original.ContentVersion {
						t.Fatal("replacement altered the original native result version")
					}
					if row.ID == originalRow.ID {
						if value.Current || value.State != domain.PRProblemDismissed || value.CI.ObservationID != original.CI.ObservationID || value.CI.QueueNodeID != original.CI.QueueNodeID || value.CI.QueueEntryNodeID != original.CI.QueueEntryNodeID {
							t.Fatal("replacement changed original proof or handling", value)
						}
					} else if !value.Current || value.State != domain.PRProblemUnhandled || value.CI.ObservationID == original.CI.ObservationID || value.CI.QueueNodeID != o.CI.MergeQueue.NodeID || value.CI.QueueEntryNodeID != o.CI.MergeQueue.Entry.NodeID {
						t.Fatal("replacement lacks its own current proof", value)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			collectCIStoreFixture(t, s, set.Revision, o, false)
			if len(readProblemFixture(t, s, set.ID)) != 2 {
				t.Fatal("unchanged replacement duplicated history")
			}
		})
	}
}

func TestPRConflictHistoryIsIndependentOfFeedbackAndCIEvaluation(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	feedback := problemObservationFixture()
	set := collectProblemFixture(t, s, 0, feedback)
	observed := ciStoreObservationFixture()
	observed.RepositoryID = feedback.RepositoryID
	observed.Query.Operation = domain.RepositoryDetail
	observed.CI = nil
	set = collectCIStoreFixture(t, s, set.Revision, observed, true)
	records := readProblemFixture(t, s, set.ID)
	var conflict Record
	for _, r := range records {
		v, _ := Decode[domain.PRProblem](r)
		if v.Kind == domain.PRMergeConflictProblem {
			conflict = r
		}
	}
	if len(records) != 3 || conflict.ID == "" {
		t.Fatal("independent conflict missing or feedback changed")
	}
	value, _ := Decode[domain.PRProblem](conflict)
	_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.dismiss-conflict", nil, func(tx *Tx) (any, error) {
		return tx.DismissPRProblem(conflict.ID, conflict.Revision, value.ContentVersion)
	})
	if err != nil {
		t.Fatal(err)
	}
	set, _ = s.Get(context.Background(), domain.ProblemKind, set.ID)
	observed.Items[0].Mergeable = nil
	set = collectCIStoreFixture(t, s, set.Revision, observed, true)
	no := false
	observed.Items[0].Mergeable = &no
	set = collectCIStoreFixture(t, s, set.Revision, observed, true)
	records = readProblemFixture(t, s, set.ID)
	if len(records) != 3 {
		t.Fatal("unknown reset conflict transition")
	}
	yes := true
	observed.Items[0].Mergeable = &yes
	set = collectCIStoreFixture(t, s, set.Revision, observed, true)
	observed.Items[0].Mergeable = &no
	set = collectCIStoreFixture(t, s, set.Revision, observed, true)
	records = readProblemFixture(t, s, set.ID)
	if len(records) != 4 {
		t.Fatal("new conflict transition lost")
	}
	for _, r := range records {
		v, _ := Decode[domain.PRProblem](r)
		if r.ID == conflict.ID && (v.State != domain.PRProblemDismissed || v.Current) {
			t.Fatal("old conflict handling changed")
		}
		if v.Kind == domain.PRFeedbackProblem && !v.Current {
			t.Fatal("conflict read invalidated independent feedback")
		}
	}
}
