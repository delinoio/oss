package core

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func Failures(r Run) []Failure {
	out := []Failure{}
	for _, c := range r.Checks {
		out = append(out, c.Failures...)
		for _, d := range c.Diagnostics {
			out = append(out, Failure{ID: Hash([]byte(c.Name + "/diagnostic/" + d.Code)), Check: c.Name, Command: r.Config.Checks[c.Name].Command, Message: d.Message, LogID: c.Log.ID})
		}
	}
	return out
}
func (s *Service) Compare(id, previous string) (Comparison, error) {
	r, e := s.Store.Run(id)
	if e != nil {
		return Comparison{}, e
	}
	out := Comparison{RunID: id, New: []Failure{}, Continuing: []Failure{}, Resolved: []Failure{}}
	if previous == "" {
		e = s.Store.DB.QueryRow("SELECT id FROM runs WHERE repo=? AND branch=? AND commit_oid<>? AND fingerprint=? AND seq<? ORDER BY seq DESC LIMIT 1", r.RepositoryID, r.Branch, r.Commit, r.Fingerprint, r.Sequence).Scan(&previous)
		if errors.Is(e, sql.ErrNoRows) {
			out.Reason = "no compatible earlier-commit execution on this branch"
			return out, nil
		}
		if e != nil {
			return out, e
		}
	}
	p, e := s.Store.Run(previous)
	if e != nil {
		return out, e
	}
	out.PreviousID = previous
	if p.RepositoryID != r.RepositoryID || p.Fingerprint != r.Fingerprint {
		out.Reason = "incompatible repository or execution context"
		return out, nil
	}
	if !p.State.Terminal() || !r.State.Terminal() || p.State == Expired || r.State == Expired {
		out.Reason = "comparison requires retained completed evidence"
		return out, nil
	}
	old := map[string]Failure{}
	for _, f := range Failures(p) {
		old[f.ID] = f
	}
	for _, f := range Failures(r) {
		if _, ok := old[f.ID]; ok {
			out.Continuing = append(out.Continuing, f)
			delete(old, f.ID)
		} else {
			out.New = append(out.New, f)
		}
	}
	for _, f := range old {
		out.Resolved = append(out.Resolved, f)
	}
	sort.Slice(out.Resolved, func(i, j int) bool { return out.Resolved[i].ID < out.Resolved[j].ID })
	out.Available = true
	return out, nil
}

type PruneResult struct {
	DryRun      bool         `json:"dry_run"`
	RunIDs      []string     `json:"run_ids"`
	Bytes       int64        `json:"bytes"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func (s *Service) Prune(dry bool, age int, maxBytes int64) (PruneResult, error) {
	out := PruneResult{DryRun: dry, RunIDs: []string{}, Diagnostics: []Diagnostic{}}
	if age < 0 || maxBytes < 0 {
		return out, E("invalid-retention", "retention limits must be nonnegative", 2)
	}
	rows, e := s.Store.DB.Query("SELECT id FROM runs WHERE state NOT IN ('queued','preparing','running','collecting','expired') ORDER BY seq ASC")
	if e != nil {
		return out, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	rows.Close()
	type candidate struct {
		run  Run
		size int64
	}
	items := []candidate{}
	var total int64
	for _, id := range ids {
		r, e := s.Store.Run(id)
		if e != nil {
			return out, e
		}
		var size int64
		_ = filepath.WalkDir(filepath.Join(s.Store.Root, "evidence", id), func(_ string, d os.DirEntry, e error) error {
			if e == nil && !d.IsDir() {
				info, e := d.Info()
				if e == nil {
					size += info.Size()
				}
			}
			return nil
		})
		items = append(items, candidate{r, size})
		total += size
	}
	for _, item := range items {
		expired := age > 0 && time.Since(item.run.CreatedAt) > time.Duration(age)*24*time.Hour
		over := maxBytes > 0 && total > maxBytes
		if !expired && !over {
			continue
		}
		out.RunIDs = append(out.RunIDs, item.run.ID)
		out.Bytes += item.size
		total -= item.size
		if dry {
			continue
		}
		// Persist a terminal tombstone before deleting evidence. Later gates must still select this attempt.
		r := item.run
		r.State = Expired
		r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: "evidence-expired", Message: "retention removed this attempt's evidence"})
		if e = s.Store.SaveRun(r); e != nil {
			return out, e
		}
		for _, dir := range []string{"evidence", "workspaces"} {
			if e = os.RemoveAll(filepath.Join(s.Store.Root, dir, r.ID)); e != nil {
				d := Diagnostic{Code: "retention-cleanup-failed", Message: "owned files could not be removed for run " + r.ID}
				out.Diagnostics = append(out.Diagnostics, d)
				r.Diagnostics = append(r.Diagnostics, d)
				_ = s.Store.SaveRun(r)
			}
		}
	}
	return out, nil
}
