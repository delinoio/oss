package core

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Service struct {
	Store    *Store
	Personal Personal
	Paths    Paths
	Log      *slog.Logger
}

func Open(paths Paths) (*Service, error) {
	p, e := ReadPersonal(paths)
	if e != nil {
		return nil, e
	}
	if e = PrivateDir(paths.Control); e != nil {
		return nil, e
	}
	s, e := OpenStore(p.StateDir)
	if e != nil {
		return nil, e
	}
	service := &Service{Store: s, Personal: p, Paths: paths, Log: slog.New(slog.NewJSONHandler(os.Stderr, nil))}
	if err := service.cleanupUpdateHelpers(); err != nil {
		service.Log.Warn("update.helper_cleanup_pending", "code", "update-helper-cleanup-pending")
	}
	return service, nil
}
func (s *Service) Close() error { return s.Store.Close() }
func (s *Service) Init(ctx context.Context, path string) (Repository, Worktree, error) {
	common, root, branch, e := Discover(ctx, path)
	if e != nil {
		return Repository{}, Worktree{}, e
	}
	if managed, _ := Git(ctx, root, "config", "--local", "--get", "ach.managed"); managed == "true" {
		return Repository{}, Worktree{}, E("managed-workspace", "managed workspaces cannot be registered", 2)
	}
	file := filepath.Join(root, ProjectFile)
	if _, e = os.Stat(file); os.IsNotExist(e) {
		if e = AtomicCreate(file, []byte(DefaultProject), 0600); e != nil && !os.IsExist(e) {
			return Repository{}, Worktree{}, e
		}
	} else if e != nil {
		return Repository{}, Worktree{}, e
	}
	return s.Store.Register(common, root, branch)
}
func (s *Service) Plan(ctx context.Context, path, commit string) (Run, error) {
	common, root, branch, e := Discover(ctx, path)
	if e != nil {
		return Run{}, e
	}
	repo, wt, e := s.Store.Registered(common, root)
	if e != nil {
		return Run{}, e
	}
	sha, e := ResolveCommit(ctx, root, commit)
	if e != nil {
		return Run{}, e
	}
	p, e := CommitConfig(ctx, root, sha)
	if e != nil {
		return Run{}, e
	}
	if e = ValidateSource(ctx, root, sha); e != nil {
		return Run{}, e
	}
	env, e := PublicEnvironment(p)
	if e != nil {
		return Run{}, e
	}
	r := Run{SchemaVersion: 1, ID: ID(), RepositoryID: repo.ID, WorktreeID: wt.ID, Source: root, Branch: branch, Commit: sha, State: Queued, Config: p, Environment: env, Fingerprint: Fingerprint(p, env), OS: runtime.GOOS, Arch: runtime.GOARCH, CreatedAt: time.Now().UTC(), Checks: []Check{}, Diagnostics: []Diagnostic{}}
	order, _ := p.Order()
	for _, name := range order {
		c := p.Checks[name]
		group := repo.ID + ":" + name
		if c.Group != "" {
			group = "named:" + c.Group
		}
		state := Queued
		if !c.Applies(runtime.GOOS) {
			state = Skipped
		}
		r.Checks = append(r.Checks, Check{ID: ID(), Name: name, State: state, Optional: c.Optional, Shell: c.SelectedShell(runtime.GOOS), Group: group, Policy: c.Policy, Reports: []Evidence{}, Failures: []Failure{}, Diagnostics: []Diagnostic{}})
	}
	return r, nil
}
func (s *Service) Submit(ctx context.Context, path, commit string, automatic bool) (Receipt, error) {
	r, e := s.Plan(ctx, path, commit)
	if e != nil {
		return Receipt{}, e
	}
	if m, _ := Git(ctx, r.Source, "config", "--local", "--get", "ach.managed"); m == "true" {
		return Receipt{}, E("managed-workspace", "automatic execution is disabled in managed workspaces", 2)
	}
	auto := ""
	if automatic {
		auto = r.WorktreeID + ":" + r.Commit + ":" + r.Fingerprint
	}
	id, e := s.Store.InsertRun(&r, auto)
	if e != nil {
		return Receipt{}, Wrap("submission-failed", e)
	}
	r, e = s.Store.Run(id)
	if e != nil {
		return Receipt{}, e
	}
	s.Log.Info("run.accepted", "run_id", id, "commit", r.Commit)
	return s.Receipt(r), nil
}
func (s *Service) Receipt(r Run) Receipt {
	out := Receipt{RunID: r.ID, Commit: r.Commit, State: r.State, StatusCommand: "ach status --run " + r.ID, WaitCommand: "ach wait --run " + r.ID, MCP: "ach_status / ach_wait with run_id=" + r.ID, Guide: "ach agent-guide"}
	if s.Personal.Mode == Daemon {
		out.URL = s.WebURL(r.ID, "")
	} else {
		out.UICommand = "ach ui --run " + r.ID
	}
	return out
}
func (s *Service) RepositoryID(ctx context.Context, path string) (string, error) {
	common, root, _, e := Discover(ctx, path)
	if e != nil {
		return "", e
	}
	r, _, e := s.Store.Registered(common, root)
	return r.ID, e
}
func (s *Service) Gate(ctx context.Context, path, commit string) (Gate, error) {
	p, e := s.Plan(ctx, path, commit)
	if e != nil {
		return Gate{}, e
	}
	g := Gate{Commit: p.Commit, State: Queued, Reason: "no compatible execution; run ach run --commit " + p.Commit}
	var id string
	e = s.Store.DB.QueryRow("SELECT id FROM runs WHERE repo=? AND commit_oid=? AND fingerprint=? ORDER BY seq DESC LIMIT 1", p.RepositoryID, p.Commit, p.Fingerprint).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return g, nil
	}
	if e != nil {
		return g, e
	}
	r, e := s.Store.Run(id)
	if e != nil {
		return g, e
	}
	return s.GateRun(r), nil
}
func (s *Service) GateRun(r Run) Gate {
	g := Gate{Commit: r.Commit, RunID: r.ID, State: r.State, Reason: "latest attempt is " + string(r.State), Diagnostics: r.Diagnostics}
	if r.State != Passed {
		return g
	}
	applicable := 0
	for _, c := range r.Checks {
		if c.State == Skipped {
			continue
		}
		applicable++
		if !c.State.Terminal() {
			g.Reason = "checks are incomplete"
			return g
		}
		if !c.Optional {
			if c.State != Passed {
				g.Reason = "required check " + c.Name + " is " + string(c.State)
				return g
			}
			if err := s.ValidateEvidence(r.ID, c); err != nil {
				g.Reason = "required evidence is unavailable: " + c.Name
				return g
			}
		}
	}
	if applicable == 0 {
		g.Reason = "no applicable checks"
		return g
	}
	if len(r.Diagnostics) > 0 {
		g.Reason = "run has unresolved runner diagnostics"
		return g
	}
	g.Passed = true
	g.Reason = "required checks passed for this exact commit and compatible context; external tools, services and secret values are not proven identical"
	return g
}
func (s *Service) Wait(ctx context.Context, id string) (Run, error) {
	for {
		r, e := s.Store.Run(id)
		if e != nil {
			return r, e
		}
		if r.State.Terminal() {
			return r, nil
		}
		select {
		case <-ctx.Done():
			return r, E("wait-expired", "wait ended; execution continues", 4)
		case <-time.After(150 * time.Millisecond):
		}
	}
}
func (s *Service) Rerun(id string, failedOnly bool) (Receipt, error) {
	retention, err := TryLock(filepath.Join(s.Store.Root, "locks", "retention.lock"))
	if err != nil {
		return Receipt{}, err
	}
	if retention == nil {
		return Receipt{}, E("retention-busy", "retention is changing evidence; retry the rerun", 3)
	}
	defer retention.Close()
	original, e := s.Store.Run(id)
	if e != nil {
		return Receipt{}, e
	}
	if !original.State.Terminal() || original.State == Expired {
		return Receipt{}, E("rerun-unavailable", "rerun requires retained completed source metadata", 2)
	}
	if original.OS != runtime.GOOS || original.Arch != runtime.GOARCH {
		return Receipt{}, E("incompatible-context", "rerun requires the original OS and architecture", 2)
	}
	selected := map[string]bool{}
	var selectWithDeps func(string)
	selectWithDeps = func(n string) {
		if selected[n] {
			return
		}
		selected[n] = true
		for _, d := range original.Config.Checks[n].DependsOn {
			selectWithDeps(d)
		}
	}
	for _, c := range original.Checks {
		if c.State == Skipped {
			continue
		}
		if !failedOnly || c.State != Passed || s.ValidateEvidence(original.ID, c) != nil {
			selectWithDeps(c.Name)
		}
	}
	if len(selected) == 0 {
		return Receipt{}, E("nothing-to-rerun", "no failed or blocked checks", 2)
	}
	r := original
	r.ID = ID()
	r.Sequence = 0
	r.ParentID = original.ID
	r.State = Queued
	r.CreatedAt = time.Now().UTC()
	r.FinishedAt = nil
	r.AcknowledgedAt = nil
	r.Diagnostics = []Diagnostic{}
	r.Checks = append([]Check(nil), original.Checks...)
	for i, c := range r.Checks {
		c.ID = ID()
		if selected[c.Name] {
			c.State = Queued
			c.Process = Process{}
			c.ExitCode = nil
			c.StartedAt = nil
			c.FinishedAt = nil
			c.Log = Evidence{}
			c.Reports = []Evidence{}
			c.Failures = []Failure{}
			c.Diagnostics = []Diagnostic{}
			c.InheritedFrom = ""
		} else if c.State == Passed {
			if c.InheritedFrom == "" {
				c.InheritedFrom = original.ID
			}
		}
		r.Checks[i] = c
	}
	_, e = s.Store.InsertRun(&r, "")
	return s.Receipt(r), e
}
