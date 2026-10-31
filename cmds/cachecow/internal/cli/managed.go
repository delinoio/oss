// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/cachecow/internal/lease"
	"github.com/delinoio/oss/cmds/cachecow/internal/lifecycle"
	"github.com/delinoio/oss/cmds/cachecow/internal/preset"
	"github.com/delinoio/oss/cmds/cachecow/internal/process"
	"github.com/delinoio/oss/cmds/cachecow/internal/repository"
	"github.com/delinoio/oss/cmds/cachecow/internal/session"
	"github.com/delinoio/oss/cmds/cachecow/internal/shim"
	"github.com/delinoio/oss/cmds/cachecow/internal/state"
)

type claims struct {
	PreparedAll bool
	Current     preset.Result
	Source      *preset.Result
	Snapshot    repository.Snapshot
}
type provenance struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Compatibility preset.Compatibility `json:"compatibility"`
	Paths         []string             `json:"paths"`
}

func paths(r preset.Result) []string {
	p := []string{}
	for _, v := range r.Roots {
		p = append(p, v.Path)
	}
	return p
}
func recordKey(p []string) string { b, _ := json.Marshal(p); return state.Key(string(b)) }
func metadataPath(root string, r preset.Result) string {
	return filepath.Join(root, "cache-"+recordKey(paths(r))+".json")
}
func transactionPath(root string, r preset.Result) string {
	return filepath.Join(root, "transaction-"+recordKey(paths(r))+".json")
}

// Validate is called in the supervisor, including after acquiring the complete
// resource set. No tool or Git process runs inside this boundary.
func Validate(p session.Plan) error {
	if p.SchemaVersion != 1 {
		return errors.New("unsupported operation schemaVersion")
	}
	var c claims
	d := json.NewDecoder(bytes.NewReader(p.Claims))
	d.DisallowUnknownFields()
	if e := d.Decode(&c); e != nil {
		return e
	}
	if !c.Current.Compatibility.Verified() {
		return errors.New("unverified preset compatibility")
	}
	if p.Record != transactionPath(p.Root, c.Current) {
		return errors.New("invalid transaction record path")
	}
	if e := c.Snapshot.Verify(); e != nil {
		return e
	}
	if c.Snapshot.Repository.ID != c.Current.Repository.ID {
		return errors.New("snapshot belongs to another worktree")
	}
	expected := paths(c.Current)
	if !equalPaths(expected, p.Resources.Destinations) {
		return errors.New("cache claims differ from discovered destinations")
	}
	allowed := map[string]bool{}
	for _, v := range c.Snapshot.Caches {
		allowed[v] = true
	}
	for _, v := range expected {
		if !allowed[v] {
			return errors.New("destination missing from discovery snapshot")
		}
		if _, e := repository.VerifyCacheClaim(c.Current.Repository, v, p.Root); e != nil {
			return e
		}
	}
	if c.Source != nil {
		if c.Source.Repository.CommonDir != c.Current.Repository.CommonDir {
			return errors.New("source belongs to another repository")
		}
		if !c.Current.Compatibility.Compatible(c.Source.Compatibility) {
			return errors.New("source tool version, platform, architecture, or build settings differ")
		}
		if !equalPaths(paths(*c.Source), p.Resources.Sources) {
			return errors.New("source claims differ from discovered roots")
		}
		for _, v := range p.Resources.Sources {
			if !allowed[v] {
				return errors.New("source missing from discovery snapshot")
			}
			if _, e := repository.VerifyCacheClaim(c.Source.Repository, v, p.Root); e != nil {
				return e
			}
		}
		var saved provenance
		if e := state.Read(metadataPath(p.Root, *c.Source), &saved); e != nil {
			return fmt.Errorf("source provenance is unavailable; run the exact requested command through ccow in the source first: %w", e)
		}
		if saved.SchemaVersion != 1 || !saved.Compatibility.Compatible(c.Source.Compatibility) || !equalPaths(saved.Paths, p.Resources.Sources) {
			return errors.New("source provenance does not match current settings; run the exact command through ccow in the source first")
		}
	} else if len(p.Resources.Sources) != 0 {
		return errors.New("source resources have no verified claims")
	}
	for _, src := range p.Resources.Sources {
		for _, dst := range expected {
			if repository.Overlaps(src, dst) {
				return errors.New("source and destination resource sets overlap")
			}
		}
	}
	for _, pair := range p.Pairs {
		if !containsPath(expected, pair.Destination) {
			return errors.New("unverified destination pair")
		}
		if pair.Source == pair.Destination {
			continue
		}
		if c.Source == nil || !containsPath(p.Resources.Sources, pair.Source) {
			return errors.New("unverified source pair")
		}
		if repository.Overlaps(pair.Source, pair.Destination) {
			return errors.New("source and destination caches overlap")
		}
	}
	return nil
}
func containsPath(p []string, s string) bool {
	for _, v := range p {
		if v == s {
			return true
		}
	}
	return false
}
func equalPaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (a *app) detect(cwd string) (string, error) {
	if a.o.preset != "" {
		return a.o.preset, nil
	}
	r, e := repository.Discover(a.ctx, cwd)
	if e != nil {
		return "", e
	}
	for dir := cwd; repository.Contains(r.Root, dir); dir = filepath.Dir(dir) {
		found := []string{}
		if _, e := os.Stat(filepath.Join(dir, "Cargo.toml")); e == nil {
			found = append(found, "cargo")
		}
		for _, name := range []string{"rspack", "rsbuild"} {
			for _, ext := range []string{"js", "ts", "mjs", "cjs", "mts", "cts"} {
				if _, e := os.Stat(filepath.Join(dir, name+".config."+ext)); e == nil {
					found = append(found, name)
					break
				}
			}
		}
		if len(found) == 1 {
			return found[0], nil
		}
		if len(found) > 1 {
			return "", bad("ambiguous preset; select --preset cargo, rspack, or rsbuild")
		}
		if dir == r.Root {
			break
		}
	}
	return "", bad("cannot detect a preset; select --preset cargo, rspack, or rsbuild")
}
func (a *app) discover(cwd string, argv []string) (preset.Result, error) {
	id := a.o.preset
	if id == "" {
		if len(argv) > 0 {
			if t, ok := shim.FromArgv0(argv[0]); ok {
				id = string(t)
			}
		}
		if id == "" {
			v, e := a.detect(cwd)
			if e != nil {
				return preset.Result{}, e
			}
			id = v
		}
	}
	discovery := append([]string(nil), argv...)
	if len(discovery) == 0 {
		r, e := a.resolve(shim.Tool(id))
		if e != nil {
			return preset.Result{}, e
		}
		discovery = []string{r.Path}
	} else {
		// Discovery and execution must resolve the same executable. In particular,
		// an explicit path must never certify the unrelated tool found on PATH.
		command, e := a.command(discovery, cwd)
		if e != nil {
			return preset.Result{}, e
		}
		discovery[0] = command.Path
	}
	return preset.Discover(a.ctx, preset.Request{CWD: cwd, Argv: discovery, Env: os.Environ(), Preset: preset.ID(id), Protected: []string{a.store.Root}})
}
func (a *app) managed(action string, argv []string) (int, error) {
	cwd, e := os.Getwd()
	if e != nil {
		return 0, e
	}
	if action == "run" || action == "tool-clean" {
		if len(argv) == 0 {
			return 0, bad("run requires -- COMMAND [ARGS...]")
		}
	}
	current, e := a.discover(cwd, argv)
	if e != nil {
		return 0, e
	}
	if action == "status" || action == "doctor" {
		return 0, a.inspect(current, action == "doctor")
	}
	if a.o.replace && a.interactive() {
		ok, e := a.confirm("Replace the selected working cache after a new clone is prepared?")
		if e != nil {
			return 0, e
		}
		if !ok {
			return 0, errors.New("replacement cancelled")
		}
	}
	for _, name := range []string{"processes", "leases"} {
		if _, e = a.store.Directory(name); e != nil {
			return 0, e
		}
	}
	p, c, e := a.plan(action, current, argv, false)
	for e != nil {
		var missing *sourceError
		if !errors.As(e, &missing) {
			return 0, e
		}
		if !a.interactive() {
			return 0, &failure{125, e.Error(), "Select a usable same-repository --source, or run interactively to choose a source/build. Build the source with ccow run -- and the exact requested command first. prepare never builds a source."}
		}
		a.notice("Source unavailable: %s", e)
		prompt := "Choose: 2 select another source; 4 cancel: "
		if action == "run" {
			prompt = "Choose: 1 build here without source; 2 select another source; 3 build source first; 4 cancel: "
		}
		choice, err := a.answer(prompt)
		if err != nil {
			return 0, err
		}
		switch choice {
		case "1":
			if action != "run" {
				continue
			}
			p, c, e = a.plan(action, current, argv, true)
		case "2":
			selected, err := a.answer("Source worktree: ")
			if err != nil {
				return 0, err
			}
			a.o.source = selected
			p, c, e = a.plan(action, current, argv, false)
		case "3":
			if action != "run" {
				continue
			}
			settings, err := a.store.Settings(current.Repository.ID)
			if err != nil {
				return 0, err
			}
			src, err := repository.SelectSource(a.ctx, current.Repository, a.o.source, settings.Source)
			if err != nil {
				a.notice("%s", err)
				continue
			}
			if src.Self {

				a.notice("Source is the current worktree; choose build here instead.")
				continue
			}
			rel, _ := filepath.Rel(current.Repository.Root, cwd)
			sourceCWD := filepath.Join(src.Repository.Root, rel)
			a.notice("Source working directory: %s\nExact requested argv: %q", sourceCWD, argv)
			ok, err := a.confirm("Run this exact command in the source?")
			if err != nil {
				return 0, err
			}
			if !ok {
				continue
			}
			sourceResult, err := a.discover(sourceCWD, argv)
			if err != nil {
				a.notice("%s", err)
				continue
			}
			sp, sc, err := a.plan("run", sourceResult, argv, true)
			if err != nil {
				return 0, err
			}
			code, err := a.execute(sp, sc, sourceResult)
			if err != nil || code != 0 {
				return code, err
			}
			p, c, e = a.plan(action, current, argv, false)
		case "4":
			return 0, errors.New("operation cancelled")
		default:
			continue
		}
	}
	return a.execute(p, c, current)
}

type sourceError struct{ error }

func (a *app) plan(action string, current preset.Result, argv []string, withoutSource bool) (session.Plan, process.Command, error) {
	p := session.Plan{SchemaVersion: 1, Root: a.store.Root, Action: session.Action(action), Replace: a.o.replace, AllowCopy: a.o.allowCopy, Record: transactionPath(a.store.Root, current)}
	if withoutSource {
		p.Replace = false
	}
	c := process.Command{Noop: true}
	var e error
	if action == "run" || action == "tool-clean" {
		c, e = a.command(argv, current.Repository.CWD)
		if e != nil {
			return p, c, e
		}
	}
	p.Resources.Destinations = paths(current)
	claim := claims{Current: current}
	need := a.o.replace
	allMissing := true
	for _, v := range current.Roots {
		exists, e := lifecycle.Exists(v.Path)
		if e != nil {
			return p, c, e
		}
		need = need || !exists
		allMissing = allMissing && !exists
	}
	if action == "clean" || action == "tool-clean" {
		need = false
	}
	if need && !withoutSource {
		saved, e := a.store.Settings(current.Repository.ID)
		if e != nil {
			return p, c, e
		}
		src, e := repository.SelectSource(a.ctx, current.Repository, a.o.source, saved.Source)
		if e != nil {
			return p, c, &sourceError{e}
		}
		if src.Self {
			if action == "prepare" && need {
				return p, c, &sourceError{errors.New("primary cache is missing; select another source or use ccow run to build it")}
			}

			if a.o.replace {
				return p, c, bad("cannot replace a cache from itself; choose another --source")
			}
			// Only the primary checkout receives the automatic build-in-place exception.
			if current.Repository.Root != current.Repository.Primary {
				return p, c, &sourceError{errors.New("selected source is the current derived worktree; choose another source")}
			}
		} else {
			rel, e := filepath.Rel(current.Repository.Root, current.Repository.CWD)
			if e != nil {
				return p, c, e
			}
			source, e := a.discover(filepath.Join(src.Repository.Root, rel), argv)
			if e != nil {
				return p, c, &sourceError{e}
			}
			if !current.Compatibility.Compatible(source.Compatibility) {
				return p, c, &sourceError{errors.New("source tool versions, platform, architecture, or build settings are incompatible")}
			}
			if len(source.Roots) != len(current.Roots) {
				return p, c, &sourceError{errors.New("source and destination cache layouts differ")}
			}
			for i, dst := range current.Roots {
				sr := source.Roots[i]
				if fmt.Sprint(sr.Kinds) != fmt.Sprint(dst.Kinds) {
					return p, c, &sourceError{errors.New("source and destination cache root roles differ")}
				}
				if repository.Overlaps(sr.Path, dst.Path) {
					return p, c, &sourceError{errors.New("source and destination cache paths overlap")}
				}
				ok, e := lifecycle.Exists(sr.Path)
				if e != nil || !ok {
					return p, c, &sourceError{fmt.Errorf("source cache is missing or unusable: %s", sr.Path)}
				}
				if e := filepath.WalkDir(sr.Path, func(_ string, d fs.DirEntry, e error) error {
					if e != nil {
						return e
					}
					if !d.IsDir() && !d.Type().IsRegular() {
						return &sourceError{errors.New("source cache contains a symlink or special file; repair it or select another source")}
					}
					return nil
				}); e != nil {
					return p, c, e
				}
				p.Pairs = append(p.Pairs, lifecycle.Pair{Source: sr.Path, Destination: dst.Path})
			}
			var prior provenance
			if e := state.Read(metadataPath(a.store.Root, source), &prior); e != nil {
				return p, c, &sourceError{errors.New("source compatibility is unrecorded; build it through ccow with the exact requested command first")}
			}
			if prior.SchemaVersion != 1 || !prior.Compatibility.Compatible(source.Compatibility) {
				return p, c, &sourceError{errors.New("source provenance is incompatible with the requested command")}
			}
			claim.Source = &source
			p.Resources.Sources = paths(source)
		}
	}
	if len(p.Pairs) == 0 {
		for _, path := range paths(current) {
			p.Pairs = append(p.Pairs, lifecycle.Pair{Source: path, Destination: path})
		}
	}
	claim.PreparedAll = claim.Source != nil && (allMissing || a.o.replace)
	all := append(append([]string{}, p.Resources.Destinations...), p.Resources.Sources...)
	snapshot, e := repository.CaptureSnapshot(a.ctx, current.Repository, all, a.store.Root)
	if e != nil {
		return p, c, e
	}
	claim.Snapshot = snapshot
	p.Claims, e = json.Marshal(claim)
	return p, c, e
}
func (a *app) execute(p session.Plan, c process.Command, current preset.Result) (int, error) {
	if capability := os.Getenv(session.Env); capability != "" {
		result, e := session.Nested(a.ctx, capability, p)
		if e != nil {
			return 0, e
		}
		a.reportCopy(result.Result)
		code := 0
		if !c.Noop {
			cmd, err := process.NewExecCommand(c)
			if err != nil {
				return executionCode(err), err
			}
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err = a.ctx.Err(); err == nil {
				err = cmd.Start()
			}
			if err == nil {
				// The outer native scope owns all descendants, including children
				// that detach. Ask that owner to cancel; killing only cmd leaks writers.
				stop := context.AfterFunc(a.ctx, func() {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					if err := session.Cancel(ctx, capability, result.Ticket); err != nil {
						a.notice("Cannot request session termination; outer cache protection remains active: %s", err)
					}
				})
				err = cmd.Wait()
				stop()
			}
			if err != nil {
				if cmd.ProcessState != nil {
					code = childCode(cmd.ProcessState)
				} else {
					code = executionCode(err)
				}
			}
		}
		if e = session.Complete(a.ctx, capability, result.Ticket, code); e != nil {
			return 0, e
		}
		if c.Noop {
			return 0, a.output(result.Result)
		}
		return code, nil
	}
	r, prepared, e := session.Execute(a.ctx, p, c, process.Streams{})
	if e != nil {
		return 0, e
	}
	a.reportCopy(prepared)
	if c.Noop && r.ExitCode == 0 {
		return 0, a.output(prepared)
	}
	return r.ExitCode, nil
}
func (a *app) reportCopy(r lifecycle.Result) {
	for _, v := range r.Trees {
		if v.CopiedFiles > 0 {
			a.notice("Ordinary copying was used for %d files (--allow-copy).", v.CopiedFiles)
		}
	}
}
func (a *app) inspect(r preset.Result, doctor bool) error {
	caches := map[string]bool{}
	for _, p := range paths(r) {
		v, e := lifecycle.Exists(p)
		if e != nil {
			return e
		}
		caches[p] = v
	}
	reg, e := lease.Open(a.ctx, filepath.Join(a.store.Root, "leases"))
	if e != nil {
		return e
	}
	defer reg.Close()
	records, e := reg.Records(a.ctx)
	if e != nil {
		return e
	}
	// Reconcile only completed native journals. Incomplete journals retain their
	// protection, even when the owner PID no longer exists.
	if doctor {
		for _, v := range records {
			scope, e := process.LoadScope(v.Ownership)
			if e == nil && process.Reconcile(scope) == nil {
				if e = reg.Reconcile(a.ctx, v, func(context.Context, lease.Record) error { return process.Reconcile(scope) }); e != nil {
					return e
				}
			}
		}
		records, e = reg.Records(a.ctx)
		if e != nil {
			return e
		}
	}
	saved, e := a.store.Settings(r.Repository.ID)
	if e != nil {
		return e
	}
	return a.output(map[string]any{"preset": r.Preset, "cachePaths": caches, "compatibility": r.Compatibility, "settings": saved, "leases": records, "cowCapability": "tested during preparation; --allow-copy permits ordinary fallback", "recovery": "Incomplete native journals retain leases. PID death alone cannot release protection."})
}

// Records updates provenance under the supervisor's lease, never after the
// client observes completion. Interrupted/failed builds retain caches but cannot
// certify them as compatible sources for another worktree.
func Records() session.RecordHooks {
	decode := func(p session.Plan) (claims, error) { var c claims; e := json.Unmarshal(p.Claims, &c); return c, e }
	return session.RecordHooks{
		Compatible: func(a, b session.Plan) bool {
			x, e := decode(a)
			if e != nil {
				return false
			}
			y, e := decode(b)
			return e == nil && x.Current.Compatibility.Compatible(y.Current.Compatibility)
		},
		Before: func(p session.Plan) error {
			c, e := decode(p)
			if e != nil {
				return e
			}
			path := metadataPath(p.Root, c.Current)
			var old provenance
			if e = state.Read(path, &old); e != nil && !os.IsNotExist(e) {
				return e
			}
			if e == nil && old.SchemaVersion != 1 {
				return errors.New("unsupported cache provenance schemaVersion; use a compatible ccow")
			}
			if p.Action == session.Prepare && c.Source == nil {
				return nil
			}
			if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
				return e
			}
			return nil
		},
		After: func(p session.Plan, code int) error {
			if code != 0 || p.Action == session.Clean || p.Action == session.ToolClean {
				return nil
			}
			c, e := decode(p)
			if e != nil {
				return e
			}
			if p.Action == session.Prepare && !c.PreparedAll {
				return nil
			}
			return state.Write(metadataPath(p.Root, c.Current), provenance{1, c.Current.Compatibility, paths(c.Current)})
		},
	}
}
