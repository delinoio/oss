package runmoor

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const diagnosticLimit int64 = 256 << 20

type LogWriter struct {
	mu     sync.Mutex
	dir    string
	output io.Writer
	color  bool
	format string
}

func NewLogger(c Config, out io.Writer) (*slog.Logger, error) {
	dir := filepath.Join(c.Storage.State, "diagnostics")
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	w := &LogWriter{dir: dir, output: out, color: !c.Logging.NoColor && os.Getenv("NO_COLOR") == "" && c.Logging.Format == "text", format: c.Logging.Format}
	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(c.Logging.Level))
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewTextHandler(w, opts)
	if c.Logging.Format == "json" {
		h = slog.NewJSONHandler(w, opts)
	}
	if err := PruneDiagnostics(dir, time.Now(), 0); err != nil {
		return nil, err
	}
	return slog.New(h), nil
}
func (w *LogWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := PruneDiagnostics(w.dir, time.Now(), int64(len(b))); err != nil {
		return 0, err
	}
	path := filepath.Join(w.dir, time.Now().UTC().Format("20060102")+".log")
	f, e := openPrivate(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND)
	if e != nil {
		return 0, e
	}
	_, e = f.Write(b)
	closeErr := f.Close()
	if e != nil {
		return 0, e
	}
	if closeErr != nil {
		return 0, closeErr
	}
	out := b
	if w.color {
		out = bytes.ReplaceAll(out, []byte("level=ERROR"), []byte("level=\x1b[31mERROR\x1b[0m"))
		out = bytes.ReplaceAll(out, []byte("level=WARN"), []byte("level=\x1b[33mWARN\x1b[0m"))
		out = bytes.ReplaceAll(out, []byte("level=INFO"), []byte("level=\x1b[32mINFO\x1b[0m"))
	}
	_, e = w.output.Write(out)
	return len(b), e
}

var diagnosticMu sync.Mutex

func PruneDiagnostics(dir string, now time.Time, reserve int64) error {
	diagnosticMu.Lock()
	defer diagnosticMu.Unlock()
	return pruneDiagnostics(dir, now, reserve)
}
func pruneDiagnostics(dir string, now time.Time, reserve int64) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return problem(ErrState, "Cannot inspect diagnostic retention.", "Check the private diagnostics directory.")
	}
	var files []os.FileInfo
	var total int64
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 || e.IsDir() {
			continue
		}
		info, er := e.Info()
		if er != nil {
			return problem(ErrState, "Cannot inspect a diagnostic file.", "Check filesystem permissions.")
		}
		if now.Sub(info.ModTime()) > 7*24*time.Hour {
			if er = os.Remove(filepath.Join(dir, e.Name())); er != nil {
				return problem(ErrState, "Expired diagnostic removal failed.", "Check permissions and free disk space.")
			}
			continue
		}
		files = append(files, info)
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime().Before(files[j].ModTime()) })
	for _, f := range files {
		if total+reserve <= diagnosticLimit {
			break
		}
		if err = os.Remove(filepath.Join(dir, f.Name())); err != nil {
			return problem(ErrState, "Diagnostic quota cleanup failed.", "Check permissions and free disk space.")
		}
		total -= f.Size()
	}
	return nil
}

// Only structured manager observations are preserved. Raw runner/worker logs
// can contain arbitrary workflow output and must never be copied verbatim.
func SaveRunnerDiagnostic(c Config, r Runner, exit *int) error {
	dir := filepath.Join(c.Storage.State, "diagnostics")
	if err := privateDir(dir); err != nil {
		return err
	}
	data, _ := json.Marshal(struct {
		Runner  string      `json:"runner"`
		Pool    string      `json:"pool"`
		Backend Backend     `json:"backend"`
		Phase   RunnerPhase `json:"phase"`
		Exit    *int        `json:"exit_code,omitempty"`
		Problem *Problem    `json:"problem,omitempty"`
		At      time.Time   `json:"at"`
	}{r.ID, r.PoolID, r.Backend, r.Phase, exit, r.Problem, time.Now().UTC()})
	diagnosticMu.Lock()
	defer diagnosticMu.Unlock()
	if err := pruneDiagnostics(dir, time.Now(), int64(len(data))); err != nil {
		return err
	}
	f, e := openPrivate(filepath.Join(dir, r.ID+".json"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if e != nil {
		return e
	}
	_, e = f.Write(data)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func logProblem(l *slog.Logger, event string, p *Problem) {
	if p == nil {
		return
	}
	l.Warn(event, "code", p.Code, "pool", p.Pool, "runner", p.Runner, "http_status", p.HTTPStatus)
}
func scrubName(s string) string {
	if safeName.MatchString(s) {
		return s
	}
	return "invalid"
}
func minimalEnv() []string {
	v := []string{"PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin", "LANG=C", "LC_ALL=C"}
	if h, e := os.UserHomeDir(); e == nil {
		v = append(v, "HOME="+h)
	}
	return v
}
func safeOutput(b []byte) bool { return len(b) < 4096 && !strings.ContainsAny(string(b), "\x00\x1b") }
