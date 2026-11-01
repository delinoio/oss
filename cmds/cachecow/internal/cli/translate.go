// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// rootTranslation maps a current worktree root to the source worktree root inside environment
// values and arguments. Source discovery must describe the same build as the current command,
// so every path under the current root has to name the equivalent path under the source root.
//
// Only whole-path occurrences are rewritten: the root must not continue into another path
// component (/t/repo-wt/x stays unchanged for /t/repo) and must not be the tail of a longer
// path (/a/t/repo/x and /backup-/t/repo/x stay unchanged). The root must start a token, either at
// the start of the value or after whitespace, a quote, '=', or a path-list separator, optionally
// behind the direnv "-" marker. The translation is purely lexical; roots must be canonical.
type rootTranslation struct{ from, to string }

func (t rootTranslation) enabled() bool { return t.from != "" && t.from != t.to }

// isRootBoundary reports whether b separates tokens, so a root that follows it starts a
// standalone path. Filename punctuation such as '-', '+', or '@' is not a boundary: a root that
// follows it is the tail of a longer path (/backup-/t/repo/cargo for /t/repo).
func isRootBoundary(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '"', '\'', '`', '=', ':', ';', ',', '(', ')', '<', '>', '|', '&':
		return true
	}
	return b == os.PathListSeparator
}

// isRootStart reports whether the root match beginning at start is a standalone path. A single
// hyphen directly before the root is the direnv marker ("-/t/repo" in DIRENV_DIR) only when the
// hyphen itself starts a token; a hyphen inside another path component is filename punctuation.
func isRootStart(s string, start int) bool {
	if start == 0 {
		return true
	}
	if s[start-1] == '-' {
		return start == 1 || isRootBoundary(s[start-2])
	}
	return isRootBoundary(s[start-1])
}

// isRootEnd reports whether the byte after a root match ends the path.
func isRootEnd(b byte) bool {
	return b == '/' || b == filepath.Separator || b == os.PathListSeparator
}

// value rewrites every whole-path occurrence of the current root in s.
func (t rootTranslation) value(s string) string {
	if !t.enabled() || !strings.Contains(s, t.from) {
		return s
	}
	var out strings.Builder
	last := 0
	for i := 0; i+len(t.from) <= len(s); {
		j := strings.Index(s[i:], t.from)
		if j < 0 {
			break
		}
		start := i + j
		end := start + len(t.from)
		i = start + 1
		if !isRootStart(s, start) {
			continue
		}
		if end < len(s) && !isRootEnd(s[end]) {
			continue
		}
		out.WriteString(s[last:start])
		out.WriteString(t.to)
		last = end
		i = end
	}
	out.WriteString(s[last:])
	return out.String()
}

// args returns a translated copy of argv.
func (t rootTranslation) args(argv []string) []string {
	out := make([]string, len(argv))
	changed := 0
	for i, v := range argv {
		out[i] = t.value(v)
		if out[i] != v {
			changed++
		}
	}
	if changed > 0 {
		slog.Debug("translated command arguments into the source worktree", "changed", changed, "from_root", t.from, "to_root", t.to)
	}
	return out
}

// env returns a translated copy of KEY=value entries. Keys are never rewritten.
func (t rootTranslation) env(env []string) []string {
	out := make([]string, len(env))
	changed := 0
	for i, entry := range env {
		out[i] = entry
		// A leading '=' belongs to the key on Windows ("=C:=C:\dir").
		if k := strings.IndexByte(entry[min(1, len(entry)):], '='); k >= 0 {
			k++
			out[i] = entry[:k+1] + t.value(entry[k+1:])
		}
		if out[i] != entry {
			changed++
		}
	}
	if changed > 0 {
		slog.Debug("translated environment into the source worktree", "changed", changed, "from_root", t.from, "to_root", t.to)
	}
	return out
}

// envValue returns the last value of key in env. Windows environment keys are case-insensitive.
func envValue(env []string, key string) string {
	value := ""
	for _, entry := range env {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if k == key || (runtime.GOOS == "windows" && strings.EqualFold(k, key)) {
			value = v
		}
	}
	return value
}

// lookPath finds name through the PATH of env. The process PATH keeps using exec.LookPath;
// a different PATH (for example one translated into a source worktree) is searched without
// changing the process environment.
func lookPath(name string, env []string) (string, error) {
	path := envValue(env, "PATH")
	if path == os.Getenv("PATH") {
		return exec.LookPath(name)
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		// exec.LookPath checks a name that contains a separator directly and applies PATHEXT on Windows.
		if found, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return found, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
