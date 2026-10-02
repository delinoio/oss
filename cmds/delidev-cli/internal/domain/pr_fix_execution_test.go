// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
)

func TestPRFixExecutionRechecksImmutableWritePermission(t *testing.T) {
	observation := conflictObservationFixture()
	repository := observation.Repository
	observation.Items[0].HeadRepository = &PRHeadRepositoryObservation{State: PRHeadRepositoryAvailable, Repository: &repository}
	target, err := NewPRGitTarget(observation)
	if err != nil {
		t.Fatal(err)
	}
	account := NewID()
	input := ExecutionJobInput{
		Version: 1, SessionID: NewID(), MachineID: NewID(), ExecutionID: NewID(),
		InputID: NewID(), ThreadRequestID: NewID(), TurnRequestID: NewID(), AccountID: account, ConnectionID: NewID(),
		Configuration: ExecutionConfiguration{AgentID: NewID(), AgentRevision: 1, Harness: Codex,
			ModelID: NewID(), ModelRevision: 1, ProviderID: NewID(), NativeModel: "fixture-model", Routing: Priority,
			Accounts: []WeightedAccount{{ID: account, Weight: 1}}, Options: AgentOptions{Permission: PermissionWorkspaceWrite}},
		Input: SessionInput{Mode: ExecuteMode, Prompt: "Repair the original retained problem"},
		Installation: Installation{Harness: Codex, State: InstallationDetected, Version: CodexProtocolVersion,
			ProtocolVerified: true, Protocol: &ProtocolObservation{Protocol: CodexAppServer, State: ProtocolVerified}},
		Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`),
		Remediation: &PRFixExecution{AttemptID: NewID(), Target: target, Strategy: MergeConflictStrategy},
	}
	input.ConfigurationDigest, _ = input.Configuration.Digest()
	if err := input.Validate(); err != nil {
		t.Fatal("valid original write assignment", err)
	}
	for _, permission := range []PermissionMode{PermissionWorkspaceWrite, PermissionFullAccess, PermissionReadOnly, PermissionDefault} {
		t.Run(string(permission), func(t *testing.T) {
			changed := input
			changed.Configuration.AgentRevision++
			changed.Configuration.Options.Permission = permission
			changed.ConfigurationDigest, _ = changed.Configuration.Digest()
			err := changed.Validate()
			if permission == PermissionWorkspaceWrite || permission == PermissionFullAccess {
				if err != nil {
					t.Fatal("explicit write assignment refused", err)
				}
			} else if err == nil || SafeError(err).Code != Unsupported {
				t.Fatal("changed non-write assignment retained Git authority", err)
			}
			changed.Remediation = nil
			if err := changed.Validate(); err != nil {
				t.Fatal("ordinary non-remediation permission changed", err)
			}
		})
	}
}
