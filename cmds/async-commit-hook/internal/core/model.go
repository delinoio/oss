package core

import (
	"encoding/json"
	"fmt"
	"time"
)

const Version = "0.1.0"
const SchemaVersion = 1
const ProjectFile = ".config/async-commit-hook.toml"

type State string

const (
	Queued      State = "queued"
	Preparing   State = "preparing"
	Running     State = "running"
	Collecting  State = "collecting"
	Passed      State = "passed"
	Failed      State = "failed"
	Blocked     State = "blocked"
	Cancelled   State = "cancelled"
	Replaced    State = "replaced"
	Interrupted State = "interrupted"
	Skipped     State = "skipped"
	Expired     State = "expired"
)

func (s State) Terminal() bool {
	switch s {
	case Passed, Failed, Blocked, Cancelled, Replaced, Interrupted, Skipped, Expired:
		return true
	}
	return false
}

type Mode string

const (
	Daemon   Mode = "daemon"
	OnDemand Mode = "on-demand"
)

type Policy string

const (
	Parallel Policy = "parallel"
	Queue    Policy = "queue"
	Replace  Policy = "replace"
)

type Shell string

const (
	Sh         Shell = "sh"
	Bash       Shell = "bash"
	PowerShell Shell = "powershell"
	Pwsh       Shell = "pwsh"
)

type PushPolicy string

const (
	PushBlock PushPolicy = "block"
	PushWait  PushPolicy = "wait"
	PushRun   PushPolicy = "run-and-wait"
)

type ReportKind string

const (
	JUnit  ReportKind = "junit"
	GoTest ReportKind = "go-test-json"
)

type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}
type Error struct {
	Diagnostic
	Exit int `json:"-"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func E(code, message string, exit int) error {
	return &Error{Diagnostic: Diagnostic{Code: code, Message: message}, Exit: exit}
}
func Wrap(code string, err error) error { return E(code, err.Error(), 3) }

type Repository struct {
	ID        string     `json:"id"`
	CommonDir string     `json:"common_dir"`
	Name      string     `json:"name"`
	Worktrees []Worktree `json:"worktrees"`
}
type Worktree struct {
	ID           string `json:"id"`
	RepositoryID string `json:"repository_id"`
	Path         string `json:"path"`
	Branch       string `json:"branch"`
	Available    bool   `json:"available"`
}
type Failure struct {
	ID      string `json:"id"`
	Check   string `json:"check"`
	Test    string `json:"test,omitempty"`
	Command string `json:"command"`
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	LogID   string `json:"log_id"`
}
type Evidence struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Process struct {
	ScopeDir string    `json:"scope_dir,omitempty"`
	PID      int       `json:"pid"`
	Birth    string    `json:"birth"`
	Group    int       `json:"group"`
	Members  []Process `json:"members,omitempty"`
}
type Check struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	State         State        `json:"state"`
	Optional      bool         `json:"optional"`
	Shell         Shell        `json:"shell"`
	Group         string       `json:"group"`
	Policy        Policy       `json:"policy"`
	ExitCode      *int         `json:"exit_code,omitempty"`
	Process       Process      `json:"process"`
	StartedAt     *time.Time   `json:"started_at,omitempty"`
	FinishedAt    *time.Time   `json:"finished_at,omitempty"`
	Log           Evidence     `json:"log"`
	Reports       []Evidence   `json:"reports"`
	Failures      []Failure    `json:"failures"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
	InheritedFrom string       `json:"inherited_from,omitempty"`
}
type Run struct {
	SchemaVersion  int               `json:"schema_version"`
	ID             string            `json:"id"`
	Sequence       int64             `json:"sequence"`
	RepositoryID   string            `json:"repository_id"`
	WorktreeID     string            `json:"worktree_id"`
	Source         string            `json:"source"`
	Branch         string            `json:"branch"`
	Commit         string            `json:"commit"`
	State          State             `json:"state"`
	Config         Project           `json:"config"`
	Environment    map[string]string `json:"environment"`
	Fingerprint    string            `json:"fingerprint"`
	OS             string            `json:"os"`
	Arch           string            `json:"arch"`
	CreatedAt      time.Time         `json:"created_at"`
	FinishedAt     *time.Time        `json:"finished_at,omitempty"`
	AcknowledgedAt *time.Time        `json:"acknowledged_at,omitempty"`
	ParentID       string            `json:"parent_id,omitempty"`
	Checks         []Check           `json:"checks"`
	Diagnostics    []Diagnostic      `json:"diagnostics"`
}
type Receipt struct {
	RunID         string      `json:"run_id"`
	Commit        string      `json:"commit"`
	State         State       `json:"state"`
	StatusCommand string      `json:"status_command"`
	WaitCommand   string      `json:"wait_command"`
	MCP           string      `json:"mcp"`
	Guide         string      `json:"guide"`
	URL           string      `json:"url,omitempty"`
	UICommand     string      `json:"ui_command,omitempty"`
	Diagnostic    *Diagnostic `json:"diagnostic,omitempty"`
}
type Gate struct {
	Passed      bool         `json:"passed"`
	State       State        `json:"state"`
	Commit      string       `json:"commit"`
	RunID       string       `json:"run_id,omitempty"`
	Reason      string       `json:"reason"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}
type Comparison struct {
	Available  bool      `json:"available"`
	Reason     string    `json:"reason,omitempty"`
	RunID      string    `json:"run_id"`
	PreviousID string    `json:"previous_id,omitempty"`
	New        []Failure `json:"new"`
	Continuing []Failure `json:"continuing"`
	Resolved   []Failure `json:"resolved"`
}
type Page struct {
	Runs       []Run  `json:"runs"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type LogPage struct {
	Text       string `json:"text"`
	NextOffset int64  `json:"next_offset"`
	Complete   bool   `json:"complete"`
}
type Output struct {
	SchemaVersion int         `json:"schema_version"`
	Result        any         `json:"result,omitempty"`
	Error         *Diagnostic `json:"error,omitempty"`
}

func Encode(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("internal JSON model: %v", err))
	}
	return b
}
