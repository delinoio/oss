// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"slices"
	"time"
)

type CodexAppsAction string
type CodexAppsState string

const (
	CodexAppsInspect   CodexAppsAction = "inspect"
	CodexAppsRevoke    CodexAppsAction = "revoke"
	CodexAppsQueued    CodexAppsState  = "queued"
	CodexAppsClaimed   CodexAppsState  = "claimed"
	CodexAppsSucceeded CodexAppsState  = "succeeded"
	CodexAppsFailed    CodexAppsState  = "failed"
	CodexAppsUncertain CodexAppsState  = "uncertain"
)

// Inventory is an observation of one retained original controller. It never
// authorizes another execution, account or connector installation.
type CodexAppsInventory struct {
	OperationID ID `json:"operation_id"`
	ClaimID     ID `json:"claim_id"`
	// Set only by the original Worker after a complete forced native catalog refresh.
	NativeCatalogRefreshVerified bool           `json:"native_catalog_refresh_verified"`
	Version                      int            `json:"version"`
	SessionID                    ID             `json:"session_id"`
	AccountID                    ID             `json:"account_id"`
	ConfigurationGeneration      ID             `json:"configuration_generation"`
	ExecutionID                  ID             `json:"execution_id"`
	ExecutionJobID               ID             `json:"execution_job_id"`
	MachineID                    ID             `json:"machine_id"`
	InstanceID                   ID             `json:"instance_id"`
	NativeThreadID               NativeIdentity `json:"native_thread_id"`
	ObservedAt                   time.Time      `json:"observed_at"`
	Apps                         []CodexApp     `json:"apps"`
}

func (v CodexAppsInventory) Validate() error {
	if !v.NativeCatalogRefreshVerified || v.OperationID.Validate() != nil || v.ClaimID.Validate() != nil || v.Version != 1 || v.ObservedAt.IsZero() || v.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil || v.Apps == nil || len(v.Apps) > MaxCodexAppInventory {
		return invalidCodexApps()
	}
	for _, id := range []ID{v.SessionID, v.AccountID, v.ConfigurationGeneration, v.ExecutionID, v.ExecutionJobID, v.MachineID, v.InstanceID} {
		if id.Validate() != nil {
			return invalidCodexApps()
		}
	}
	seen := map[string]bool{}
	for _, a := range v.Apps {
		if a.Validate() != nil || seen[a.ID] {
			return invalidCodexApps()
		}
		seen[a.ID] = true
	}
	return nil
}

// A claim records the original native obligation before delivery. A terminal
// receipt is never retried. Uncertainty retains that obligation independently
// of selection state and native process cleanup.
type CodexAppsOperation struct {
	ClaimID        ID                     `json:"claim_id,omitempty"`
	Version        int                    `json:"version"`
	ID             ID                     `json:"id"`
	Revision       uint64                 `json:"revision,string"`
	RequestID      ID                     `json:"request_id"`
	ActorID        ID                     `json:"actor_id"`
	Action         CodexAppsAction        `json:"action"`
	State          CodexAppsState         `json:"state"`
	Original       CodexAppConfiguration  `json:"original"`
	Next           *CodexAppConfiguration `json:"next,omitempty"`
	ExecutionID    ID                     `json:"execution_id"`
	ExecutionJobID ID                     `json:"execution_job_id"`
	MachineID      ID                     `json:"machine_id"`
	InstanceID     ID                     `json:"instance_id"`
	NativeThreadID NativeIdentity         `json:"native_thread_id"`
	Inventory      *CodexAppsInventory    `json:"inventory,omitempty"`
	Problem        *Error                 `json:"problem,omitempty"`
}

func (v CodexAppsOperation) Validate() error {
	if v.Version != 1 || v.Revision == 0 || v.Original.Validate() != nil || !slices.Contains([]CodexAppsState{CodexAppsQueued, CodexAppsClaimed, CodexAppsSucceeded, CodexAppsFailed, CodexAppsUncertain}, v.State) {
		return invalidCodexApps()
	}
	for _, id := range []ID{v.ID, v.RequestID, v.ActorID, v.ExecutionID, v.ExecutionJobID, v.MachineID, v.InstanceID} {
		if id.Validate() != nil {
			return invalidCodexApps()
		}
	}
	if (v.State != CodexAppsQueued && v.ClaimID.Validate() != nil) || (v.State == CodexAppsQueued && v.ClaimID != "") || v.NativeThreadID.Validate(Codex, NativeThreadIdentity) != nil {
		return invalidCodexApps()
	}
	if v.Action == CodexAppsInspect {
		if v.Next != nil {
			return invalidCodexApps()
		}
	} else if v.Action == CodexAppsRevoke {
		if v.Next == nil || !v.Original.RemovalOnly(*v.Next) {
			return invalidCodexApps()
		}
	} else {
		return invalidCodexApps()
	}
	if v.Inventory != nil {
		i := v.Inventory
		expected := v.Original
		if v.Next != nil && v.State == CodexAppsSucceeded {
			expected = *v.Next
		}
		if i.Validate() != nil || i.OperationID != v.ID || i.ClaimID != v.ClaimID || i.SessionID != expected.SessionID || i.AccountID != expected.AccountID || i.ConfigurationGeneration != expected.Generation || i.ExecutionID != v.ExecutionID || i.ExecutionJobID != v.ExecutionJobID || i.MachineID != v.MachineID || i.InstanceID != v.InstanceID || i.NativeThreadID != v.NativeThreadID {
			return invalidCodexApps()
		}
	}
	if v.State == CodexAppsSucceeded && (v.Inventory == nil || v.Problem != nil) || (v.State == CodexAppsFailed || v.State == CodexAppsUncertain) && v.Problem == nil || (v.State == CodexAppsQueued || v.State == CodexAppsClaimed) && (v.Inventory != nil || v.Problem != nil) {
		return invalidCodexApps()
	}
	return nil
}
