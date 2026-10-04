package domain

import "encoding/json"

// Portable documents contain configuration, never observations, credentials,
// device registrations, routing cursors or session history.
const ConfigurationBundleVersion = 2
const MaxConfigurationEntries = 256
const MaxConfigurationBundleBytes = 384 << 10
const MaxConfigurationPlanBytes = 768 << 10
const MaxConfigurationCheckouts = 64

type ConfigurationEntry struct {
	ID       ID              `json:"id"`
	Kind     Kind            `json:"kind"`
	Document json.RawMessage `json:"document"`
}
type ConfigurationMachine struct {
	ID           ID     `json:"id"`
	Name         string `json:"name"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}
type ConfigurationBundle struct {
	Version  uint32                 `json:"version"`
	Entries  []ConfigurationEntry   `json:"entries"`
	Machines []ConfigurationMachine `json:"machines"`
}

type ConfigurationImportAction string

const (
	ConfigurationCreate  ConfigurationImportAction = "create"
	ConfigurationReuse   ConfigurationImportAction = "reuse"
	ConfigurationReplace ConfigurationImportAction = "replace"
)

// Only settings can be replaced. Other existing configuration is explicitly
// reused unchanged or imported as a new independent entity; accounts are new.
type ConfigurationBinding struct {
	SourceID         ID                        `json:"source_id"`
	Action           ConfigurationImportAction `json:"action"`
	TargetID         ID                        `json:"target_id"`
	ExpectedRevision uint64                    `json:"expected_revision"`
}
type ConfigurationMachineBinding struct {
	SourceID ID `json:"source_id"`
	TargetID ID `json:"target_id"`
}
type ConfigurationCheckoutBinding struct {
	RepositoryID ID     `json:"repository_id"`
	MachineID    ID     `json:"machine_id"`
	Path         string `json:"path"`
}
type ConfigurationImportSelection struct {
	Bundle    ConfigurationBundle            `json:"bundle"`
	Bindings  []ConfigurationBinding         `json:"bindings"`
	Machines  []ConfigurationMachineBinding  `json:"machines"`
	Checkouts []ConfigurationCheckoutBinding `json:"checkouts"`
}
type ConfigurationChange struct {
	SourceID         ID                        `json:"source_id"`
	ID               ID                        `json:"id"`
	Kind             Kind                      `json:"kind"`
	Action           ConfigurationImportAction `json:"action"`
	ExpectedRevision uint64                    `json:"expected_revision"`
	Before           json.RawMessage           `json:"before,omitempty"`
	After            json.RawMessage           `json:"after"`
}
type ConfigurationTargetMachine struct {
	ID           ID     `json:"id"`
	Name         string `json:"name"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}
type ConfigurationImportPlan struct {
	Version  uint32                       `json:"version"`
	Changes  []ConfigurationChange        `json:"changes"`
	Machines []ConfigurationTargetMachine `json:"machines"`
}

// Preview tokens are scoped to this exact plan, authenticated actor and server.
// They contain no configuration; the caller retains the reviewed document.
type ConfigurationImportPreview struct {
	Plan  ConfigurationImportPlan `json:"plan"`
	Token string                  `json:"token"`
}
type ConfigurationImportResult struct {
	JobID     ID                              `json:"job_id"`
	State     JobState                        `json:"state"`
	Resources []ConfigurationImportedResource `json:"resources"`
	Problem   *Error                          `json:"problem,omitempty"`
}
type ConfigurationImportedResource struct {
	SourceID ID                        `json:"source_id"`
	ID       ID                        `json:"id"`
	Kind     Kind                      `json:"kind"`
	Action   ConfigurationImportAction `json:"action"`
	Revision uint64                    `json:"revision"`
}
