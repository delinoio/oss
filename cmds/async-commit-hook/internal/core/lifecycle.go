package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type Component struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Process    Process `json:"process"`
	ConfigHash string  `json:"config_hash"`
	StateDir   string  `json:"state_dir"`
}

func (s *Service) configHash() string {
	return Hash(Encode(struct {
		Mode  Mode
		Port  int
		State string
	}{s.Personal.Mode, s.Personal.APIPort, s.Personal.StateDir}))
}
func (s *Service) Active() ([]Component, error) {
	entries, e := os.ReadDir(s.Paths.Control)
	if e != nil {
		return nil, e
	}
	out := []Component{}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		b, e := os.ReadFile(filepath.Join(s.Paths.Control, entry.Name()))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		var c Component
		if json.Unmarshal(b, &c) != nil {
			return nil, E("component-record-invalid", "invalid lifecycle record; run ach doctor", 3)
		}
		alive := ProcessAlive(c.Process)
		if c.Kind == "check-supervisor" {
			var err error
			alive, err = supervisorLeaseActive(c.Process)
			if err != nil {
				return nil, err
			}
			if !alive {
				// Proof is final: this stopped supervisor cannot launch again.
				// Remove its stale lease before retention can remove the journal.
				if _, err = s.Store.DB.Exec("DELETE FROM components WHERE id=?", c.ID); err != nil {
					return nil, err
				}
				if err = os.Remove(filepath.Join(s.Paths.Control, entry.Name())); err != nil && !os.IsNotExist(err) {
					return nil, err
				}
			}
		}
		if alive {
			out = append(out, c)
		}
	}
	return out, nil
}
func (s *Service) Enter(kind string) (Component, func(), error) {
	p, err := ProcessIdentity(os.Getpid())
	if err != nil {
		return Component{}, nil, err
	}
	return s.enterProcess(kind, p)
}

func (s *Service) enterProcess(kind string, p Process) (Component, func(), error) {
	if _, err := os.Stat(filepath.Join(s.Paths.Control, "update.json")); err == nil {
		return Component{}, nil, E("update-recovery-required", "an update is pending; use ach self-update --recover before starting services", 3)
	}
	l, e := TryLock(filepath.Join(s.Paths.Control, "lifecycle.lock"))
	for attempt := 0; l == nil && e == nil && attempt < 100; attempt++ {
		time.Sleep(10 * time.Millisecond)
		l, e = TryLock(filepath.Join(s.Paths.Control, "lifecycle.lock"))
	}
	if e != nil {
		return Component{}, nil, e
	}
	if l == nil {
		return Component{}, nil, E("lifecycle-busy", "another startup or update owns the lifecycle; retry", 3)
	}
	defer l.Close()
	if _, err := os.Stat(filepath.Join(s.Paths.Control, "update.json")); err == nil {
		return Component{}, nil, E("update-recovery-required", "an update is pending; use ach self-update --recover", 3)
	}
	active, e := s.Active()
	if e != nil {
		return Component{}, nil, e
	}
	for _, c := range active {
		if c.ConfigHash != s.configHash() {
			return Component{}, nil, E("configuration-active", "stop all checks and servers using the previous mode, port and state directory before applying configuration changes", 2)
		}
	}
	c := Component{ID: ID(), Kind: kind, Process: p, ConfigHash: s.configHash(), StateDir: s.Store.Root}
	path := filepath.Join(s.Paths.Control, c.ID+".json")
	if e = AtomicWrite(path, Encode(c), 0600); e != nil {
		return c, nil, e
	}
	_, e = s.Store.DB.Exec("INSERT INTO components(id,kind,pid,birth,config_hash,created) VALUES(?,?,?,?,?,?)", c.ID, c.Kind, p.PID, p.Birth, c.ConfigHash, time.Now().UTC().Format(time.RFC3339Nano))
	if e != nil {
		_ = os.Remove(path)
		return c, nil, e
	}
	return c, func() { _, _ = s.Store.DB.Exec("DELETE FROM components WHERE id=?", c.ID); _ = os.Remove(path) }, nil
}
func (s *Service) Start(mode Mode) error {
	active, e := s.Active()
	if e != nil {
		return e
	}
	for _, c := range active {
		if c.ConfigHash != s.configHash() {
			return E("configuration-active", "stop existing processes before changing mode, port or state_dir", 2)
		}
		if mode == Daemon && c.Kind == "daemon" {
			var stop string
			if err := s.Store.DB.QueryRow("SELECT stop FROM components WHERE id=?", c.ID).Scan(&stop); err != nil {
				return err
			}
			if stop != "" {
				return E("daemon-draining", "request is saved; start the daemon after its current drain finishes", 3)
			}
			return nil
		}
	}
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	sub := "worker"
	if mode == Daemon {
		sub = "serve"
	}
	cmd := exec.Command(executable, "internal", sub, "--config", s.Paths.Config)
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	f, e := os.OpenFile(filepath.Join(s.Paths.Control, "runner.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	cmd.Stdout = f
	cmd.Stderr = f
	Detached(cmd)
	if e = cmd.Start(); e != nil {
		return E("worker-start-failed", "request is saved but background process could not start; retry ach daemon start or ach run", 3)
	}
	_ = cmd.Process.Release()
	for i := 0; i < 100; i++ {
		active, e = s.Active()
		if e != nil {
			return e
		}
		for _, c := range active {
			if ((mode == Daemon && c.Kind == "daemon") || (mode == OnDemand && c.Kind == "worker")) && c.ConfigHash == s.configHash() {
				return nil
			}
		}
		if mode == OnDemand {
			pending, err := s.Store.Pending()
			if err != nil {
				return err
			}
			if len(pending) == 0 {
				return nil
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	return E("runner-start-failed", "request remains saved; inspect ach doctor and local runner.log for port or startup errors", 3)
}
func (s *Service) Stop(force bool) error {
	active, e := s.Active()
	if e != nil {
		return e
	}
	action := "drain"
	if force {
		action = "force"
	}
	for _, c := range active {
		if c.Kind == "daemon" {
			if _, e = s.Store.DB.Exec("UPDATE components SET stop=? WHERE id=?", action, c.ID); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Service) WebURL(run, code string) string {
	fragment := url.Values{}
	fragment.Set("port", strconv.Itoa(s.Personal.APIPort))
	if run != "" {
		fragment.Set("run", run)
	}
	if code != "" {
		fragment.Set("pair", code)
	}
	return "https://ach.delino.io/#" + fragment.Encode()
}
func (s *Service) Worker(ctx context.Context) error {
	c, close, e := s.Enter("worker")
	if e != nil {
		return e
	}
	defer close()
	return s.Work(ctx, false, c.ID)
}
func (s *Service) Doctor(ctx context.Context, path string) []Diagnostic {
	out := []Diagnostic{}
	add := func(code, message, hint string) { out = append(out, Diagnostic{code, message, hint}) }
	if !SupportedTarget() {
		add("unsupported-platform", "this OS/architecture is unsupported", "use a supported desktop target")
	}
	if _, e := exec.LookPath("git"); e != nil {
		add("git-missing", "Git is not available", "install Git and add it to PATH")
	}
	var integrity string
	if e := s.Store.DB.QueryRow("PRAGMA quick_check").Scan(&integrity); e != nil || integrity != "ok" {
		add("state-integrity", "SQLite health check failed", "stop ach and restore a consistent backup")
	}
	r, e := s.Plan(ctx, path, "")
	if e != nil {
		add("plan-unavailable", e.Error(), "run ach init; commit valid configuration")
	} else {
		for name, c := range r.Config.Checks {
			if !c.Applies(r.OS) {
				continue
			}
			if _, e = exec.LookPath(string(c.SelectedShell(r.OS))); e != nil {
				add("shell-missing", "shell for "+name+" is unavailable", "install the configured shell")
			}
			if _, _, e = s.Personal.CommandEnvironment(c, r.Environment); e != nil {
				add("environment-unavailable", e.Error(), "restore the declared input/reference; values are never shown")
			}
		}
	}
	active, e := s.Active()
	if e != nil {
		add("lifecycle-unavailable", e.Error(), "inspect account-owned lifecycle records")
	} else {
		for _, c := range active {
			if c.ConfigHash != s.configHash() {
				add("configuration-active", "a previous configuration still owns active processes", "restore the previous config and stop checks/servers first")
			}
		}
	}
	entries, e := os.ReadDir(filepath.Join(s.Store.Root, "workspaces"))
	if e == nil {
		for _, entry := range entries {
			r, err := s.Store.Run(entry.Name())
			if err == nil && r.State.Terminal() {
				add("workspace-cleanup-pending", "owned workspace remains for run "+r.ID, "run ach prune after reviewing its diagnostic")
			}
		}
	}
	if len(out) == 0 {
		add("healthy", fmt.Sprintf("state, registry, tools and declared inputs are ready (%s)", s.Personal.Mode), "")
	}
	return out
}
