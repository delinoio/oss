package core

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Redactor holds enough suffix bytes to recognize a secret split between writes.
// Nothing from the held suffix reaches disk until it is known to be safe.
type Redactor struct {
	mu      sync.Mutex
	out     io.Writer
	secrets []string
	pending []byte
	max     int
	err     error
}

func NewRedactor(w io.Writer, secrets []string) *Redactor {
	r := &Redactor{out: w}
	for _, s := range secrets {
		if s != "" {
			r.secrets = append(r.secrets, s)
			if len(s) > r.max {
				r.max = len(s)
			}
		}
	}
	return r
}
func (r *Redactor) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return 0, r.err
	}
	r.pending = append(r.pending, b...)
	r.flush(false)
	return len(b), r.err
}
func (r *Redactor) flush(final bool) {
	if len(r.secrets) == 0 {
		_, r.err = r.out.Write(r.pending)
		r.pending = nil
		return
	}
	var safe bytes.Buffer
	defer func() {
		if r.err == nil && safe.Len() > 0 {
			_, r.err = r.out.Write(safe.Bytes())
		}
	}()
	for len(r.pending) > 0 {
		if !final && len(r.pending) < r.max {
			return
		}
		longest := 0
		for _, s := range r.secrets {
			if bytes.HasPrefix(r.pending, []byte(s)) && len(s) > longest {
				longest = len(s)
			}
		}
		if longest > 0 {
			safe.WriteString("[REDACTED]")
			r.pending = r.pending[longest:]
		} else {
			safe.WriteByte(r.pending[0])
			r.pending = r.pending[1:]
		}
		if r.err != nil {
			return
		}
	}
}
func (r *Redactor) Close() error { r.mu.Lock(); defer r.mu.Unlock(); r.flush(true); return r.err }
func Redact(b []byte, secrets []string) []byte {
	var out bytes.Buffer
	r := NewRedactor(&out, secrets)
	_, _ = r.Write(b)
	_ = r.Close()
	return out.Bytes()
}

func ParseReport(kind ReportKind, b []byte, check, command, logID string) ([]Failure, error) {
	switch kind {
	case JUnit:
		return parseJUnit(b, check, command, logID)
	case GoTest:
		return parseGoTest(b, check, command, logID)
	}
	return nil, E("invalid-report-kind", "unsupported report kind", 2)
}
func parseJUnit(b []byte, check, command, logID string) ([]Failure, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	out := []Failure{}
	type suiteIdentity struct {
		Kind, Name string
		Occurrence int
	}
	suites := []suiteIdentity{}
	occurrences := map[string]int{}
	testOccurrence := 0
	failureOccurrences := map[string]int{}
	test := ""
	class := ""
	file := ""
	line := 0
	sawRoot := false
	declaredFailures := 0
	depth := 0
	for {
		token, e := d.Token()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return nil, E("report-malformed", "JUnit XML cannot be parsed", 1)
		}
		switch t := token.(type) {
		case xml.Directive:
			return nil, E("report-malformed", "XML directives are not supported", 1)
		case xml.EndElement:
			depth--
			if (t.Name.Local == "testsuite" || t.Name.Local == "testsuites") && len(suites) > 0 {
				suites = suites[:len(suites)-1]
			}
			if t.Name.Local == "testcase" {
				test, class, file, line, testOccurrence = "", "", "", 0, 0
				failureOccurrences = map[string]int{}
			}
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(t)) != 0 {
				return nil, E("report-malformed", "text outside JUnit root", 1)
			}
		case xml.StartElement:
			if depth == 0 {
				if sawRoot || (t.Name.Local != "testsuite" && t.Name.Local != "testsuites") {
					return nil, E("report-malformed", "expected one JUnit testsuite root", 1)
				}
			}
			depth++
			attrs := map[string]string{}
			for _, a := range t.Attr {
				attrs[a.Name.Local] = a.Value
			}
			switch t.Name.Local {
			case "testsuites", "testsuite":
				sawRoot = true
				key := string(Encode([]any{suites, t.Name.Local, attrs["name"]}))
				occurrences[key]++
				suites = append(suites, suiteIdentity{t.Name.Local, attrs["name"], occurrences[key]})
				for _, k := range []string{"failures", "errors"} {
					if attrs[k] != "" {
						v, err := strconv.Atoi(attrs[k])
						if err != nil || v < 0 {
							return nil, E("report-malformed", "invalid JUnit failure count", 1)
						}
						if v > declaredFailures {
							declaredFailures = v
						}
					}
				}
			case "testcase":
				test = attrs["name"]
				class = attrs["classname"]
				file = attrs["file"]
				line, _ = strconv.Atoi(attrs["line"])
				key := string(Encode([]any{suites, "testcase", class, test}))
				occurrences[key]++
				testOccurrence = occurrences[key]
				failureOccurrences = map[string]int{}
			case "failure", "error":
				var body string
				if e = d.DecodeElement(&body, &t); e != nil {
					return nil, E("report-malformed", "invalid JUnit failure", 1)
				}
				depth--
				names := []string{}
				for _, suite := range suites {
					if suite.Name != "" {
						names = append(names, suite.Name)
					}
				}
				identity := strings.Join(append(names, class, test), "/")
				failureOccurrences[t.Name.Local]++
				id := Hash(Encode([]any{check, "junit", suites, class, test, testOccurrence, t.Name.Local, failureOccurrences[t.Name.Local]}))
				message := strings.TrimSpace(attrs["message"] + "\n" + body)
				out = append(out, Failure{ID: id, Check: check, Test: identity, Command: command, Message: message, File: file, Line: line, LogID: logID})
			}
		}
	}
	if !sawRoot {
		return nil, E("report-malformed", "JUnit report has no testsuite root", 1)
	}
	if declaredFailures > 0 && len(out) == 0 {
		out = append(out, Failure{ID: Hash([]byte(check + "/junit/summary")), Check: check, Command: command, Message: "JUnit summary reports failures without test details", LogID: logID})
	}
	return out, nil
}
func parseGoTest(b []byte, check, command, logID string) ([]Failure, error) {
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	out := []Failure{}
	seen := false
	terminal := false
	output := map[string]*reportOutputTail{}
	tests := map[string]bool{}
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var event struct{ Action, Package, Test, Output string }
		if e := json.Unmarshal(scanner.Bytes(), &event); e != nil || event.Action == "" {
			return nil, E("report-malformed", "invalid Go test JSON event", 1)
		}
		seen = true
		key := event.Package + "/" + event.Test
		switch event.Action {
		case "output":
			if output[key] == nil {
				output[key] = &reportOutputTail{}
			}
			output[key].append(event.Output)
		case "run", "start":
			tests[key] = false
		case "pass", "skip", "fail":
			terminal = true
			tests[key] = true
			if event.Action == "fail" {
				out = append(out, Failure{ID: Hash([]byte(check + "/go/" + key)), Check: check, Test: key, Command: command, Message: strings.TrimSpace(output[key].String()), LogID: logID})
			}
			delete(output, key)
		case "pause", "cont", "bench":
		default:
			return nil, E("report-malformed", "unknown Go test JSON action", 1)
		}
	}
	if scanner.Err() != nil || !seen || !terminal {
		return nil, E("report-malformed", "Go test report is empty, truncated or incomplete", 1)
	}
	for _, done := range tests {
		if !done {
			return nil, E("report-incomplete", "Go test report contains unfinished tests", 1)
		}
	}
	return out, nil
}
func (s *Service) SaveEvidence(run, name string, b []byte) (Evidence, error) {
	id := ID()
	path, e := s.Store.EvidencePath(run, id)
	if e != nil {
		return Evidence{}, e
	}
	if e = AtomicWrite(path, b, 0600); e != nil {
		return Evidence{}, e
	}
	return Evidence{ID: id, Name: name, SHA256: Hash(b), Size: int64(len(b))}, nil
}

// Declared report paths are outputs owned by one check, never committed or
// prerequisite evidence. Remove old files before the command can create them.
func clearReportOutputs(workspace string, reports []Report) error {
	if len(reports) == 0 {
		return nil
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, report := range reports {
		if !SafeRelative(report.Path) {
			return E("unsafe-path", "invalid report output", 2)
		}
		info, err := root.Lstat(report.Path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return E("unsafe-file", "report output is not a file", 2)
		}
		if err = root.Remove(report.Path); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) ValidateEvidence(run string, c Check) error {
	if c.InheritedFrom != "" {
		run = c.InheritedFrom
	}
	if c.Log.ID == "" {
		return E("evidence-missing", "log evidence is absent", 1)
	}
	all := append([]Evidence{c.Log}, c.Reports...)
	for _, v := range all {
		path, e := s.Store.EvidencePath(run, v.ID)
		if e != nil {
			return e
		}
		root, e := os.OpenRoot(s.Store.Root)
		if e != nil {
			return e
		}
		f, e := root.Open(filepath.Join("evidence", run, filepath.Base(path)))
		root.Close()
		if e != nil {
			return e
		}
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() != v.Size {
			f.Close()
			return E("evidence-missing", "evidence is unavailable or changed", 1)
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil || hex.EncodeToString(h.Sum(nil)) != v.SHA256 {
			return E("evidence-integrity", "evidence failed integrity verification", 1)
		}
	}
	return nil
}
func (s *Service) Logs(run, check string, offset int64, limit int) (LogPage, error) {
	if offset < 0 || limit < 1 || limit > 1024*1024 {
		return LogPage{}, E("invalid-log-range", "offset must be nonnegative and limit 1..1048576", 2)
	}
	r, e := s.Store.Run(run)
	if e != nil {
		return LogPage{}, e
	}
	var selected *Check
	for i := range r.Checks {
		if r.Checks[i].Name == check || r.Checks[i].ID == check {
			selected = &r.Checks[i]
		}
	}
	if selected == nil {
		return LogPage{}, E("check-not-found", "select a check from this run", 2)
	}
	if selected.InheritedFrom != "" {
		run = selected.InheritedFrom
	}
	id := selected.Log.ID
	if id == "" {
		return LogPage{Complete: selected.State.Terminal()}, nil
	}
	return s.evidencePage(run, id, offset, limit, selected.State.Terminal())
}
func (s *Service) Report(run, id string, offset int64, limit int) (LogPage, error) {
	r, err := s.Store.Run(run)
	if err != nil {
		return LogPage{}, err
	}
	for _, c := range r.Checks {
		for _, report := range c.Reports {
			if report.ID == id {
				if c.InheritedFrom != "" {
					run = c.InheritedFrom
				}
				return s.evidencePage(run, id, offset, limit, true)
			}
		}
	}
	return LogPage{}, E("report-not-found", "select a report owned by this execution", 2)
}
func (s *Service) evidencePage(run, id string, offset int64, limit int, terminal bool) (LogPage, error) {
	if offset < 0 || limit < 1 || limit > 1024*1024 {
		return LogPage{}, E("invalid-evidence-range", "offset must be nonnegative and limit 1..1048576", 2)
	}
	path, e := s.Store.EvidencePath(run, id)
	if e != nil {
		return LogPage{}, e
	}
	root, e := os.OpenRoot(s.Store.Root)
	if e != nil {
		return LogPage{}, E("evidence-missing", "log evidence is unavailable", 1)
	}
	defer root.Close()
	f, e := root.Open(filepath.Join("evidence", run, filepath.Base(path)))
	if e != nil {
		return LogPage{}, E("evidence-missing", "log evidence is unavailable", 1)
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return LogPage{}, E("evidence-invalid", "log is not a regular file", 3)
	}
	if _, e = f.Seek(offset, io.SeekStart); e != nil {
		return LogPage{}, e
	}
	// Read ahead by at most one rune so a valid character crossing the byte
	// budget is emitted once, in full. A live stream may not have written the
	// rest of its final rune yet; leave those bytes for the next request.
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+utf8.UTFMax-1))
	n := 0
	for n < len(b) && n < limit {
		if !utf8.FullRune(b[n:]) && !terminal {
			break
		}
		_, size := utf8.DecodeRune(b[n:])
		n += size
	}
	b = b[:n]
	// Text transports require UTF-8; cursor positions and stored evidence remain raw bytes.
	return LogPage{Text: strings.ToValidUTF8(string(b), "\uFFFD"), NextOffset: offset + int64(len(b)), Complete: terminal && offset+int64(len(b)) >= info.Size()}, e
}
