// SPDX-License-Identifier: Apache-2.0
// Package executionenv carries bounded user-tool authority independently of
// private native provider configuration. It never opens credential files.
package executionenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Ordinary is an in-memory context supplied only by an executing Worker.
// Its zero value grants no user configuration access; never serialize it.
type Ordinary struct{ ghDirectory string }

// Current resolves the original Worker environment and process directory.
// A missing or invalid directory retains gh's ordinary unauthenticated failure.
func Current() Ordinary {
	cwd, err := os.Getwd()
	if err != nil {
		return Ordinary{}
	}
	return Resolve(runtime.GOOS, os.Environ(), cwd)
}

// Resolve follows gh's configuration precedence without checking, creating,
// reading or owning that directory. Relative values bind to the Worker cwd.
func Resolve(goos string, env []string, cwd string) Ordinary {
	get := func(name string) string {
		for _, entry := range env {
			key, value, ok := strings.Cut(entry, "=")
			if ok && (key == name || goos == "windows" && strings.EqualFold(key, name)) {
				return value
			}
		}
		return ""
	}
	directory := get("GH_CONFIG_DIR")
	if directory == "" {
		if xdg := get("XDG_CONFIG_HOME"); xdg != "" {
			directory = filepath.Join(xdg, "gh")
		} else if appdata := get("APPDATA"); goos == "windows" && appdata != "" {
			directory = filepath.Join(appdata, "GitHub CLI")
		} else {
			home := get("HOME")
			if goos == "windows" {
				home = get("USERPROFILE")
			}
			if home != "" {
				directory = filepath.Join(home, ".config", "gh")
			}
		}
	}
	if directory == "" || strings.ContainsAny(directory, "\x00\r\n") || !filepath.IsAbs(cwd) {
		return Ordinary{}
	}
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(cwd, directory)
	}
	return Ordinary{filepath.Clean(directory)}
}

// Apply runs after each adapter rebuilds its private environment. It copies
// only a directory selector; gh itself owns configuration and OS-store access.
func (c Ordinary) Apply(env []string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "GH_CONFIG_DIR") || strings.EqualFold(key, "GH_TOKEN") || strings.EqualFold(key, "GITHUB_TOKEN") {
			continue
		}
		result = append(result, entry)
	}
	if c.ghDirectory != "" {
		result = append(result, "GH_CONFIG_DIR="+c.ghDirectory)
	}
	return result
}

// Available is a safe diagnostic classification, never a path or login proof.
func (c Ordinary) Available() bool { return c.ghDirectory != "" }
