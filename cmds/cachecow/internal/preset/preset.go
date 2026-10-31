// SPDX-License-Identifier: Apache-2.0

// Package preset discovers tool-owned cache roots without modifying freshness
// metadata. A successful discovery is not proof that an existing cache was built
// with these settings: compare its recorded Compatibility before reuse.
package preset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/delinoio/oss/cmds/cachecow/internal/repository"
	"golang.org/x/mod/semver"
)

type ID string

const (
	Cargo   ID = "cargo"
	Rspack  ID = "rspack"
	Rsbuild ID = "rsbuild"
)

type RootKind string

const (
	Target     RootKind = "target"
	Build      RootKind = "build"
	Persistent RootKind = "persistent"
)

type Root struct {
	Path  string
	Kinds []RootKind
}
type Compatibility struct {
	Schema          int
	Preset          ID
	ToolVersion     string
	CompilerVersion string
	NodeVersion     string
	Platform        string
	Architecture    string
	SettingsDigest  string
}

func (c Compatibility) Verified() bool {
	if c.Schema != 1 || c.Platform == "" || c.Architecture == "" || len(c.SettingsDigest) != 64 {
		return false
	}
	if _, err := hex.DecodeString(c.SettingsDigest); err != nil {
		return false
	}
	switch c.Preset {
	case Cargo:
		return supported(c.ToolVersion, "1.99.1", "2.0.0") && len(c.CompilerVersion) == 64
	case Rspack, Rsbuild:
		return supported(c.ToolVersion, "2.3.0", "3.0.0") && supported(c.CompilerVersion, "2.3.0", "3.0.0") && supported(c.NodeVersion, "24.0.0", "1000000.0.0")
	default:
		return false
	}
}
func (c Compatibility) Compatible(other Compatibility) bool {
	return c.Verified() && other.Verified() && c == other
}

type Result struct {
	Inputs        []repository.FileStamp
	Preset        ID
	Repository    repository.Info
	Roots         []Root
	Compatibility Compatibility
}
type Request struct {
	CWD       string
	Argv      []string // Exact command, including executable; never persisted.
	Env       []string // Complete child environment. Nil inherits the current environment.
	Preset    ID       // Empty selects from the executable basename.
	Protected []string // Settings storage, source caches, and other forbidden paths.
	Runner    Runner   // Optional process adapter for deterministic fixtures.
}
type Runner interface {
	Output(context.Context, string, []string, []string) ([]byte, error)
}
type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, cwd string, env, argv []string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("discovery command is empty")
	}
	// Resolve PATH from the supplied child environment, not the agent's process.
	executable := argv[0]
	if !strings.ContainsAny(executable, "/\\") {
		executable = ""
		for _, dir := range filepath.SplitList(envValue(env, "PATH")) {
			if dir == "" {
				continue
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(cwd, dir)
			}
			p := filepath.Join(dir, argv[0])
			if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(p), ".exe") {
				p += ".exe"
			}
			if s, e := os.Stat(p); e == nil && !s.IsDir() && (runtime.GOOS == "windows" || s.Mode()&0111 != 0) {
				executable = p
				break
			}
		}
		if executable == "" {
			return nil, errors.New("discovery executable was not found in the child PATH; install the selected tool")
		}
	} else if !filepath.IsAbs(executable) {
		executable = filepath.Join(cwd, executable)
	}
	cmd := exec.CommandContext(ctx, executable, argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = env
	b, e := cmd.Output()
	if e != nil {
		return nil, errors.New("tool discovery failed; verify the selected tool and project configuration directly")
	}
	return b, nil
}
func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == key {
			return v
		}
	}
	return ""
}
func setEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, v := range env {
		if !strings.HasPrefix(v, key+"=") {
			out = append(out, v)
		}
	}
	return append(out, key+"="+value)
}
func supported(v, min, max string) bool {
	return semver.IsValid("v"+v) && !strings.ContainsAny(v, "-+") && semver.Compare("v"+v, "v"+min) >= 0 && semver.Compare("v"+v, "v"+max) < 0
}
func digest(value any) string {
	b, _ := json.Marshal(value)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func Discover(ctx context.Context, req Request) (Result, error) {
	var out Result
	if len(req.Argv) == 0 {
		return out, errors.New("supply the exact build command for preset discovery")
	}
	if req.Env == nil {
		req.Env = os.Environ()
	}
	if req.Runner == nil {
		req.Runner = ExecRunner{}
	}
	cwd, e := repository.Canonical(req.CWD)
	if e != nil {
		return out, e
	}
	req.CWD = cwd
	out.Repository, e = repository.Discover(ctx, cwd)
	if e != nil {
		return out, e
	}
	out.Preset = req.Preset
	if out.Preset == "" {
		out.Preset = ID(strings.TrimSuffix(strings.ToLower(filepath.Base(req.Argv[0])), ".exe"))
	}
	if out.Preset != Cargo && out.Preset != Rspack && out.Preset != Rsbuild {
		return out, errors.New("cannot identify the build tool; select --preset cargo, rspack, or rsbuild")
	}
	out.Compatibility = Compatibility{Schema: 1, Preset: out.Preset, Platform: runtime.GOOS, Architecture: runtime.GOARCH}
	var settings any
	if out.Preset == Cargo {
		settings, e = discoverCargo(ctx, req, &out)
	} else {
		settings, e = discoverFrontend(ctx, req, &out)
	}
	if e != nil {
		return Result{}, e
	}
	if e := repository.VerifyFileStamps(out.Inputs); e != nil {
		return Result{}, e
	}
	if len(out.Roots) == 0 {
		return Result{}, errors.New("no verified cache roots; configure a supported persistent cache")
	}
	for i := range out.Roots {
		p, e := out.Repository.ValidateCache(ctx, out.Roots[i].Path, req.Protected...)
		if e != nil {
			return Result{}, e
		}
		out.Roots[i].Path = p
	}
	// Merge equal roots but reject partial overlap: lifecycle operations must never
	// move a parent out from under another managed cache.
	var roots []Root
	for _, r := range out.Roots {
		merged := false
		for i := range roots {
			if r.Path == roots[i].Path {
				for _, kind := range r.Kinds {
					found := false
					for _, existing := range roots[i].Kinds {
						if kind == existing {
							found = true
							break
						}
					}
					if !found {
						roots[i].Kinds = append(roots[i].Kinds, kind)
					}
				}
				merged = true
				break
			}
			if repository.Overlaps(r.Path, roots[i].Path) {
				return Result{}, errors.New("cache roots overlap; configure equal or disjoint cache directories")
			}
		}
		if !merged {
			roots = append(roots, r)
		}
	}
	out.Roots = roots
	// Only digests leave discovery. Conservative environment hashing can reject
	// reusable caches, but never assumes an unknown setting is irrelevant.
	normalize := func(s string) string { return strings.ReplaceAll(s, out.Repository.Root, "${WORKTREE}") }
	env := make([]string, 0, len(req.Env))
	for _, entry := range req.Env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "CCOW_SESSION", "PWD", "OLDPWD", "SHLVL", "_":
			continue
		}
		env = append(env, normalize(entry))
	}
	sort.Strings(env)
	args := append([]string(nil), req.Argv[1:]...)
	for i := range args {
		args[i] = normalize(args[i])
	}
	paths := make([]string, len(out.Roots))
	for i, r := range out.Roots {
		paths[i] = normalize(r.Path)
	}
	out.Compatibility.SettingsDigest = digest(struct {
		Args, Env, Roots []string
		CWD              string
		Settings         any
	}{args, env, paths, normalize(req.CWD), settings})
	slog.DebugContext(ctx, "cache discovery verified", "preset", out.Preset, "cache_roots", len(out.Roots), "tool_version", out.Compatibility.ToolVersion)
	return out, nil
}
