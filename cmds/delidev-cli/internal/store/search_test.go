package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const dropSearchFixtureSchema = `DROP TRIGGER transcript_search_insert; DROP TRIGGER transcript_search_delete; DROP TRIGGER transcript_search_update; DROP TABLE transcript_fts; DROP TABLE transcript_search; DROP INDEX search_execution_job; DROP INDEX search_epoch; `

type searchFixture struct{ session, message, account, agent, project, execution domain.ID }

func seedSearch(t *testing.T, s *Store, text string, archive domain.ArchiveState) searchFixture {
	t.Helper()
	f := searchFixture{domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()}
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.search", f, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.SessionKind, f.session, 0, f.session, f.project, domain.Session{Name: "Search session", AgentID: f.agent, Archive: archive, Outcome: domain.ExecutionSucceeded}); err != nil {
			return nil, err
		}
		input, _ := json.Marshal(domain.ExecutionJobInput{ExecutionID: f.execution, SessionID: f.session, AccountID: f.account})
		if _, err := tx.Put(domain.JobKind, domain.NewID(), 0, f.session, f.project, domain.Job{Type: domain.ExecuteSessionJob, Input: input}); err != nil {
			return nil, err
		}
		return tx.Put(domain.MessageKind, f.message, 0, f.session, f.project, domain.ExecutionMessage{ExecutionID: f.execution, Role: domain.AssistantMessage, Text: text, State: domain.MessageComplete})
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func searchPage(t *testing.T, s *Store, f SearchFilter) ([]Record, bool, uint64, error) {
	t.Helper()
	if f.Limit == 0 {
		f.Limit = 50
	}
	var rows []Record
	var more bool
	var epoch uint64
	err := s.Read(context.Background(), func(tx *Tx) error { var err error; rows, more, epoch, err = tx.SearchMessages(f); return err })
	return rows, more, epoch, err
}

func TestSearchLiteralUnicodeAndHistoricalAccountFilters(t *testing.T) {
	s, _ := openTest(t)
	f := seedSearch(t, s, `Résumé 검색어 100% _literal_ "quote" [bracket] *star*`, domain.Archived)
	seedSearch(t, s, "different searchable content", domain.NotArchived)
	for _, query := range []string{"résumé", "RÉSUMÉ", "검색", "검색어", "100%", "_literal_", `"quote"`, "[bracket]", "*star*", "é", "%", "_"} {
		rows, more, _, err := searchPage(t, s, SearchFilter{Query: query})
		if err != nil || more || len(rows) != 1 || rows[0].ID != f.message {
			t.Fatalf("literal %q: %v %v %v", query, rows, more, err)
		}
	}
	for _, query := range []string{"missing OR résumé", `" OR *`, "[missing]"} {
		rows, _, _, err := searchPage(t, s, SearchFilter{Query: query})
		if err != nil || len(rows) != 0 {
			t.Fatalf("interpreted query syntax: %q %v", query, err)
		}
	}
	good := domain.SearchSelection{SessionID: f.session, ProjectID: f.project, AgentID: f.agent, AccountID: f.account, Outcome: domain.ExecutionSucceeded, Archive: domain.Archived}
	rows, _, _, err := searchPage(t, s, SearchFilter{Query: "résumé", SearchSelection: good})
	if err != nil || len(rows) != 1 {
		t.Fatal("lost matching filters", err)
	}
	for _, bad := range []domain.SearchSelection{{AccountID: domain.NewID()}, {ProjectID: domain.NewID()}, {AgentID: domain.NewID()}, {SessionID: domain.NewID()}, {Archive: domain.NotArchived}, {Outcome: domain.ExecutionFailed}} {
		rows, _, _, err := searchPage(t, s, SearchFilter{Query: "résumé", SearchSelection: bad})
		if err != nil || len(rows) != 0 {
			t.Fatal("ignored filter", bad, err)
		}
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.account-change", nil, func(tx *Tx) (any, error) {
		r, e := tx.Get(domain.SessionKind, f.session)
		if e != nil {
			return nil, e
		}
		value, _ := Decode[domain.Session](r)
		value.CurrentExecution = &domain.ExecutionSelection{AccountID: domain.NewID()}
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, value)
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, _, _, err = searchPage(t, s, SearchFilter{Query: "résumé", SearchSelection: good})
	if err != nil || len(rows) != 1 {
		t.Fatal("current selection relabeled original account", err)
	}
}

func TestSearchAtomicEditsDeletionRollbackAndPagination(t *testing.T) {
	s, root := openTest(t)
	f := seedSearch(t, s, "common original", domain.NotArchived)
	seedSearch(t, s, "common next", domain.Archived)
	rows, more, epoch, err := searchPage(t, s, SearchFilter{Query: "common", Limit: 1})
	if err != nil || !more || len(rows) != 1 {
		t.Fatal(err)
	}
	after := rows[0].ID
	next, more, _, err := searchPage(t, s, SearchFilter{Query: "common", After: after, Epoch: epoch, Limit: 1})
	if err != nil || more || len(next) != 1 || next[0].ID == after {
		t.Fatal("bad next page", err)
	}
	edit := func(fail bool) error {
		_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.edit", nil, func(tx *Tx) (any, error) {
			r, e := tx.Get(domain.MessageKind, f.message)
			if e != nil {
				return nil, e
			}
			v, _ := Decode[domain.ExecutionMessage](r)
			v.Text = "replacement"
			_, e = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, v)
			if e == nil && fail {
				return nil, domain.Fail(domain.Conflict, "fixture", "fixture")
			}
			return nil, e
		})
		return err
	}
	assertCode(t, edit(true), domain.Conflict)
	rows, _, _, err = searchPage(t, s, SearchFilter{Query: "original"})
	if err != nil || len(rows) != 1 {
		t.Fatal("index escaped rollback", err)
	}
	if err := edit(false); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = searchPage(t, s, SearchFilter{Query: "common", After: after, Epoch: epoch})
	assertCode(t, err, domain.CursorExpired)
	rows, _, _, err = searchPage(t, s, SearchFilter{Query: "original"})
	if err != nil || len(rows) != 0 {
		t.Fatal("retained stale text", err)
	}
	if _, err := s.db.Exec("VACUUM"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, _, _, err = searchPage(t, s, SearchFilter{Query: "replacement"})
	if err != nil || len(rows) != 1 {
		t.Fatal("vacuum/restart lost source mapping", err)
	}
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.delete", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.MessageKind, f.message, 2) })
	if err != nil {
		t.Fatal(err)
	}
	rows, _, _, err = searchPage(t, s, SearchFilter{Query: "replacement"})
	if err != nil || len(rows) != 0 {
		t.Fatal("deleted source remains searchable", err)
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM transcript_search WHERE message_id=?", f.message).Scan(&count); err != nil || count != 0 {
		t.Fatal("derived content survived source deletion", err)
	}
	if _, err = s.db.Exec("INSERT INTO transcript_fts(transcript_fts,rank) VALUES('integrity-check',1)"); err != nil {
		t.Fatal(err)
	}
}

func TestSearchMigrationBackfillsAndBacksUpWithoutRewritingMessages(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "backfill", true: "rollback"}[conflict], func(t *testing.T) {
			s, root := openTest(t)
			f := seedSearch(t, s, "pre-migration transcript", domain.Archived)
			original, err := s.Get(context.Background(), domain.MessageKind, f.message)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(dropSearchFixtureSchema + "PRAGMA user_version=12;"); err != nil {
				t.Fatal(err)
			}
			if conflict {
				if _, err = s.db.Exec("CREATE TABLE transcript_search(conflict TEXT)"); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(context.Background(), root)
			if conflict {
				if err == nil {
					s.Close()
					t.Fatal("migration adopted foreign index")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				rows, _, _, err := searchPage(t, s, SearchFilter{Query: "pre-migration"})
				if err != nil || len(rows) != 1 || string(rows[0].Data) != string(original.Data) || rows[0].Revision != original.Revision {
					t.Fatal("backfill changed original", err)
				}
			}
			backups, err := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
			if err != nil || len(backups) != 1 {
				t.Fatal("missing migration backup", err)
			}
			for _, path := range []string{backups[0], filepath.Join(root, "state.sqlite")} {
				db, err := sql.Open("sqlite", databaseURI(path, true))
				if err != nil {
					t.Fatal(err)
				}
				var version int
				err = db.QueryRow("PRAGMA user_version").Scan(&version)
				db.Close()
				want := 12
				if path != backups[0] && !conflict {
					want = SchemaVersion
				}
				if err != nil || version != want {
					t.Fatal("migration lost version/rollback", version, err)
				}
			}
		})
	}
}
