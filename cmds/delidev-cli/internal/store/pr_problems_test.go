package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func problemObservationFixture() domain.RepositoryQueryResult {
	at := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	no := false
	body := "Original PR"
	item := domain.RepositoryItem{Provider: domain.GitHubCom, Kind: domain.RepositoryPullRequest, IdentitySource: domain.RepositoryPullRequestIdentity, ID: "53", NodeID: "PR_53", Number: "17", Title: "Original title", State: domain.RepositoryItemOpen, CreatedAt: at, UpdatedAt: at, URL: "https://github.com/fixture-owner/repo/pull/17", Body: &body, Draft: &no, Merged: &no, BaseRef: "main", BaseSHA: strings.Repeat("a", 40), HeadRef: "feature", HeadSHA: strings.Repeat("b", 40)}
	entries := []domain.PRFeedback{
		{Kind: domain.PRReviewBody, ID: "71", NodeID: "REVIEW_71", Body: "Approved with observations", NativeState: "APPROVED", PublishedAt: at, ReviewSubmittedAt: &at, URL: item.URL + "#pullrequestreview-71"},
		{Kind: domain.PRConversationComment, ID: "9007199254740993", NodeID: "COMMENT_1", Body: "Original feedback <script>inert</script>", PublishedAt: at, URL: item.URL + "#issuecomment-9007199254740993"},
	}
	for i := range entries {
		entries[i].ContentVersion = entries[i].Version()
	}
	return domain.RepositoryQueryResult{RepositoryID: domain.NewID(), RepositoryRevision: "1", ProfileID: domain.NewID(), GenerationID: domain.NewID(), ObservedAt: at, Identity: domain.GitHubIdentity{ID: "13", NodeID: "U_13", Login: "fixture-user"}, Repository: domain.RemoteRepository{Provider: domain.GitHubCom, ID: "37", NodeID: "R_37", Owner: "fixture-owner", Name: "repo"}, Query: domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryFeedback, Number: "17"}, Items: []domain.RepositoryItem{item}, Feedback: &domain.PullRequestFeedback{BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Entries: entries, Threads: []domain.PRFeedbackThread{}}}
}

func collectProblemFixture(t *testing.T, s *Store, revision uint64, observation domain.RepositoryQueryResult) Record {
	t.Helper()
	var set Record
	_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.collect-problems", observation, func(tx *Tx) (any, error) {
		var err error
		set, _, err = tx.ObservePRFeedback(revision, observation)
		return struct{ ID domain.ID }{set.ID}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func readProblemFixture(t *testing.T, s *Store, set domain.ID) []Record {
	t.Helper()
	var rows []Record
	err := s.Read(notificationOwner(), func(tx *Tx) error {
		var err error
		var more bool
		rows, more, err = tx.ListPRProblems(set, "", 50)
		if more {
			t.Fatal("unexpected fixture page")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestPRProblemsKeepOriginalVersionsAndLocalDismissalAcrossProviderChanges(t *testing.T) {
	s, root := openTest(t)
	observed := problemObservationFixture()
	if err := observed.Validate(); err != nil {
		t.Fatal(err)
	}
	set := collectProblemFixture(t, s, 0, observed)
	rows := readProblemFixture(t, s, set.ID)
	if len(rows) != 2 {
		t.Fatal("incomplete collection")
	}
	comment := rows[1]
	original, _ := Decode[domain.PRProblem](comment)
	requestID := domain.NewID()
	_, err := s.Mutate(notificationOwner(), requestID, "fixture.dismiss", nil, func(tx *Tx) (any, error) {
		saved, err := tx.DismissPRProblem(comment.ID, comment.Revision, original.ContentVersion)
		return struct{ ID domain.ID }{saved.ID}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	set, _ = s.Get(context.Background(), domain.ProblemKind, set.ID)
	observed.Feedback.Entries[0].NativeState = "DISMISSED"
	observed.RepositoryID = domain.NewID() // A local alias cannot duplicate the remote PR.
	set = collectProblemFixture(t, s, set.Revision, observed)
	rows = readProblemFixture(t, s, set.ID)
	review, _ := Decode[domain.PRProblem](rows[0])
	dismissed, _ := Decode[domain.PRProblem](rows[1])
	if review.State != domain.PRProblemUnhandled || review.Feedback.NativeState != "APPROVED" || review.LatestProvider.NativeState != "DISMISSED" {
		t.Fatal("provider dismissal erased unhandled original feedback")
	}
	if dismissed.State != domain.PRProblemDismissed || dismissed.Dismissal.RequestID != requestID || dismissed.Feedback.Body != original.Feedback.Body {
		t.Fatal("local dismissal was erased or conflated")
	}
	edited := observed.ObservedAt.Add(time.Minute)
	observed.ObservedAt = edited
	observed.Feedback.Entries[1].Body = "Edited feedback"
	observed.Feedback.Entries[1].LastEditedAt = &edited
	observed.Feedback.Entries[1].ContentVersion = observed.Feedback.Entries[1].Version()
	set = collectProblemFixture(t, s, set.Revision, observed)
	rows = readProblemFixture(t, s, set.ID)
	if len(rows) != 3 {
		t.Fatal("edited content did not retain a new version")
	}
	prior, _ := Decode[domain.PRProblem](rows[1])
	latest, _ := Decode[domain.PRProblem](rows[2])
	if prior.Current || prior.State != domain.PRProblemDismissed || !latest.Current || latest.State != domain.PRProblemUnhandled || latest.ContentVersion == prior.ContentVersion || latest.Feedback.Body != "Edited feedback" {
		t.Fatal("content-version isolation failed")
	}
	observed.Feedback.Entries = []domain.PRFeedback{}
	set = collectProblemFixture(t, s, set.Revision, observed)
	s.Close()
	reopened, err := Open(notificationOwner(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows = readProblemFixture(t, reopened, set.ID)
	if len(rows) != 3 {
		t.Fatal("disappearance or restart erased evidence")
	}
	for _, row := range rows {
		value, err := Decode[domain.PRProblem](row)
		if err != nil || value.Current {
			t.Fatal("absent feedback became current", err)
		}
	}
	value, _ := Decode[domain.PRProblem](rows[2])
	if value.State != domain.PRProblemUnhandled {
		t.Fatal("provider disappearance resolved unhandled feedback")
	}
}

func TestPRProblemsRejectStaleCollectorsAndVersionDismissalAtomically(t *testing.T) {
	s, _ := openTest(t)
	observed := problemObservationFixture()
	set := collectProblemFixture(t, s, 0, observed)
	row := readProblemFixture(t, s, set.ID)[0]
	original, _ := Decode[domain.PRProblem](row)
	for _, mutation := range []func(*Tx) error{
		func(tx *Tx) error { _, _, err := tx.ObservePRFeedback(0, observed); return err },
		func(tx *Tx) error {
			_, err := tx.DismissPRProblem(row.ID, row.Revision+1, original.ContentVersion)
			return err
		},
		func(tx *Tx) error {
			_, err := tx.DismissPRProblem(row.ID, row.Revision, strings.Repeat("0", 64))
			return err
		},
	} {
		_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.stale-problem", nil, func(tx *Tx) (any, error) { return nil, mutation(tx) })
		assertCode(t, err, domain.Conflict)
	}
	current, _ := s.Get(context.Background(), domain.ProblemKind, set.ID)
	if current.Revision != set.Revision {
		t.Fatal("rejected mutation changed shared epoch")
	}
	observed.Feedback.Entries[0].Body = "Tampered without a content version"
	_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.invalid-problem", nil, func(tx *Tx) (any, error) { _, _, err := tx.ObservePRFeedback(set.Revision, observed); return nil, err })
	if err == nil {
		t.Fatal("unvalidated content accepted")
	}
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()})
	err = s.Read(worker, func(tx *Tx) error { _, _, err := tx.ListPRProblems(set.ID, "", 50); return err })
	assertCode(t, err, domain.PermissionDenied)
	rows := readProblemFixture(t, s, set.ID)
	value, _ := Decode[domain.PRProblem](rows[0])
	if value.State != domain.PRProblemUnhandled || value.Feedback.Body != original.Feedback.Body {
		t.Fatal("failure changed retained evidence")
	}
}

func TestPRProblemMigrationPreservesV17BackupAndRejectsUnknownLegacyOwnership(t *testing.T) {
	for _, mode := range []string{"empty", "legacy", "collision"} {
		t.Run(mode, func(t *testing.T) {
			s, root := openTest(t)
			if mode != "collision" {
				if _, err := s.db.Exec(dropPRProblemFixtureSchema); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "legacy" {
				_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.legacy-problem", nil, func(tx *Tx) (any, error) {
					return tx.Put(domain.ProblemKind, domain.NewID(), 0, "", "", map[string]string{"unknown": "preserve"})
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec("PRAGMA user_version=17"); err != nil {
				t.Fatal(err)
			}
			s.Close()
			migrated, err := Open(notificationOwner(), root)
			if mode == "empty" {
				if err != nil {
					t.Fatal(err)
				}
				migrated.Close()
			} else if err == nil {
				migrated.Close()
				t.Fatal("legacy ownership overwritten")
			}
			backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
			if err != nil || len(backups) != 1 {
				t.Fatal("missing pre-migration backup", err)
			}
			for _, path := range []string{backups[0], filepath.Join(root, "state.sqlite")} {
				db, err := sql.Open("sqlite", databaseURI(path, true))
				if err != nil {
					t.Fatal(err)
				}
				var version int
				err = db.QueryRow("PRAGMA user_version").Scan(&version)
				db.Close()
				want := 17
				if path != backups[0] && mode == "empty" {
					want = SchemaVersion
				}
				if err != nil || version != want {
					t.Fatal("migration changed original or failed rollback", version, err)
				}
			}
		})
	}
}

func TestPRProblemPagesRetainWholeEscapedBodies(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	observation := problemObservationFixture()
	for i := range observation.Feedback.Entries {
		observation.Feedback.Entries[i].Body = strings.Repeat("\x01", 64<<10)
		observation.Feedback.Entries[i].ContentVersion = observation.Feedback.Entries[i].Version()
	}
	set := collectProblemFixture(t, s, 0, observation)
	at := observation.ObservedAt.Add(time.Second)
	for i := range observation.Feedback.Entries {
		observation.Feedback.Entries[i].LastEditedAt = &at
		observation.Feedback.Entries[i].ContentVersion = observation.Feedback.Entries[i].Version()
	}
	set = collectProblemFixture(t, s, set.Revision, observation)
	err := s.Read(notificationOwner(), func(tx *Tx) error {
		first, more, err := tx.ListPRProblems(set.ID, "", 50)
		if err != nil {
			return err
		}
		if len(first) != 2 || !more {
			t.Fatal("escaped document byte bound did not split page", len(first), more)
		}
		second, more, err := tx.ListPRProblems(set.ID, first[len(first)-1].ID, 50)
		if err != nil {
			return err
		}
		if len(second) != 2 || more {
			t.Fatal("lost retained versions", len(second), more)
		}
		for _, row := range append(first, second...) {
			v, err := Decode[domain.PRProblem](row)
			if err != nil || len(v.Feedback.Body) != 64<<10 {
				t.Fatal("body truncated", err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPRProblemRetainedQuotaRollsBackWholeCollection(t *testing.T) {
	s, _ := openTest(t)
	defer s.Close()
	observation := problemObservationFixture()
	empty := observation
	empty.Feedback = &domain.PullRequestFeedback{BaseSHA: observation.Items[0].BaseSHA, HeadSHA: observation.Items[0].HeadSHA, Entries: []domain.PRFeedback{}, Threads: []domain.PRFeedbackThread{}}
	set := collectProblemFixture(t, s, 0, empty)
	_, err := s.Mutate(notificationOwner(), domain.NewID(), "fixture.fill-history", nil, func(tx *Tx) (any, error) {
		_, parent, err := tx.GetPRProblemSet(set.ID)
		if err != nil {
			return nil, err
		}
		for n := 1; n < domain.MaxRetainedPRProblems; n++ {
			entry := observation.Feedback.Entries[1]
			entry.ID = strconv.Itoa(n)
			entry.NodeID = "OLD_" + entry.ID
			entry.URL = observation.Items[0].URL + "#issuecomment-" + entry.ID
			entry.ContentVersion = entry.Version()
			provider, _ := domain.FeedbackProviderState(entry, nil)
			value := domain.PRProblem{Version: 1, Type: domain.PRProblemEvidenceRecord, SetID: set.ID, Kind: domain.PRFeedbackProblem, Target: parent.Target, Observation: *parent.Feedback, ContentVersion: entry.ContentVersion, Feedback: &entry, OriginalProvider: &provider, LatestProvider: &provider, State: domain.PRProblemUnhandled}
			if _, err := tx.putPRProblem(domain.NewID(), 0, value); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(notificationOwner(), domain.NewID(), "fixture.quota-collect", nil, func(tx *Tx) (any, error) {
		_, _, err := tx.ObservePRFeedback(set.Revision, observation)
		return nil, err
	})
	if domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("quota ignored", err)
	}
	err = s.Read(notificationOwner(), func(tx *Tx) error {
		current, _, err := tx.GetPRProblemSet(set.ID)
		if err != nil {
			return err
		}
		var count int
		if err = tx.tx.QueryRow("SELECT COUNT(*) FROM pr_problem_records WHERE set_id=?", set.ID).Scan(&count); err != nil {
			return err
		}
		if count != domain.MaxRetainedPRProblems-1 || current.Revision != set.Revision {
			t.Fatal("partial inventory committed at quota", count, current.Revision)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
