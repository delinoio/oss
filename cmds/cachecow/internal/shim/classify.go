// SPDX-License-Identifier: Apache-2.0

// Package shim owns optional tool entry points. It does not execute commands or
// acquire cache locks; callers route Managed and Clean through the shared lifecycle.
package shim

import (
	"path/filepath"
	"strings"
)

type Tool string

const (
	Cargo   Tool = "cargo"
	Rspack  Tool = "rspack"
	Rsbuild Tool = "rsbuild"
)

func Tools() []Tool        { return []Tool{Cargo, Rspack, Rsbuild} }
func (t Tool) Valid() bool { return t == Cargo || t == Rspack || t == Rsbuild }

// FromArgv0 recognizes installed native copies, including Windows executable names.
func FromArgv0(argv0 string) (Tool, bool) {
	t := Tool(strings.TrimSuffix(filepath.Base(argv0), ".exe"))
	return t, t.Valid()
}

type Mode string

const (
	Passthrough Mode = "passthrough"
	Managed     Mode = "managed"
	Clean       Mode = "clean" // Lock and wait, but never prepare or clone.
)

type Reason string

const (
	CacheOperation Reason = "cache-operation"
	CleanOperation Reason = "clean-operation"
	Query          Reason = "query"
	Unknown        Reason = "unknown-command"
	OutsideGit     Reason = "outside-git"
	Bypass         Reason = "bypass"
)

type Classification struct {
	Mode   Mode
	Reason Reason
}

// Classify only inspects args. It never removes or rewrites them. The caller must
// pass the original argv to the selected executable, including +toolchain.
func Classify(tool Tool, args []string, inGit bool, bypass string) Classification {
	pass := func(r Reason) Classification { return Classification{Passthrough, r} }
	if bypass == "1" {
		return pass(Bypass)
	}
	if !inGit {
		return pass(OutsideGit)
	}
	if !tool.Valid() {
		return pass(Unknown)
	}
	// Help/version flags after -- belong to the executed program, not the tool.
	for _, arg := range args {
		if arg == "--" {
			break
		}
		switch arg {
		case "--help", "-h", "--version", "-V":
			return pass(Query)
		}
	}
	i := 0
	if tool == Cargo && len(args) > 0 && strings.HasPrefix(args[0], "+") {
		i++
	}
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		a := args[i]
		if tool != Cargo {
			// Frontend CLIs accept common options before the subcommand. Rspack
			// also uses its default build command when only build options occur.
			switch a {
			case "--watch", "-w", "--hot", "--no-hot", "--open", "--no-open":
				i++
			case "--config", "-c", "--mode", "--env", "--config-name", "--entry", "--output-path", "--port", "--host":
				if i+1 >= len(args) {
					return pass(Unknown)
				}
				i += 2
			default:
				matched := false
				for _, prefix := range []string{"--config=", "--mode=", "--env=", "--config-name=", "--entry=", "--output-path=", "--port=", "--host="} {
					if strings.HasPrefix(a, prefix) {
						matched = true
						break
					}
				}
				if !matched {
					return pass(Unknown)
				}
				i++
			}
			continue
		}
		switch {
		case a == "--verbose" || a == "--quiet" || a == "-q" || a == "--locked" || a == "--offline" || a == "--frozen" || (strings.HasPrefix(a, "-v") && strings.Trim(a[1:], "v") == ""):
			i++
		case a == "--color" || a == "--config" || a == "-C" || a == "-Z":
			if i+1 >= len(args) {
				return pass(Unknown)
			}
			i += 2
		case strings.HasPrefix(a, "--color=") || strings.HasPrefix(a, "--config=") || strings.HasPrefix(a, "-Z") || strings.HasPrefix(a, "-C"):
			i++
		default:
			return pass(Unknown)
		}
	}
	if i == len(args) {
		if tool == Rspack {
			return Classification{Managed, CacheOperation}
		}
		return pass(Query)
	}
	command := args[i]
	if tool == Cargo {
		switch command {
		case "clean":
			return Classification{Clean, CleanOperation}
		case "build", "b", "check", "c", "test", "t", "bench", "run", "r", "doc", "d", "rustc", "rustdoc", "clippy", "fix":
			return Classification{Managed, CacheOperation}
		case "help", "version", "metadata", "locate-project", "read-manifest", "pkgid", "tree", "search", "info", "verify-project":
			return pass(Query)
		}
	} else {
		switch command {
		case "build", "dev":
			return Classification{Managed, CacheOperation}
		case "serve", "watch":
			if tool == Rspack {
				return Classification{Managed, CacheOperation}
			}
		case "help", "version", "inspect", "preview":
			return pass(Query)
		}
	}
	return pass(Unknown)
}
