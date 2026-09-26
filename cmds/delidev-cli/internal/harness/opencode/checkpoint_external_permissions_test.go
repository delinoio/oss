package opencode

import (
	"bytes"
	"encoding/json"
	"testing"
)

func setCheckpointExternalTool(value *nativeCheckpoint, s *sessionAPI, i *observedInteraction, name checkpointToolName) {
	part := s.observer.parts[s.observer.calls[i.value.Tool.CallID]]
	part.value.Tool = checkpointInlineToolFixture(name)
	part.value.Tool.CallID = i.value.Tool.CallID
	replaceCheckpointRejectedTool(value, s, i, name, checkpointRejectExternal)
}

func TestCheckpointExternalAllowancesPreserveOriginalCrossToolClosure(t *testing.T) {
	names := []checkpointToolName{checkpointReadTool, checkpointShellTool, checkpointGlobTool, checkpointGrepTool, checkpointWriteTool, checkpointEditTool, checkpointApplyPatchTool}
	for index, name := range names {
		t.Run(string(name), func(t *testing.T) {
			value, s, policy := checkpointPolicyFixture(t)
			direct := s.observer.interactions[policy.alwaysObservations[0]]
			setCheckpointExternalTool(&value, s, direct, name)
			setCheckpointExternalTool(&value, s, policy, names[(index+1)%len(names)])
			value.Tools = s.checkpointToolHistory(value)
			if value.Tools == nil || value.Tools.Version != 13 || len(value.Tools.Always) != 1 || len(value.Tools.Policy) != 1 || value.Tools.Policy[0].Permission != checkpointAllowExternal || checkpointReplacementProfile(value) != nil {
				t.Fatal("original external allowance or automatic closure lost")
			}
			for i, rule := range value.Tools.Always[0].Rules {
				if rule != (PermissionRule{"external_directory", direct.value.Permission.Always[i], PermissionAllow}) {
					t.Fatal("original external allowance changed")
				}
			}
			original, _ := json.Marshal(value)
			s.predecessor, s.observer = &value, &inputObserver{}
			s.restoredAlways = 1
			s.sessionPermissions = checkpointAppliedPermissions(value.Tools, 1)
			next := nativeCheckpoint{Project: "global", NativeRoot: "/", Previous: []HistoryObservation{copyHistoryObservation(value.History)}}
			next.Tools = s.checkpointToolHistory(next)
			if next.Tools == nil || next.Tools.Version != 13 || next.Tools.AppliedAlways != 1 || checkpointReplacementProfile(next) != nil || s.freshCheckpointInput(direct.attempt.claim.RequestID, "new-message", "new-part") {
				t.Fatal("external successor lost applied prefix or reused response")
			}
			next.Tools.Policy[0].Sources[0], next.Tools.Always[0].Rules[0].Pattern = "changed", "changed"
			after, _ := json.Marshal(value)
			if !bytes.Equal(original, after) {
				t.Fatal("successor changed original external allowance evidence")
			}
			for version := uint32(1); version <= 12; version++ {
				value.Tools.Version = version
				if validCheckpointTools(value) {
					t.Fatal("old proof acquired external allowance", version)
				}
			}
		})
	}
}

func TestCheckpointExternalAllowanceRejectsIncompatibleOriginalState(t *testing.T) {
	for _, fault := range []string{"unknown", "tool", "unfinished", "metadata", "http", "native", "canceled", "read-rule", "mixed-rules", "rule-name", "policy-name", "policy-missing", "policy-tool", "source"} {
		t.Run(fault, func(t *testing.T) {
			value, s, policy := checkpointPolicyFixture(t)
			direct := s.observer.interactions[policy.alwaysObservations[0]]
			setCheckpointExternalTool(&value, s, direct, checkpointWriteTool)
			setCheckpointExternalTool(&value, s, policy, checkpointShellTool)
			value.Tools = s.checkpointToolHistory(value)
			if value.Tools == nil {
				t.Fatal("missing original fixture proof")
			}
			capture := true
			switch fault {
			case "unknown":
				direct.value.Permission.Name = "*"
			case "tool":
				s.observer.parts[direct.attempt.claim.PartID].value.Tool.Name = "future"
			case "unfinished":
				s.observer.parts[direct.attempt.claim.PartID].value.Tool.State = ToolRunning
			case "metadata":
				s.observer.parts[direct.attempt.claim.PartID].value.Tool.Metadata = nil
			case "http":
				direct.attempt.receipt.HTTPAccepted = false
			case "native":
				direct.attempt.receipt.NativeAccepted = false
			case "canceled":
				direct.canceled = true
			default:
				capture = false
				switch fault {
				case "read-rule":
					value.Tools.Always[0].Rules[0].Permission = "read"
				case "mixed-rules":
					value.Tools.Always[0].Rules = append(value.Tools.Always[0].Rules, PermissionRule{"read", "private-pattern", PermissionAllow})
				case "rule-name":
					value.Tools.Always[0].Rules[0].Permission = "edit"
				case "policy-name":
					value.Tools.Policy[0].Permission = "read"
				case "policy-missing":
					value.Tools.Policy[0].Permission = ""
				case "policy-tool":
					value.Tools.Parts[1].Name = checkpointQuestionTool
				case "source":
					value.Tools.Policy[0].Sources[0] = "per_01960dcbe199ABCDEFGHIJKLMN"
				}
			}
			if capture && s.checkpointToolHistory(value) != nil || !capture && validCheckpointTools(value) {
				t.Fatal("invalid external allowance acquired restoration", fault)
			}
		})
	}
}

func TestCheckpointExternalReadAllowanceKeepsItsOrdinaryErrorProfile(t *testing.T) {
	value, s, i := checkpointPermissionFixture(t, PermissionAlways)
	setCheckpointExternalTool(&value, s, i, checkpointReadTool)
	setCheckpointReadError(&value, s, i.attempt.claim.PartID)
	value.Tools = s.checkpointToolHistory(value)
	if value.Tools == nil || value.Tools.Version != 13 || value.Tools.Parts[0].ErrorProfile != checkpointReadError || checkpointReplacementProfile(value) != nil {
		t.Fatal("original ordinary Read error lost external allowance")
	}
}

func TestCheckpointExternalPolicyCannotBorrowOnlyReadSource(t *testing.T) {
	value, s, policy := checkpointPolicyFixture(t)
	setCheckpointExternalTool(&value, s, policy, checkpointShellTool)
	if s.checkpointToolHistory(value) != nil {
		t.Fatal("external policy closure borrowed an unrelated Read allowance")
	}
	value, _, _ = checkpointPolicyFixture(t)
	value.Tools.Version = 13
	value.Tools.Policy[0].Permission = checkpointAllowExternal
	value.Tools.Parts[1].Name = checkpointShellTool
	if validCheckpointTools(value) {
		t.Fatal("stored external closure has no original same-permission source")
	}
}
