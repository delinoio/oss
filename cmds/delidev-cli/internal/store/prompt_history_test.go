// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func historyRows(t *testing.T, s *Store, project domain.ID) []Record {
	t.Helper()
	var rows []Record
	err := s.Read(context.Background(), func(tx *Tx) error {
		var err error
		rows, err = tx.ListProjectPromptHistory(project, 0, 101)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func appendHistory(t *testing.T, s *Store, request, project domain.ID, text string) Result {
	t.Helper()
	result, err := s.Mutate(context.Background(), request, "history.test", struct {
		Project domain.ID
		Text    string
	}{project, text}, func(tx *Tx) (any, error) { return nil, tx.AppendProjectPromptHistory(project, text) })
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestProjectPromptHistoryAtomicBoundReplayAndIndependentLifetime(t *testing.T) {
	s, _ := openTest(t)
	project, other := domain.NewID(), domain.NewID()
	create(t, s, project, "A")
	create(t, s, other, "B")
	exact := "  first\n한글\t "
	request := domain.NewID()
	appendHistory(t, s, request, project, exact)
	appendHistory(t, s, domain.NewID(), project, exact)
	if !appendHistory(t, s, request, project, exact).Replayed || len(historyRows(t, s, project)) != 2 {
		t.Fatal("receipt replay duplicated history")
	}
	row := historyRows(t, s, project)[0]
	value, _ := Decode[domain.ProjectPromptHistory](row)
	if value.Prompt != exact || row.SessionID != "" {
		t.Fatal("text/ownership changed")
	}
	_, err := s.Mutate(context.Background(), domain.NewID(), "rollback", nil, func(tx *Tx) (any, error) {
		if err := tx.AppendProjectPromptHistory(project, "rollback canary"); err != nil {
			return nil, err
		}
		return nil, errors.New("rollback")
	})
	if err == nil || len(historyRows(t, s, project)) != 2 {
		t.Fatal("rollback added history")
	}
	var wg sync.WaitGroup
	for i := 0; i < 101; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			appendHistory(t, s, domain.NewID(), project, fmt.Sprintf("concurrent %d", i))
		}(i)
	}
	wg.Wait()
	rows := historyRows(t, s, project)
	if len(rows) != 100 {
		t.Fatal(len(rows))
	}
	var previous uint64 = ^uint64(0)
	for _, r := range rows {
		v, _ := Decode[domain.ProjectPromptHistory](r)
		if v.AcceptanceSequence >= previous {
			t.Fatal("nonmonotonic acceptance order")
		}
		previous = v.AcceptanceSequence
	}
	appendHistory(t, s, domain.NewID(), other, "B retained")
	_, err = s.Mutate(context.Background(), domain.NewID(), "project.delete", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.ProjectKind, project, 1) })
	if err != nil {
		t.Fatal(err)
	}
	retained, err := s.List(context.Background(), Filter{Kind: domain.ProjectPromptHistoryKind, ProjectID: project, Limit: 200})
	if err != nil || len(retained) != 0 || len(historyRows(t, s, other)) != 1 {
		t.Fatal("project deletion crossed scope", err)
	}
}
func TestProjectPromptHistoryClearReplayMaximumTextAndBackup(t *testing.T) {
	s, root := openTest(t)
	project := domain.NewID()
	create(t, s, project, "A")
	text := strings.Repeat("x", 256<<10)
	appendHistory(t, s, domain.NewID(), project, text)
	backup, err := s.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "backups", string(backup)+".sqlite")
	if err := ValidateBackup(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	clear := func() Result {
		r, e := s.Mutate(context.Background(), request, "history.clear", project, func(tx *Tx) (any, error) { return tx.ClearProjectPromptHistory(project) })
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	clear()
	var retired int
	if err := s.db.QueryRow("SELECT count(*) FROM tombstones WHERE kind='project_prompt_history'").Scan(&retired); err != nil || retired != 0 {
		t.Fatal("live clear prevents captured backup restoration", retired, err)
	}
	appendHistory(t, s, domain.NewID(), project, "later")
	if !clear().Replayed || len(historyRows(t, s, project)) != 1 {
		t.Fatal("replayed clear erased later submission")
	}
	// The immutable backup still contains the original independent history.
	db, err := sql.Open("sqlite", databaseURI(path, true)+"&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw []byte
	if err := db.QueryRow("SELECT body FROM entities WHERE kind='project_prompt_history' AND project_id=?", project).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var value domain.ProjectPromptHistory
	if domain.Decode(raw, &value) != nil || value.Prompt != text {
		t.Fatal("backup lost history")
	}
}

func TestProjectPromptHistorySurvivesRestartAndSessionDeletionAndRestoresCapturedState(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	project, session := domain.NewID(), domain.NewID()
	create(t, s, project, "A")
	exact := "  first\n한글  "
	appendHistory(t, s, domain.NewID(), project, exact)
	_, err := s.Mutate(ctx, domain.NewID(), "session.fixture", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.SessionKind, session, 0, session, project, domain.Session{Name: "Source session"})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "session.delete", nil, func(tx *Tx) (any, error) { return nil, tx.Delete(domain.SessionKind, session, 1) })
	if err != nil {
		t.Fatal(err)
	}
	if len(historyRows(t, s, project)) != 1 {
		t.Fatal("session deletion erased history")
	}
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows := historyRows(t, reopened, project)
	value, _ := Decode[domain.ProjectPromptHistory](rows[0])
	if value.Prompt != exact {
		t.Fatal("restart changed text")
	}
	_, err = reopened.Mutate(ctx, domain.NewID(), "history.clear", project, func(tx *Tx) (any, error) { return tx.ClearProjectPromptHistory(project) })
	if err != nil {
		t.Fatal(err)
	}
	// Opening an immutable captured database copy validates the same stored history
	// used by managed restore, without replacing the original live database.
	restoredRoot := filepath.Join(t.TempDir(), "restored")
	if err := os.MkdirAll(restoredRoot, 0700); err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(filepath.Join(root, "backups", string(backup)+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(restoredRoot, "state.sqlite"), image, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, restoredRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	rows = historyRows(t, restored, project)
	value, _ = Decode[domain.ProjectPromptHistory](rows[0])
	if value.Prompt != exact || len(historyRows(t, reopened, project)) != 0 {
		t.Fatal("captured/live history conflated")
	}
}
