package store

import (
	"context"
	"database/sql"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"path/filepath"
	"strings"
	"testing"
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
	_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.dismiss-ci", nil, func(tx *Tx) (any, error) {
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

func TestPRProblemV18MigrationPreservesOriginalBytesAndDismissal(t *testing.T) {
	s, root := openTest(t)
	set := collectProblemFixture(t, s, 0, problemObservationFixture())
	rows := readProblemFixture(t, s, set.ID)
	value, _ := Decode[domain.PRProblem](rows[0])
	_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.legacy-dismiss", nil, func(tx *Tx) (any, error) {
		return tx.DismissPRProblem(rows[0].ID, rows[0].Revision, value.ContentVersion)
	})
	if err != nil {
		t.Fatal(err)
	}
	before := readProblemFixture(t, s, set.ID)
	// Recreate the exact v18 typed index around otherwise unchanged resources.
	old := prProblemSchema[strings.Index(prProblemSchema, "CREATE TABLE pr_problem_records"):]
	_, err = s.db.Exec(dropNativeAccountingFixtureSchema + `DROP INDEX provider_preset_unique; DROP TABLE backup_deletions; DROP TABLE pr_remediation_attempts; ALTER TABLE pr_problem_records RENAME TO retained_v19; DROP INDEX pr_problem_current; DROP INDEX pr_problem_history;` + old + `INSERT INTO pr_problem_records(id,set_id,kind,native_node,content_version,current) SELECT id,set_id,kind,native_node,content_version,current FROM retained_v19; DROP TABLE retained_v19; DROP TABLE pr_problem_ci_observations; PRAGMA user_version=18;`)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(notificationOwner(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after := readProblemFixture(t, s, set.ID)
	if len(before) != len(after) {
		t.Fatal("migration lost feedback")
	}
	for i := range before {
		if before[i].ID != after[i].ID || before[i].Revision != after[i].Revision || string(before[i].Data) != string(after[i].Data) {
			t.Fatal("migration rewrote original evidence or local handling")
		}
	}
	backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatal("missing synchronized v18 backup", err)
	}
	db, err := sql.Open("sqlite", databaseURI(backups[0], true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 18 {
		t.Fatal("backup is not original schema", version, err)
	}
}
