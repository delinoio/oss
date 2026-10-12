// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func NativeReviewTarget(p *pb.NativeCodeReviewTarget) (domain.NativeCodeReviewTarget, error) {
	if p == nil {
		return domain.NativeCodeReviewTarget{}, domain.NativeCodeReviewUnavailable()
	}
	kinds := map[pb.NativeCodeReviewTargetKind]domain.NativeCodeReviewTargetKind{pb.NativeCodeReviewTargetKind_NATIVE_CODE_REVIEW_TARGET_KIND_UNCOMMITTED: domain.ReviewUncommitted, pb.NativeCodeReviewTargetKind_NATIVE_CODE_REVIEW_TARGET_KIND_BASE_BRANCH: domain.ReviewBaseBranch, pb.NativeCodeReviewTargetKind_NATIVE_CODE_REVIEW_TARGET_KIND_COMMIT: domain.ReviewCommit, pb.NativeCodeReviewTargetKind_NATIVE_CODE_REVIEW_TARGET_KIND_CUSTOM: domain.ReviewCustom}
	value := domain.NativeCodeReviewTarget{Kind: kinds[p.Kind], RepositoryID: domain.ID(p.RepositoryId), DiffRevision: p.DiffRevision, Reference: p.Reference, Instructions: p.Instructions}
	return value, value.Validate()
}
func NativeReviewTargetWire(v domain.NativeCodeReviewTarget) *pb.NativeCodeReviewTarget {
	kinds := map[domain.NativeCodeReviewTargetKind]pb.NativeCodeReviewTargetKind{domain.ReviewUncommitted: pb.NativeCodeReviewTargetKind_NATIVE_CODE_REVIEW_TARGET_KIND_UNCOMMITTED, domain.ReviewBaseBranch: pb.NativeCodeReviewTargetKind_NATIVE_CODE_REVIEW_TARGET_KIND_BASE_BRANCH, domain.ReviewCommit: pb.NativeCodeReviewTargetKind_NATIVE_CODE_REVIEW_TARGET_KIND_COMMIT, domain.ReviewCustom: pb.NativeCodeReviewTargetKind_NATIVE_CODE_REVIEW_TARGET_KIND_CUSTOM}
	return &pb.NativeCodeReviewTarget{Kind: kinds[v.Kind], RepositoryId: string(v.RepositoryID), DiffRevision: v.DiffRevision, Reference: v.Reference, Instructions: v.Instructions}
}
func NativeReviewSelection(p *pb.NativeCodeReviewSelection) (domain.NativeCodeReviewSelection, error) {
	var v domain.NativeCodeReviewSelection
	if p == nil {
		return v, domain.NativeCodeReviewUnavailable()
	}
	target, err := NativeReviewTarget(p.Target)
	if err != nil {
		return v, err
	}
	v = domain.NativeCodeReviewSelection{Target: target, HeadCommit: p.HeadCommit, BaseCommit: p.BaseCommit, ContentDigest: p.ContentDigest}
	return v, v.Validate()
}
func NativeReviewSelectionWire(v domain.NativeCodeReviewSelection) *pb.NativeCodeReviewSelection {
	return &pb.NativeCodeReviewSelection{Target: NativeReviewTargetWire(v.Target), HeadCommit: v.HeadCommit, BaseCommit: v.BaseCommit, ContentDigest: v.ContentDigest}
}
func NativeReviewStateWire(v domain.NativeCodeReviewState) pb.NativeCodeReviewState {
	states := map[domain.NativeCodeReviewState]pb.NativeCodeReviewState{domain.NativeReviewRejected: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_REJECTED, domain.NativeReviewQueued: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_QUEUED, domain.NativeReviewReady: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_READY, domain.NativeReviewEntered: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_ENTERED, domain.NativeReviewExited: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_EXITED, domain.NativeReviewCompleted: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_COMPLETED, domain.NativeReviewInterrupted: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_INTERRUPTED, domain.NativeReviewUncertain: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_UNCERTAIN, domain.NativeReviewStale: pb.NativeCodeReviewState_NATIVE_CODE_REVIEW_STATE_STALE}
	return states[v]
}
func NativeReviewState(v pb.NativeCodeReviewState) domain.NativeCodeReviewState {
	for _, s := range []domain.NativeCodeReviewState{domain.NativeReviewRejected, domain.NativeReviewQueued, domain.NativeReviewReady, domain.NativeReviewEntered, domain.NativeReviewExited, domain.NativeReviewCompleted, domain.NativeReviewInterrupted, domain.NativeReviewUncertain, domain.NativeReviewStale} {
		if NativeReviewStateWire(s) == v {
			return s
		}
	}
	return ""
}
func NativeReviewResultWire(v domain.NativeCodeReviewResult) *pb.NativeCodeReviewResult {
	p := &pb.NativeCodeReviewResult{Selection: NativeReviewSelectionWire(v.Selection), ThreadId: string(v.ThreadID), TurnId: string(v.TurnID), EnteredItemId: v.EnteredItemID, ExitedItemId: v.ExitedItemID, Correctness: v.Correctness, Explanation: v.Explanation, Confidence: v.Confidence, RolloutDigest: v.RolloutDigest, CleanupVerified: v.CleanupVerified}
	for _, f := range v.Findings {
		p.Findings = append(p.Findings, &pb.NativeCodeReviewFinding{Title: f.Title, Body: f.Body, Confidence: f.Confidence, Priority: f.Priority, Path: f.Path, StartLine: f.StartLine, EndLine: f.EndLine})
	}
	return p
}
func nativeReviewJobDocument(raw []byte) []byte {
	var envelope struct {
		Type domain.JobType `json:"type"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Type != domain.NativeCodeReviewJob {
		return raw
	}
	var job domain.Job
	if domain.DecodeNativeCodeReviewJob(raw, &job) != nil {
		return nil
	}
	var input domain.NativeCodeReviewInput
	if domain.DecodeNativeCodeReviewInput(job.Input, &input) != nil {
		return nil
	}
	// Only the original Worker receives the private source assignment. Public
	// resources keep the selected target, state and validated bounded findings.
	job.Input, _ = json.Marshal(struct {
		Version  uint32                        `json:"version"`
		ActionID domain.ID                     `json:"action_id"`
		Target   domain.NativeCodeReviewTarget `json:"target"`
	}{1, input.ActionID, input.Target})
	result, _ := json.Marshal(job)
	return result
}

func NativeReviewUsage(p *pb.NativeCodeReviewResponseUsage) (domain.NativeResponseUsage, error) {
	var value domain.NativeResponseUsage
	if p == nil || p.Sequence == 0 || p.Sequence > domain.MaxExecutionEvents || (p.NativeVersion != "" && !domain.ValidNativeVersionMetadata(p.NativeVersion)) {
		return value, domain.NativeCodeReviewUnavailable()
	}
	value.ResponseDigest, value.CostEvidence = p.ResponseDigest, domain.UsageCostMissing
	if p.UnspecifiedNativeCost {
		value.CostEvidence = domain.UsageCostUnspecified
	}
	if p.CountsAvailable {
		value.Counts = &domain.NativeTokenCounts{Input: p.Input, Cached: p.CachedInput, CacheWrite: p.CacheWriteInput, Output: p.Output, Reasoning: p.ReasoningOutput, Total: p.Total}
	} else if p.Input != nil || p.CachedInput != nil || p.CacheWriteInput != nil || p.Output != nil || p.ReasoningOutput != nil || p.Total != nil {
		return value, domain.NativeCodeReviewUnavailable()
	}
	return value, value.Validate()
}
func NativeReviewUsageWire(value domain.NativeResponseUsage, sequence uint64, version string) *pb.NativeCodeReviewResponseUsage {
	p := &pb.NativeCodeReviewResponseUsage{ResponseDigest: value.ResponseDigest, Sequence: sequence, NativeVersion: version, UnspecifiedNativeCost: value.CostEvidence == domain.UsageCostUnspecified}
	if value.Counts != nil {
		p.CountsAvailable = true
		p.Input = value.Counts.Input
		p.CachedInput = value.Counts.Cached
		p.CacheWriteInput = value.Counts.CacheWrite
		p.Output = value.Counts.Output
		p.ReasoningOutput = value.Counts.Reasoning
		p.Total = value.Counts.Total
	}
	return p
}
