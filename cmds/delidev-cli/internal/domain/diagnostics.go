package domain

import "time"

type DiagnosticState string

const (
	DiagnosticObserved      DiagnosticState = "observed"
	DiagnosticUnavailable   DiagnosticState = "unavailable"
	DiagnosticUnconfigured  DiagnosticState = "unconfigured"
	DiagnosticNotApplicable DiagnosticState = "not-applicable"
	DiagnosticFailed        DiagnosticState = "failed"
	DiagnosticSuperseded    DiagnosticState = "superseded"
)

type DiagnosticResult struct {
	State    DiagnosticState `json:"state"`
	Code     Code            `json:"code,omitempty"`
	Guidance string          `json:"guidance,omitempty"`
}
type DiagnosticResourceCount struct {
	Kind  Kind   `json:"kind"`
	Count uint64 `json:"count,string"`
}
type DiagnosticStorage struct {
	Result               DiagnosticResult          `json:"result"`
	DatabaseBytes        *uint64                   `json:"database_bytes,string,omitempty"`
	WALBytes             *uint64                   `json:"wal_bytes,string,omitempty"`
	LogicalDatabaseBytes *uint64                   `json:"logical_database_bytes,string,omitempty"`
	VolumeCapacityBytes  *uint64                   `json:"volume_capacity_bytes,string,omitempty"`
	VolumeAvailableBytes *uint64                   `json:"volume_available_bytes,string,omitempty"`
	Resources            []DiagnosticResourceCount `json:"resources"`
}
type DiagnosticCredential struct {
	AccountID    ID               `json:"account_id"`
	ConnectionID ID               `json:"connection_id,omitempty"`
	Result       DiagnosticResult `json:"result"`
}

// Diagnostics omit executable paths and native error strings. These are retained
// observations, not a new probe or a grant of selected-account capabilities.
type DiagnosticInstallation struct {
	Harness          Harness           `json:"harness"`
	State            InstallationState `json:"state"`
	Version          string            `json:"version,omitempty"`
	Capabilities     []Capability      `json:"capabilities"`
	ObservedAt       *time.Time        `json:"observed_at,omitempty"`
	ProtocolVerified bool              `json:"protocol_verified"`
	ProtocolState    ProtocolState     `json:"protocol_state,omitempty"`
	ProblemCode      Code              `json:"problem_code,omitempty"`
	Guidance         string            `json:"guidance,omitempty"`
}
type DiagnosticMachine struct {
	MachineID     ID                       `json:"machine_id"`
	Name          string                   `json:"name"`
	OS            string                   `json:"os"`
	Architecture  string                   `json:"architecture"`
	Version       string                   `json:"version"`
	LastSeen      time.Time                `json:"last_seen"`
	Disabled      bool                     `json:"disabled"`
	ActiveStream  bool                     `json:"active_stream"`
	Installations []DiagnosticInstallation `json:"installations"`
}
type DoctorReport struct {
	SchemaVersion         uint32                 `json:"schema_version"`
	ObservedAt            time.Time              `json:"observed_at"`
	Version               string                 `json:"version"`
	ProtocolVersion       uint32                 `json:"protocol_version"`
	DatabaseSchemaVersion uint32                 `json:"database_schema_version"`
	OS                    string                 `json:"os"`
	Architecture          string                 `json:"architecture"`
	ServerID              ID                     `json:"server_id"`
	Listener              string                 `json:"listener"`
	Database              string                 `json:"database"`
	CredentialStore       string                 `json:"credential_store"`
	InferenceProbes       bool                   `json:"inference_probes"`
	Storage               DiagnosticStorage      `json:"storage"`
	Machines              []DiagnosticMachine    `json:"machines"`
	MoreMachines          bool                   `json:"more_machines"`
	Credentials           []DiagnosticCredential `json:"credentials"`
	MoreCredentials       bool                   `json:"more_credentials"`
}
