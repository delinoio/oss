// SPDX-License-Identifier: Apache-2.0
package preset

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/cachecow/internal/repository"
	"github.com/pelletier/go-toml/v2"
)

// Cargo metadata owns config precedence, workspace selection, relative paths,
// and build-dir templates. Never reconstruct those rules from Cargo.toml.
// https://doc.rust-lang.org/cargo/commands/cargo-metadata.html
func discoverCargo(ctx context.Context, req Request, out *Result) (any, error) {
	for _, key := range []string{"CARGO_BUILD_RUSTC", "RUSTC_WRAPPER", "RUSTC_WORKSPACE_WRAPPER", "CARGO_BUILD_RUSTC_WRAPPER", "CARGO_BUILD_RUSTC_WORKSPACE_WRAPPER"} {
		if envValue(req.Env, key) != "" {
			return nil, errors.New("custom compiler wrappers cannot yet be verified; use the selected toolchain without wrappers")
		}
	}
	prefix := []string{req.Argv[0]}
	start := 1
	if len(req.Argv) > 1 && strings.HasPrefix(req.Argv[1], "+") {
		prefix = append(prefix, req.Argv[1])
		start++
	}
	b, e := req.Runner.Output(ctx, req.CWD, req.Env, append(append([]string{}, prefix...), "--version"))
	if e != nil {
		return nil, e
	}
	words := strings.Fields(string(b))
	if len(words) < 2 || words[0] != "cargo" || !supported(words[1], "1.99.1", "2.0.0") {
		return nil, errors.New("Cargo requires stable 1.99.1 or later within 1.x; select a supported toolchain")
	}
	out.Compatibility.ToolVersion = words[1]
	args := append(append([]string{}, prefix...), "metadata", "--format-version", "1", "--no-deps")
	env := req.Env
	var extraConfigs []string
	// Forward only discovery-relevant options. Runtime arguments remain untouched.
	// Directory CLI options become highest-precedence --config overrides, because
	// metadata does not accept every build option.
	for i := start; i < len(req.Argv); i++ {
		arg := req.Argv[i]
		if strings.HasPrefix(arg, "-Z") || strings.HasPrefix(arg, "-C") {
			return nil, errors.New("unstable Cargo options cannot be verified; use stable Cargo without -Z or -C")
		}
		if arg == "--" {
			break
		}
		key, value, inline := strings.Cut(arg, "=")
		switch key {
		case "--manifest-path", "--config", "--target-dir", "--build-dir", "--features", "-F", "--target":
			if !inline {
				i++
				if i >= len(req.Argv) {
					return nil, errors.New("Cargo discovery option has no value; correct the build command")
				}
				value = req.Argv[i]
			}
			if value == "" {
				return nil, errors.New("Cargo discovery option has an empty value; correct the build command")
			}
			switch key {
			case "--config":
				if strings.Contains(value, "=") {
					if err := validateConfig([]byte(value)); err != nil {
						return nil, err
					}
				}
				args = append(args, key, value)
			case "--target-dir":
				env = setEnv(env, "CARGO_TARGET_DIR", value)
				extraConfigs = append(extraConfigs, "build.target-dir="+tomlString(value))
			case "--build-dir":
				extraConfigs = append(extraConfigs, "build.build-dir="+tomlString(value))
			case "--target":
				args = append(args, "--filter-platform", value)
			default:
				args = append(args, key, value)
			}
		case "--all-features", "--no-default-features", "--offline", "--locked", "--frozen":
			args = append(args, key)
		case "-Z", "--unstable-features", "-C":
			return nil, errors.New("unstable Cargo settings are unsupported; select stable Cargo")
		}
	}
	for _, config := range extraConfigs {
		args = append(args, "--config", config)
	}
	b, e = req.Runner.Output(ctx, req.CWD, env, args)
	if e != nil {
		return nil, e
	}
	var meta struct {
		Version   int    `json:"version"`
		Target    string `json:"target_directory"`
		Build     string `json:"build_directory"`
		Workspace string `json:"workspace_root"`
		Packages  []struct {
			Manifest string `json:"manifest_path"`
		} `json:"packages"`
	}
	if json.Unmarshal(b, &meta) != nil || meta.Version != 1 || !filepath.IsAbs(meta.Target) || !filepath.IsAbs(meta.Build) || !filepath.IsAbs(meta.Workspace) {
		return nil, errors.New("Cargo metadata did not report verified target_directory and build_directory; use a stable Cargo that reports both fields (no target-dir fallback is safe)")
	}
	// Metadata from a manifest outside this repository must not authorize its cache.
	workspace, e := repository.Discover(ctx, meta.Workspace)
	if e != nil || workspace.CommonDir != out.Repository.CommonDir {
		return nil, errors.New("Cargo workspace is outside the selected Git repository; correct --manifest-path")
	}
	meta.Workspace, e = repository.Canonical(meta.Workspace)
	if e != nil {
		return nil, e
	}
	out.Roots = []Root{{meta.Target, []RootKind{Target}}, {meta.Build, []RootKind{Build}}}
	rustc := envValue(req.Env, "RUSTC")
	if rustc == "" {
		rustc = "rustc"
	}
	compilerArgs := []string{rustc}
	if len(prefix) > 1 && rustc == "rustc" {
		compilerArgs = append(compilerArgs, prefix[1])
	}
	compilerArgs = append(compilerArgs, "-vV")
	b, e = req.Runner.Output(ctx, req.CWD, req.Env, compilerArgs)
	if e != nil {
		return nil, e
	}
	compilerFields := strings.Fields(string(b))
	if len(compilerFields) < 2 || !supported(compilerFields[1], "1.99.1", "2.0.0") || !strings.HasPrefix(string(b), "rustc ") || !strings.Contains(string(b), "\nhost: ") || strings.Contains(string(b), "nightly") {
		return nil, errors.New("cannot verify a stable rustc compiler; check the selected Rust toolchain")
	}
	out.Compatibility.CompilerVersion = digest(string(b))
	// Hash configuration bytes without storing them. Cargo still decides source
	// freshness; source files and artifact timestamps are never rewritten.
	configs := map[string]string{}
	for p := req.CWD; ; p = filepath.Dir(p) {
		for _, name := range []string{"config", "config.toml"} {
			path := filepath.Join(p, ".cargo", name)
			if e = hashFile(configs, path); e != nil {
				return nil, e
			}
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	home := envValue(req.Env, "CARGO_HOME")
	if home == "" {
		home = envValue(req.Env, "HOME")
		if home == "" {
			home = envValue(req.Env, "USERPROFILE")
		}
		if home == "" {
			home, e = os.UserHomeDir()
			if e != nil {
				return nil, errors.New("cannot resolve Cargo home; set CARGO_HOME")
			}
		}
		home = filepath.Join(home, ".cargo")
	} else if !filepath.IsAbs(home) {
		home = filepath.Join(req.CWD, home)
	}
	for _, name := range []string{"config", "config.toml"} {
		if e = hashFile(configs, filepath.Join(home, name)); e != nil {
			return nil, e
		}
	}
	// File-form --config can live outside the usual hierarchy.
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--config" && !strings.Contains(args[i+1], "=") {
			p := args[i+1]
			if !filepath.IsAbs(p) {
				p = filepath.Join(req.CWD, p)
			}
			if e = hashFile(configs, p); e != nil {
				return nil, e
			}
		}
	}
	// Preserve configuration position and precedence in the digest, while
	// allowing identical configuration in corresponding linked worktrees.
	manifests := map[string]string{}
	paths := []string{filepath.Join(meta.Workspace, "Cargo.toml")}
	for _, pkg := range meta.Packages {
		paths = append(paths, pkg.Manifest)
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, errors.New("cannot verify Cargo manifest settings; check permissions")
		}
		canonical, err := repository.Canonical(path)
		if err != nil {
			return nil, err
		}
		manifests[strings.ReplaceAll(canonical, out.Repository.Root, "${WORKTREE}")] = digest(string(b))
	}
	normalized := map[string]string{}
	for path, value := range configs {
		normalized[strings.ReplaceAll(path, out.Repository.Root, "${WORKTREE}")] = value
	}
	return struct {
		Configs   map[string]string
		Manifests map[string]string
		Workspace string
	}{normalized, manifests, relativeProject(out.Repository.Root, meta.Workspace)}, nil
}
func tomlString(s string) string { b, _ := json.Marshal(s); return string(b) }
func hashFile(dst map[string]string, path string) error {
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return errors.New("cannot read Cargo configuration; check permissions")
	}
	if e = validateConfig(b); e != nil {
		return e
	}
	dst[path] = digest(string(b))
	return nil
}
func validateConfig(b []byte) error {
	var config map[string]any
	if toml.Unmarshal(b, &config) != nil {
		return errors.New("malformed Cargo configuration; correct the TOML file")
	}
	if _, ok := config["include"]; ok {
		return errors.New("Cargo config includes cannot yet be verified; use self-contained configuration")
	}
	if build, ok := config["build"].(map[string]any); ok {
		for _, key := range []string{"rustc", "rustc-wrapper", "rustc-workspace-wrapper"} {
			if _, ok := build[key]; ok {
				return errors.New("configured compiler or wrapper cannot yet be verified; use the selected toolchain")
			}
		}
	}
	if env, ok := config["env"].(map[string]any); ok {
		for _, key := range []string{"RUSTC", "RUSTUP_TOOLCHAIN", "RUSTC_WRAPPER", "RUSTC_WORKSPACE_WRAPPER"} {
			if _, ok := env[key]; ok {
				return errors.New("compiler overrides in Cargo config env cannot yet be verified")
			}
		}
	}
	return nil
}

func relativeProject(root, path string) string {
	p, e := filepath.Rel(root, path)
	if e != nil {
		return path
	}
	return p
}
