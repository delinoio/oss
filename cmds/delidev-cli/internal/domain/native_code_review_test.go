// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestNativeCodeReviewTargetClosure(t *testing.T) {
	for _, target := range []NativeCodeReviewTarget{
		{Kind: ReviewUncommitted, RepositoryID: NewID(), DiffRevision: strings.Repeat("a", 64)},
		{Kind: ReviewBaseBranch, RepositoryID: NewID(), DiffRevision: strings.Repeat("a", 64), Reference: "main"},
		{Kind: ReviewCommit, RepositoryID: NewID(), DiffRevision: strings.Repeat("a", 64), Reference: strings.Repeat("b", 40)},
		{Kind: ReviewCustom, RepositoryID: NewID(), DiffRevision: strings.Repeat("a", 64), Instructions: "Review the selected changes."},
	} {
		if target.Validate() != nil {
			t.Fatal("original target rejected", target.Kind)
		}
		bad := target
		bad.DiffRevision = "changed"
		if bad.Validate() == nil {
			t.Fatal("unbound target accepted")
		}
		bad = target
		bad.Kind = "feedback"
		if bad.Validate() == nil {
			t.Fatal("ordinary feedback acquired native review")
		}
	}
}
func TestNativeCodeReviewFindingsRejectForeignLocationAndMalformedConfidence(t *testing.T) {
	finding := NativeCodeReviewFinding{Title: "Retain original identity", Body: "The observation uses the wrong source.", Confidence: 0.8, Priority: 1, Path: "src/main.go", StartLine: 1, EndLine: 2}
	if finding.Validate() != nil {
		t.Fatal("bounded finding rejected")
	}
	for _, mutate := range []func(*NativeCodeReviewFinding){
		func(f *NativeCodeReviewFinding) { f.Path = "../foreign.go" }, func(f *NativeCodeReviewFinding) { f.Path = "/private/raw/path.go" },
		func(f *NativeCodeReviewFinding) { f.Confidence = math.NaN() }, func(f *NativeCodeReviewFinding) { f.Confidence = math.Inf(1) },
		func(f *NativeCodeReviewFinding) { f.Priority = 4 }, func(f *NativeCodeReviewFinding) { f.StartLine = 0 },
	} {
		bad := finding
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("foreign or malformed finding accepted")
		}
	}
}

func TestNativeReviewMachineCapabilityIsClosedAndUnique(t *testing.T) {
	machine := Machine{Name: "Original Worker", OS: "linux", Architecture: "amd64", WorkerCapabilities: []WorkerCapability{NativeCodexReviewV1}}
	if err := machine.Validate(); err != nil {
		t.Fatal("own negotiated capability refused", err)
	}
	machine.WorkerCapabilities = append(machine.WorkerCapabilities, NativeCodexReviewV1)
	if machine.Validate() == nil {
		t.Fatal("duplicate native review support accepted")
	}
	machine.WorkerCapabilities = []WorkerCapability{"foreign-native-review"}
	if machine.Validate() == nil {
		t.Fatal("unknown review capability accepted")
	}
}

// boundedNativeReviewResult returns a valid result with one usage record bound
// to the result's action, thread and turn.
func boundedNativeReviewResult(t *testing.T) NativeCodeReviewResult {
	t.Helper()
	action, thread, turn := NewID(), NewID(), NewID()
	target := NativeCodeReviewTarget{Kind: ReviewUncommitted, RepositoryID: NewID(), DiffRevision: strings.Repeat("a", 64)}
	result := NativeCodeReviewResult{
		Version: 1, ActionID: action,
		Selection:     NativeCodeReviewSelection{Target: target, HeadCommit: strings.Repeat("b", 40), ContentDigest: strings.Repeat("c", 64)},
		ThreadID:      NativeIdentity(thread),
		TurnID:        NativeIdentity(turn),
		EnteredItemID: "entered", ExitedItemID: "exited",
		Findings:    []NativeCodeReviewFinding{},
		Explanation: "Bounded explanation.", Correctness: "patch is correct",
		RolloutDigest: strings.Repeat("e", 64), CleanupVerified: true,
	}
	provider := NewID()
	result.UsageRecords = []ResponseUsageRecord{{Purpose: NativeCodeReviewUsage, SessionID: NewID(), ExecutionID: action, AccountID: NewID(), ConnectionID: NewID(), ProviderID: provider, ModelID: (ModelIdentity{ProviderID: provider, NativeID: "fixture"}).Key(), Harness: Codex, ThreadID: string(thread), TurnID: string(turn), Sequence: 1, Usage: NativeResponseUsage{ResponseDigest: strings.Repeat("a", 64), CostEvidence: UsageCostMissing}}}
	if err := result.Validate(); err != nil {
		t.Fatal("base fixture rejected", err)
	}
	return result
}

func encodedNativeReviewSize(t *testing.T, result NativeCodeReviewResult) int {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return len(encoded)
}

// fillNativeReviewResult adds maximal findings and then converts plain bytes to
// escaped bytes until the encoded size is exactly target. Quote characters grow
// by one encoded byte and angle brackets by five, so every deficit is reachable.
func fillNativeReviewResult(t *testing.T, result NativeCodeReviewResult, target int) NativeCodeReviewResult {
	t.Helper()
	for range 64 {
		result.Findings = append(result.Findings, NativeCodeReviewFinding{Title: strings.Repeat("t", 1024), Body: strings.Repeat("b", 8192), Confidence: 0.5, Priority: 1, Path: "src/main.go", StartLine: 1, EndLine: 2})
	}
	result.Explanation = strings.Repeat("x", 8192)
	deficit := target - encodedNativeReviewSize(t, result)
	if deficit < 0 {
		t.Fatal("plain fill already exceeds target")
	}
	for i := range result.Findings {
		for _, char := range []string{"<", "\""} {
			step := 5
			if char == "\"" {
				step = 1
			}
			for deficit >= step {
				body := []byte(result.Findings[i].Body)
				index := strings.IndexByte(string(body), 'b')
				if index < 0 {
					break
				}
				body[index] = char[0]
				result.Findings[i].Body = string(body)
				deficit -= step
			}
		}
	}
	if deficit != 0 {
		t.Fatalf("fixture could not reach target size, remaining %d", deficit)
	}
	if got := encodedNativeReviewSize(t, result); got != target {
		t.Fatalf("encoded size %d, want %d", got, target)
	}
	return result
}

func TestNativeCodeReviewResultAcceptsExactlyTheOutputCeiling(t *testing.T) {
	for _, target := range []int{MaxWorkerJobOutputBytes - 1, MaxWorkerJobOutputBytes} {
		result := fillNativeReviewResult(t, boundedNativeReviewResult(t), target)
		if err := result.Validate(); err != nil {
			t.Fatalf("result of %d encoded bytes rejected: %v", target, err)
		}
	}
}

func TestNativeCodeReviewResultRejectsEncodedSizeAboveOutputCeiling(t *testing.T) {
	result := fillNativeReviewResult(t, boundedNativeReviewResult(t), MaxWorkerJobOutputBytes)
	last := &result.Findings[len(result.Findings)-1]
	last.Body = strings.Replace(last.Body, "b", "\"", 1)
	if encodedNativeReviewSize(t, result) != MaxWorkerJobOutputBytes+1 {
		t.Fatal("fixture did not exceed the ceiling by one byte")
	}
	err := result.Validate()
	if err == nil || SafeError(err).Code != ResourceExhausted {
		t.Fatalf("over-limit result was not rejected as resource exhausted: %v", err)
	}
}

func TestNativeCodeReviewResultLimitCountsEscapedBytesNotRawLength(t *testing.T) {
	result := boundedNativeReviewResult(t)
	raw := 0
	for range 64 {
		finding := NativeCodeReviewFinding{Title: strings.Repeat("<", 1024), Body: strings.Repeat("<", 8192), Confidence: 0.5, Priority: 1, Path: "src/main.go", StartLine: 1, EndLine: 2}
		raw += len(finding.Title) + len(finding.Body)
		result.Findings = append(result.Findings, finding)
	}
	if raw >= MaxWorkerJobOutputBytes {
		t.Fatal("raw string lengths must stay below the ceiling for this regression")
	}
	for _, finding := range result.Findings {
		if finding.Validate() != nil {
			t.Fatal("each finding is individually bounded")
		}
	}
	if err := result.Validate(); err == nil || SafeError(err).Code != ResourceExhausted {
		t.Fatalf("escape-heavy result below the raw ceiling was accepted: %v", err)
	}
}

func TestNativeCodeReviewResultLimitIncludesUsageRecords(t *testing.T) {
	result := fillNativeReviewResult(t, boundedNativeReviewResult(t), MaxWorkerJobOutputBytes)
	result.UsageRecords = append(result.UsageRecords, result.UsageRecords[0])
	result.UsageRecords[1].Sequence = 2
	if err := result.Validate(); err == nil || SafeError(err).Code != ResourceExhausted {
		t.Fatalf("usage records did not count against the ceiling: %v", err)
	}
}

func TestNativeCodeReviewStaleProofIsClosedAndDistinctFromRejectedProof(t *testing.T) {
	action := NewID()
	valid := NativeCodeReviewStaleProof{Version: 1, ActionID: action, Stale: true, CleanupVerified: true}
	if valid.Validate() != nil {
		t.Fatal("verified stale proof rejected")
	}
	for name, proof := range map[string]NativeCodeReviewStaleProof{
		"version":            {Version: 2, ActionID: action, Stale: true, CleanupVerified: true},
		"action":             {Version: 1, ActionID: "not-an-id", Stale: true, CleanupVerified: true},
		"not stale":          {Version: 1, ActionID: action, CleanupVerified: true},
		"cleanup unverified": {Version: 1, ActionID: action, Stale: true},
	} {
		if proof.Validate() == nil {
			t.Errorf("%s proof was accepted", name)
		}
	}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	var rejected NativeCodeReviewRejectedProof
	if Decode(raw, &rejected) == nil && rejected.Validate() == nil {
		t.Fatal("stale proof decoded as a no-send proof")
	}
	rejectedRaw, _ := json.Marshal(NativeCodeReviewRejectedProof{Version: 1, ActionID: action, NoSend: true, CleanupVerified: true})
	var stale NativeCodeReviewStaleProof
	if Decode(rejectedRaw, &stale) == nil && stale.Validate() == nil {
		t.Fatal("no-send proof decoded as a stale proof")
	}
}
