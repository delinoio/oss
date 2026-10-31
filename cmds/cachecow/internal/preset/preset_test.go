// SPDX-License-Identifier: Apache-2.0
package preset

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type call struct {
	cwd       string
	env, args []string
}
type fakeRunner struct {
	calls                       []call
	version, metadata, frontend string
}

func (f *fakeRunner) Output(_ context.Context, cwd string, env, args []string) ([]byte, error) {
	f.calls = append(f.calls, call{cwd, append([]string(nil), env...), append([]string(nil), args...)})
	if args[len(args)-1] == "--version" {
		return []byte(f.version), nil
	}
	if args[len(args)-1] == "-vV" {
		return []byte("rustc 1.99.1 (fixture)\nhost: x86_64-unknown-linux-gnu\nrelease: 1.99.1\n"), nil
	}
	if args[0] == "node" {
		return []byte(f.frontend), nil
	}
	return []byte(f.metadata), nil
}
func project(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "project 한 space")
	os.Mkdir(p, 0700)
	cmd := exec.Command("git", "-C", p, "init")
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("git: %s %v", b, e)
	}
	return p
}
func cargoFixture(t *testing.T) (Request, *fakeRunner) {
	p := project(t)
	b, _ := json.Marshal(map[string]any{"version": 1, "workspace_root": p, "target_directory": filepath.Join(p, "target"), "build_directory": filepath.Join(p, "target")})
	f := &fakeRunner{version: "cargo 1.99.1 (fixture)", metadata: string(b)}
	return Request{CWD: p, Argv: []string{"cargo", "build"}, Env: []string{"PATH=/fixture", "SETTING=secret-value"}, Runner: f}, f
}
func TestCargoDiscoveryAndCompatibility(t *testing.T) {
	req, f := cargoFixture(t)
	req.Argv = []string{"cargo", "+1.99.1", "build", "--target-dir", "cache 한", "--config", "build.jobs=2", "--features=one", "--", "--target-dir", "ignored"}
	got, e := Discover(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Roots) != 1 || len(got.Roots[0].Kinds) != 2 || !got.Compatibility.Compatible(got.Compatibility) {
		t.Fatalf("bad result %+v", got)
	}
	meta := f.calls[1]
	joined := strings.Join(meta.args, "\n")
	for _, arg := range []string{"+1.99.1", "metadata", "build.target-dir=\"cache 한\"", "--features", "one", "build.jobs=2"} {
		if !strings.Contains(joined, arg) {
			t.Errorf("missing %s: %s", arg, joined)
		}
	}
	canonicalCWD, _ := filepath.EvalSymlinks(req.CWD)
	if strings.Contains(joined, "ignored") || meta.cwd != canonicalCWD {
		t.Fatal("incorrect discovery argv/cwd")
	}
	if envValue(meta.env, "CARGO_TARGET_DIR") != "cache 한" {
		t.Fatal("lost CLI override")
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "secret-value") || strings.Contains(string(encoded), "build.jobs") {
		t.Fatal("sensitive settings leaked")
	}
	changed := got.Compatibility
	changed.Platform = "different"
	if got.Compatibility.Compatible(changed) || got.Compatibility.Compatible(Compatibility{}) {
		t.Fatal("accepted mismatch/unknown")
	}
	req.Env = append(req.Env, "NEW_SETTING=1")
	next, e := Discover(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	if got.Compatibility.Compatible(next.Compatibility) {
		t.Fatal("ignored settings change")
	}
}
func TestCargoRejectsUnknownUnsafeAndUnsupported(t *testing.T) {
	for _, version := range []string{"cargo 1.99.0", "cargo 2.0.0", "cargo 1.100.0-nightly", "garbage"} {
		t.Run(version, func(t *testing.T) {
			r, f := cargoFixture(t)
			f.version = version
			if _, e := Discover(context.Background(), r); e == nil {
				t.Fatal("accepted version")
			}
		})
	}
	for _, kind := range []string{"missing-build", "malformed", "unsafe", "overlap", "bad-config"} {
		t.Run(kind, func(t *testing.T) {
			r, f := cargoFixture(t)
			switch kind {
			case "missing-build":
				f.metadata = `{"version":1}`
			case "malformed":
				f.metadata = "{"
			case "unsafe":
				b, _ := json.Marshal(map[string]any{"version": 1, "workspace_root": r.CWD, "target_directory": r.CWD, "build_directory": r.CWD})
				f.metadata = string(b)
			case "overlap":
				b, _ := json.Marshal(map[string]any{"version": 1, "workspace_root": r.CWD, "target_directory": filepath.Join(r.CWD, "target"), "build_directory": filepath.Join(r.CWD, "target", "build")})
				f.metadata = string(b)
			case "bad-config":
				os.Mkdir(filepath.Join(r.CWD, ".cargo"), 0700)
				os.WriteFile(filepath.Join(r.CWD, ".cargo", "config.toml"), []byte("bad = ["), 0600)
			}
			if _, e := Discover(context.Background(), r); e == nil {
				t.Fatal("accepted invalid discovery")
			}
		})
	}
}
func TestCargoConfigChangesAndMonorepo(t *testing.T) {
	r, _ := cargoFixture(t)
	sub := filepath.Join(r.CWD, "member")
	os.Mkdir(sub, 0700)
	r.CWD = sub
	config := filepath.Join(sub, ".cargo")
	os.Mkdir(config, 0700)
	p := filepath.Join(config, "config.toml")
	os.WriteFile(p, []byte("[build]\njobs=2\n"), 0600)
	a, e := Discover(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("[build]\njobs=3\n"), 0600)
	b, e := Discover(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if a.Compatibility.Compatible(b.Compatibility) {
		t.Fatal("ignored config")
	}
}
func frontendFixture(t *testing.T) (Request, *fakeRunner, map[string]any) {
	r, f := cargoFixture(t)
	r.Preset = Rspack
	r.Argv = []string{"rspack", "build"}
	os.Mkdir(filepath.Join(r.CWD, ".ccow"), 0700)
	os.WriteFile(filepath.Join(r.CWD, ".ccow", "discover.cjs"), []byte("fixture"), 0600)
	data := map[string]any{"nodeVersion": "24.0.0", "toolVersion": "2.3.0", "rspackVersion": "2.3.0", "result": map[string]any{"schema": 1, "preset": "rspack", "caches": []map[string]any{{"type": "persistent", "path": filepath.Join(r.CWD, ".cache", "rspack")}}, "outputs": []string{filepath.Join(r.CWD, "dist")}, "settings": map[string]any{"mode": "production"}}}
	return r, f, data
}
func TestFrontendIntegration(t *testing.T) {
	for _, kind := range []string{"valid", "node-old", "tool-old", "output", "dependencies", "unknown-settings", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			r, f, data := frontendFixture(t)
			result := data["result"].(map[string]any)
			switch kind {
			case "node-old":
				data["nodeVersion"] = "23.0.0"
			case "tool-old":
				data["toolVersion"] = "2.2.9"
			case "output":
				result["caches"] = []map[string]any{{"type": "persistent", "path": filepath.Join(r.CWD, "dist")}}
			case "dependencies":
				result["caches"] = []map[string]any{{"type": "persistent", "path": filepath.Join(r.CWD, "node_modules", ".cache")}}
			case "unknown-settings":
				delete(result, "settings")
			}
			b, _ := json.Marshal(data)
			f.frontend = string(b)
			if kind == "malformed" {
				f.frontend = "{"
			}
			got, e := Discover(context.Background(), r)
			if kind == "valid" {
				if e != nil || len(got.Roots) != 1 {
					t.Fatalf("%+v %v", got, e)
				}
			} else if e == nil {
				t.Fatal("accepted unsafe/unknown frontend")
			}
		})
	}
}
func TestPresetSelection(t *testing.T) {
	r, _ := cargoFixture(t)
	r.Argv[0] = "wrapper"
	if _, e := Discover(context.Background(), r); e == nil {
		t.Fatal("guessed wrapper preset")
	}
	r.Preset = Cargo
	if _, e := Discover(context.Background(), r); e != nil {
		t.Fatal(e)
	}
}

func TestEquivalentWorktreesAndDifferentCWD(t *testing.T) {
	r, f := cargoFixture(t)
	p := r.CWD
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", p}, args...)...)
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%s: %v", b, e)
		}
	}
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "init")
	w := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "-b", "linked", w)
	a, e := Discover(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	r.CWD = w
	b, _ := json.Marshal(map[string]any{"version": 1, "workspace_root": w, "target_directory": filepath.Join(w, "target"), "build_directory": filepath.Join(w, "target")})
	f.metadata = string(b)
	next, e := Discover(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if !a.Compatibility.Compatible(next.Compatibility) {
		t.Fatalf("equivalent worktrees differ: %+v %+v", a.Compatibility, next.Compatibility)
	}
	os.Mkdir(filepath.Join(w, "member"), 0700)
	r.CWD = filepath.Join(w, "member")
	nested, e := Discover(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if next.Compatibility.Compatible(nested.Compatibility) {
		t.Fatal("different project cwd treated as same settings")
	}
}
func TestNativeNodeProtocolWithFixturePackages(t *testing.T) {
	node, e := exec.LookPath("node")
	if e != nil {
		t.Skip("Node is unavailable")
	}
	r, _, _ := frontendFixture(t)
	r.Runner = ExecRunner{}
	r.Env = append(os.Environ(), "PATH="+filepath.Dir(node))
	r.Preset = Rsbuild
	r.Argv = []string{"rsbuild", "build", "--mode", "production"}
	for _, name := range []string{"@rspack/core", "@rsbuild/core"} {
		p := filepath.Join(r.CWD, "node_modules", name)
		os.MkdirAll(p, 0700)
		os.WriteFile(filepath.Join(p, "package.json"), []byte(`{"version":"2.3.0"}`), 0600)
	}
	script := `const path=require('node:path'); module.exports=async ({cwd,preset,argv}) => ({schema:1,preset,caches:[{type:'persistent',path:path.join(cwd,'.cache','test')}],outputs:[path.join(cwd,'dist')],settings:{mode:argv[3]}});`
	os.WriteFile(filepath.Join(r.CWD, ".ccow", "discover.cjs"), []byte(script), 0600)
	result, e := Discover(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if result.Preset != Rsbuild || !result.Compatibility.Verified() {
		t.Fatalf("bad result %+v", result)
	}
	// Probe failures must not expose arbitrary project stdout/stderr or argv.
	os.WriteFile(filepath.Join(r.CWD, ".ccow", "discover.cjs"), []byte(`throw new Error('secret-value')`), 0600)
	_, e = Discover(context.Background(), r)
	if e == nil || strings.Contains(e.Error(), "secret-value") {
		t.Fatalf("unsafe error %v", e)
	}
}
func TestCargoSeparatedRootsAndEnvironment(t *testing.T) {
	r, f := cargoFixture(t)
	r.Env = append(r.Env, "CARGO_TARGET_DIR=custom-target", "CARGO_BUILD_BUILD_DIR=custom-build")
	b, _ := json.Marshal(map[string]any{"version": 1, "workspace_root": r.CWD, "target_directory": filepath.Join(r.CWD, "custom-target"), "build_directory": filepath.Join(r.CWD, "custom-build")})
	f.metadata = string(b)
	got, e := Discover(context.Background(), r)
	if e != nil || len(got.Roots) != 2 {
		t.Fatalf("%+v %v", got, e)
	}
	if envValue(f.calls[1].env, "CARGO_BUILD_BUILD_DIR") != "custom-build" {
		t.Fatal("lost environment")
	}
}

func TestCargoRejectsUnverifiedConfigIndirection(t *testing.T) {
	for _, setting := range []string{`build.rustc="custom"`, `build={rustc="custom"}`, `include=["other.toml"]`, `env.RUSTC="custom"`, `build.rustc-wrapper="wrapper"`} {
		t.Run(setting, func(t *testing.T) {
			r, _ := cargoFixture(t)
			r.Argv = append(r.Argv, "--config", setting)
			if _, e := Discover(context.Background(), r); e == nil {
				t.Fatal("accepted unverified compiler/include")
			}
		})
	}
}
func TestCompatibilityRejectsMalformedRecords(t *testing.T) {
	r, _ := cargoFixture(t)
	got, e := Discover(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Compatibility){func(c *Compatibility) { c.Schema = 2 }, func(c *Compatibility) { c.ToolVersion = "unknown" }, func(c *Compatibility) { c.SettingsDigest = strings.Repeat("z", 64) }, func(c *Compatibility) { c.Architecture = "" }} {
		c := got.Compatibility
		change(&c)
		if c.Compatible(c) {
			t.Fatal("accepted malformed compatibility")
		}
	}
}

func TestSessionAndShellEnvironmentDoesNotInvalidateCompatibility(t *testing.T) {
	r, _ := cargoFixture(t)
	a, e := Discover(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	r.Env = append(r.Env, "CCOW_SESSION=session-token", "PWD=/different", "OLDPWD=/previous", "SHLVL=42", "_=/bin/other")
	b, e := Discover(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if !a.Compatibility.Compatible(b.Compatibility) {
		t.Fatal("volatile shell/session state invalidated compatibility")
	}
}
