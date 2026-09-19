package runmoor

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

const Version = "0.1.0"

// Revision is populated by release builds; development builds remain explicit.
var Revision = "development"

type Backend string

const (
	Docker Backend = "docker"
	Tart   Backend = "tart"
)

type Mode string

const (
	Plain Mode = "plain"
	DinD  Mode = "dind"
)

type AuthKind string

const (
	PAT AuthKind = "pat"
	App AuthKind = "app"
)

type PoolPhase string

const (
	Ready     PoolPhase = "ready"
	Paused    PoolPhase = "paused"
	Draining  PoolPhase = "draining"
	Suspended PoolPhase = "suspended"
	Retired   PoolPhase = "retired"
)

type RunnerPhase string

const (
	Preparing   RunnerPhase = "preparing"
	Idle        RunnerPhase = "idle"
	Busy        RunnerPhase = "busy"
	Cleaning    RunnerPhase = "cleaning"
	Quarantined RunnerPhase = "quarantined"
	Completed   RunnerPhase = "completed"
)

type ImagePhase string

const (
	ImagePreparing ImagePhase = "preparing"
	ImageOpen      ImagePhase = "open"
	ImageSealed    ImagePhase = "sealed"
	ImageRemoving  ImagePhase = "removing"
)

type ErrorCode string

const (
	ErrConfig        ErrorCode = "CONFIG_INVALID"
	ErrPlatform      ErrorCode = "PLATFORM_UNSUPPORTED"
	ErrPermission    ErrorCode = "PERMISSION_DENIED"
	ErrState         ErrorCode = "STATE_UNAVAILABLE"
	ErrStateVersion  ErrorCode = "STATE_VERSION_UNSUPPORTED"
	ErrLocked        ErrorCode = "MANAGER_ALREADY_RUNNING"
	ErrDependency    ErrorCode = "DEPENDENCY_UNAVAILABLE"
	ErrImage         ErrorCode = "IMAGE_INVALID"
	ErrImageInUse    ErrorCode = "IMAGE_IN_USE"
	ErrAuth          ErrorCode = "AUTHENTICATION_FAILED"
	ErrRetry         ErrorCode = "DEPENDENCY_RETRY"
	ErrOwnership     ErrorCode = "OWNERSHIP_AMBIGUOUS"
	ErrCapacity      ErrorCode = "CAPACITY_EXHAUSTED"
	ErrDisk          ErrorCode = "DISK_LOW"
	ErrPreparation   ErrorCode = "PREPARATION_FAILED"
	ErrTimeout       ErrorCode = "EXECUTION_TIMEOUT"
	ErrCleanup       ErrorCode = "CLEANUP_PENDING"
	ErrBusy          ErrorCode = "RUNNER_BUSY"
	ErrControl       ErrorCode = "MANAGER_UNAVAILABLE"
	ErrPower         ErrorCode = "SLEEP_INHIBITION_UNAVAILABLE"
	ErrRunnerVersion ErrorCode = "RUNNER_VERSION_UNSUPPORTED"
)

// Problem never wraps provider errors: SDK errors can contain response bodies,
// signed URLs, JIT credentials, or subprocess output. Only fixed messages cross
// this boundary. RetryAt and HTTPStatus are safe transport classifications.
type Problem struct {
	Code       ErrorCode `json:"code"`
	Message    string    `json:"message"`
	Recovery   string    `json:"recovery"`
	Pool       string    `json:"pool,omitempty"`
	Runner     string    `json:"runner,omitempty"`
	RetryAt    time.Time `json:"retry_at,omitempty"`
	HTTPStatus int       `json:"http_status,omitempty"`
}

func (p *Problem) Error() string {
	return fmt.Sprintf("%s: %s Recovery: %s", p.Code, p.Message, p.Recovery)
}
func problem(code ErrorCode, message, recovery string) *Problem {
	return &Problem{Code: code, Message: message, Recovery: recovery}
}
func classify(err error, code ErrorCode, message, recovery string) *Problem {
	if p, ok := err.(*Problem); ok {
		q := *p
		return &q
	}
	return problem(code, message, recovery)
}
func newID() string { return uuid.Must(uuid.NewV7()).String() }

type Resources struct {
	CPU       int   `toml:"cpu" json:"cpu"`
	MemoryMiB int64 `toml:"memory_mib" json:"memory_mib"`
}

func (r Resources) Add(o Resources) Resources {
	return Resources{r.CPU + o.CPU, r.MemoryMiB + o.MemoryMiB}
}

type PoolState struct {
	ID                  string     `json:"id"`
	Generation          string     `json:"generation"`
	Spec                Pool       `json:"spec"`
	Connection          Connection `json:"connection"`
	Phase               PoolPhase  `json:"phase"`
	ScaleSetID          int        `json:"scale_set_id"`
	OwnerLabel          string     `json:"owner_label"`
	CreatePending       bool       `json:"create_pending"`
	Demand              int        `json:"demand"`
	Session             string     `json:"session,omitempty"`
	LastMessage         int        `json:"last_message"`
	PreparationFailures int        `json:"preparation_failures"`
	Problem             *Problem   `json:"problem,omitempty"`
	PreviousIdentity    string     `json:"previous_identity,omitempty"`
}
type Runner struct {
	ID               string      `json:"id"`
	PoolID           string      `json:"pool_id"`
	Generation       string      `json:"generation"`
	Name             string      `json:"name"`
	Phase            RunnerPhase `json:"phase"`
	GitHubID         int         `json:"github_id"`
	Backend          Backend     `json:"backend"`
	Resources        Resources   `json:"resources"`
	Image            string      `json:"image"`
	CreatedAt        time.Time   `json:"created_at"`
	StartedAt        time.Time   `json:"started_at,omitempty"`
	Deadline         time.Time   `json:"deadline"`
	CompletedAt      time.Time   `json:"completed_at,omitempty"`
	RemoteRemoved    bool        `json:"remote_removed"`
	Terminated       bool        `json:"terminated"`
	LocalCleaned     bool        `json:"local_cleaned"`
	CompletedJob     bool        `json:"completed_job"`
	DiagnosticsSaved bool        `json:"diagnostics_saved"`
	Forced           bool        `json:"forced"`
	Handle           Handle      `json:"handle"`
	Problem          *Problem    `json:"problem,omitempty"`
}
type Handle struct {
	Container string   `json:"container,omitempty"`
	Daemon    string   `json:"daemon,omitempty"`
	Network   string   `json:"network,omitempty"`
	Volumes   []string `json:"volumes,omitempty"`
	VM        string   `json:"vm,omitempty"`
	PID       int      `json:"pid,omitempty"`
}
type Image struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Phase         ImagePhase `json:"phase"`
	VM            string     `json:"vm"`
	Source        string     `json:"source"`
	Digest        string     `json:"digest,omitempty"`
	RunnerVersion string     `json:"runner_version,omitempty"`
	RunnerPath    string     `json:"runner_path"`
	Resources     Resources  `json:"resources"`
	CreatedAt     time.Time  `json:"created_at"`
	Problem       *Problem   `json:"problem,omitempty"`
}
type Snapshot struct {
	SchemaVersion int                   `json:"schema_version"`
	Installation  string                `json:"installation"`
	Generation    string                `json:"generation"`
	Config        Config                `json:"config"`
	Pools         map[string]*PoolState `json:"pools"`
	Runners       map[string]*Runner    `json:"runners"`
	Images        map[string]*Image     `json:"images"`
	Generations   map[string]Config     `json:"generations"`
	Paused        bool                  `json:"paused"`
	Stopping      bool                  `json:"stopping"`
	Cursor        int                   `json:"cursor"`
	PowerProblem  *Problem              `json:"power_problem,omitempty"`
}
