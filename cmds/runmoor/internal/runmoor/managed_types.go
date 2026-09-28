package runmoor

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

type UpdatePhase string

const (
	UpdatePending   UpdatePhase = "pending"
	UpdateChecking  UpdatePhase = "checking"
	UpdateWaiting   UpdatePhase = "waiting_for_capacity"
	UpdatePreparing UpdatePhase = "preparing"
	UpdateReady     UpdatePhase = "ready"
	UpdateRetry     UpdatePhase = "retry"
	UpdateExpired   UpdatePhase = "expired"
)

type ArtifactPhase string

const (
	ArtifactPreparing ArtifactPhase = "preparing"
	ArtifactReady     ArtifactPhase = "ready"
	ArtifactRemoving  ArtifactPhase = "removing"
)

type RunnerMode string

const (
	RunnerAutomatic RunnerMode = "automatic"
	RunnerPinned    RunnerMode = "pinned"
)

func runnerMode(p Pool) RunnerMode {
	if p.RunnerVersion == LatestRunner {
		return RunnerAutomatic
	}
	return RunnerPinned
}

type ManagedPool struct {
	Name             string      `json:"name"`
	Mode             RunnerMode  `json:"mode"`
	Resources        Resources   `json:"resources"`
	MaxRunners       int         `json:"max_runners"`
	SourceHash       string      `json:"source_hash"`
	AppliedHash      string      `json:"applied_hash"`
	DesiredHash      string      `json:"desired_hash"`
	Current          *Pool       `json:"current,omitempty"`
	CurrentArtifact  string      `json:"current_artifact,omitempty"`
	PreviousArtifact string      `json:"previous_artifact,omitempty"`
	BaseImage        string      `json:"base_image,omitempty"`
	Candidate        string      `json:"candidate,omitempty"`
	CandidateVersion string      `json:"candidate_version,omitempty"`
	Phase            UpdatePhase `json:"phase"`
	LastCheck        time.Time   `json:"last_check,omitempty"`
	NextCheck        time.Time   `json:"next_check,omitempty"`
	Expires          time.Time   `json:"support_deadline,omitempty"`
	Attempts         int         `json:"attempts"`
	Paused           bool        `json:"paused"`
	Problem          *Problem    `json:"error,omitempty"`
}
type RunnerArtifact struct {
	ID              string        `json:"id"`
	Pool            string        `json:"pool"`
	Backend         Backend       `json:"backend"`
	Phase           ArtifactPhase `json:"phase"`
	Image           string        `json:"image,omitempty"`
	Container       string        `json:"container,omitempty"`
	Resources       Resources     `json:"resources"`
	Reserved        bool          `json:"reserved"`
	Generated       bool          `json:"generated"`
	CreatedAt       time.Time     `json:"created_at"`
	Problem         *Problem      `json:"error,omitempty"`
	CleanupAttempts int           `json:"cleanup_attempts,omitempty"`
	NextCleanup     time.Time     `json:"next_cleanup,omitempty"`
}
type RunnerImageBuilder interface {
	Prepare(context.Context, Config, Pool, RunnerArtifact, RunnerRelease) (Pool, error)
	Cleanup(context.Context, Config, RunnerArtifact) error
}
type ManagedImageBuilder struct {
	Store  *Store
	Images *ImageManager
	Client *http.Client
	Log    *slog.Logger
}

func managesRunner(p Pool) bool {
	return p.RunnerVersion == LatestRunner || p.Image == "" || p.ImageSource != nil
}
func requestedPool(c Config, name string) (Pool, bool) {
	for _, p := range c.Pools {
		if p.Name == name {
			return p, true
		}
	}
	return Pool{}, false
}
