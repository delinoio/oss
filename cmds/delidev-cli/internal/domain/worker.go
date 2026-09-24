package domain

import (
	"context"
	"encoding/json"
	"slices"
	"time"
)

type DeviceType string

const WorkerConnectionTimeout = 45 * time.Second

const (
	OwnerDevice  DeviceType = "owner"
	ClientDevice DeviceType = "client"
	WorkerDevice DeviceType = "worker"
)

type Device struct {
	Name      string     `json:"name"`
	Type      DeviceType `json:"type"`
	MachineID ID         `json:"machine_id,omitempty"`
	Revoked   bool       `json:"revoked"`
	PairedAt  time.Time  `json:"paired_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}
type Pairing struct {
	Name      string     `json:"name"`
	Type      DeviceType `json:"type"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedBy    ID         `json:"used_by,omitempty"`
}

func (d Device) Validate() error {
	if err := Text(d.Name, "device name", 256, true); err != nil {
		return err
	}
	if d.Type != ClientDevice && d.Type != WorkerDevice {
		return Fail(InvalidArgument, "Unknown device type.", "Select client or worker.")
	}
	if d.Type == WorkerDevice {
		return d.MachineID.Validate()
	}
	if d.MachineID != "" {
		return Fail(InvalidArgument, "Client pairing cannot claim an execution machine.", "Register a separate Worker.")
	}
	return nil
}

type Principal struct {
	Type      DeviceType
	DeviceID  ID
	MachineID ID
}
type principalKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

type JobType string

const (
	InspectRepositoryJob JobType = "inspect-repository"
	SaveRepositoryJob    JobType = "save-repository"
	PrepareWorkspaceJob  JobType = "prepare-workspace"
	HarnessDiscoveryJob  JobType = "harness-discovery"
)

type JobState string

const (
	JobQueued    JobState = "queued"
	JobClaimed   JobState = "claimed"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobUncertain JobState = "uncertain"
	JobCanceled  JobState = "canceled"
)

func (s JobState) Terminal() bool { return s == JobSucceeded || s == JobFailed || s == JobCanceled }

type Job struct {
	Type       JobType         `json:"type"`
	State      JobState        `json:"state"`
	MachineID  ID              `json:"machine_id,omitempty"`
	InstanceID ID              `json:"instance_id,omitempty"`
	ParentID   ID              `json:"parent_id,omitempty"`
	Input      json.RawMessage `json:"input"`
	Output     json.RawMessage `json:"output,omitempty"`
	Problem    *Error          `json:"problem,omitempty"`
	AcceptedAt time.Time       `json:"accepted_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

func (j Job) Validate() error {
	if !slices.Contains([]JobType{InspectRepositoryJob, SaveRepositoryJob, PrepareWorkspaceJob, HarnessDiscoveryJob}, j.Type) {
		return Fail(InvalidArgument, "Unknown Worker job type.", "Use a supported product operation.")
	}
	if !slices.Contains([]JobState{JobQueued, JobClaimed, JobSucceeded, JobFailed, JobUncertain, JobCanceled}, j.State) {
		return Fail(InvalidArgument, "Unknown Worker job state.", "Reload the accepted job.")
	}
	if j.Type != SaveRepositoryJob {
		if err := j.MachineID.Validate(); err != nil {
			return err
		}
	}
	for _, id := range []ID{j.InstanceID, j.ParentID} {
		if id != "" {
			if err := id.Validate(); err != nil {
				return err
			}
		}
	}
	if len(j.Input) > 1<<20 || len(j.Output) > 1<<20 || !json.Valid(j.Input) || (len(j.Output) > 0 && !json.Valid(j.Output)) {
		return Fail(InvalidArgument, "Invalid Worker job document.", "Use a bounded versioned job payload.")
	}
	return nil
}

type RepositoryInspectionInput struct {
	Path            string   `json:"path"`
	PreferredRemote string   `json:"preferred_remote,omitempty"`
	RequiredRemotes []string `json:"required_remotes,omitempty"`
}
