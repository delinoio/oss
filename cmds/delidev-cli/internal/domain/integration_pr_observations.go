package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// These observations describe the PR head only. Applicable active rulesets and
// their evaluated commit require a separate evaluation before CI remediation.
type PullRequestDiff struct {
	Patch   string `json:"patch"`
	Digest  string `json:"digest"`
	BaseSHA string `json:"base_sha"`
	HeadSHA string `json:"head_sha"`
}

func (v PullRequestDiff) Validate(item RepositoryItem) error {
	if Text(v.Patch, "pull request diff", 512<<10, false) != nil || v.BaseSHA != item.BaseSHA || v.HeadSHA != item.HeadSHA {
		return invalidPRObservation()
	}
	sum := sha256.Sum256([]byte(v.Patch))
	if v.Digest != hex.EncodeToString(sum[:]) {
		return invalidPRObservation()
	}
	return nil
}
func invalidPRObservation() error {
	return Fail(RecoveryRequired, "The pull request observation is inconsistent.", "Refresh the exact pull request; unavailable results do not establish passing CI or remediation eligibility.")
}
func unsignedDecimal(value string) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && strconv.FormatUint(n, 10) == value
}

type CheckStatus string

const (
	CheckQueued     CheckStatus = "queued"
	CheckInProgress CheckStatus = "in-progress"
	CheckCompleted  CheckStatus = "completed"
	CheckUnknown    CheckStatus = "unknown"
)

type CheckConclusion string

const (
	CheckSuccess           CheckConclusion = "success"
	CheckFailure           CheckConclusion = "failure"
	CheckNeutral           CheckConclusion = "neutral"
	CheckCancelled         CheckConclusion = "cancelled"
	CheckSkipped           CheckConclusion = "skipped"
	CheckTimedOut          CheckConclusion = "timed-out"
	CheckActionRequired    CheckConclusion = "action-required"
	CheckStale             CheckConclusion = "stale"
	CheckStartupFailure    CheckConclusion = "startup-failure"
	CheckConclusionUnknown CheckConclusion = "unknown"
)

func ClassifyCheckStatus(raw string) CheckStatus {
	switch raw {
	case "queued":
		return CheckQueued
	case "in_progress":
		return CheckInProgress
	case "completed":
		return CheckCompleted
	default:
		return CheckUnknown
	}
}
func ClassifyCheckConclusion(raw string) CheckConclusion {
	switch raw {
	case "success":
		return CheckSuccess
	case "failure":
		return CheckFailure
	case "neutral":
		return CheckNeutral
	case "cancelled":
		return CheckCancelled
	case "skipped":
		return CheckSkipped
	case "timed_out":
		return CheckTimedOut
	case "action_required":
		return CheckActionRequired
	case "stale":
		return CheckStale
	case "startup_failure":
		return CheckStartupFailure
	default:
		return CheckConclusionUnknown
	}
}

type CheckApplication struct {
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
	Slug   string `json:"slug"`
}
type PullRequestCheck struct {
	ID               string            `json:"id"`
	NodeID           string            `json:"node_id"`
	Name             string            `json:"name"`
	HeadSHA          string            `json:"head_sha"`
	Status           CheckStatus       `json:"status"`
	NativeStatus     string            `json:"native_status"`
	Conclusion       *CheckConclusion  `json:"conclusion,omitempty"`
	NativeConclusion *string           `json:"native_conclusion,omitempty"`
	Application      *CheckApplication `json:"application,omitempty"`
	StartedAt        *time.Time        `json:"started_at,omitempty"`
	CompletedAt      *time.Time        `json:"completed_at,omitempty"`
}

func (v PullRequestCheck) Validate(head string) error {
	if !PositiveDecimal(v.ID) || Text(v.NodeID, "check node identity", 256, true) != nil || Text(v.Name, "check name", 1024, true) != nil || v.HeadSHA != head || Text(v.NativeStatus, "native check status", 64, true) != nil || v.Status != ClassifyCheckStatus(v.NativeStatus) {
		return invalidPRObservation()
	}
	if (v.Conclusion == nil) != (v.NativeConclusion == nil) {
		return invalidPRObservation()
	}
	if v.Conclusion != nil && (Text(*v.NativeConclusion, "native check conclusion", 64, true) != nil || *v.Conclusion != ClassifyCheckConclusion(*v.NativeConclusion)) {
		return invalidPRObservation()
	}
	if v.StartedAt != nil && v.StartedAt.IsZero() || v.CompletedAt != nil && v.CompletedAt.IsZero() {
		return invalidPRObservation()
	}
	if v.StartedAt != nil && v.CompletedAt != nil && v.CompletedAt.Before(*v.StartedAt) {
		return invalidPRObservation()
	}
	if a := v.Application; a != nil && (!PositiveDecimal(a.ID) || Text(a.NodeID, "check application node identity", 256, true) != nil || Text(a.Slug, "check application slug", 100, true) != nil || strings.ContainsAny(a.Slug, "\r\n")) {
		return invalidPRObservation()
	}
	return nil
}

type CheckRunFilter string

const LatestCheckRuns CheckRunFilter = "latest"

type PullRequestChecks struct {
	HeadSHA    string             `json:"head_sha"`
	Filter     CheckRunFilter     `json:"filter"`
	TotalCount string             `json:"total_count"`
	Runs       []PullRequestCheck `json:"runs"`
}

func (v PullRequestChecks) Validate(item RepositoryItem, pageSize uint32) error {
	if v.HeadSHA != item.HeadSHA || v.Filter != LatestCheckRuns || !unsignedDecimal(v.TotalCount) || v.Runs == nil || len(v.Runs) > int(pageSize) {
		return invalidPRObservation()
	}
	total, _ := strconv.ParseUint(v.TotalCount, 10, 64)
	if total < uint64(len(v.Runs)) {
		return invalidPRObservation()
	}
	seen := map[string]bool{}
	for _, run := range v.Runs {
		if run.Validate(v.HeadSHA) != nil || seen[run.ID] {
			return invalidPRObservation()
		}
		seen[run.ID] = true
	}
	return nil
}

type CommitStatusState string

const (
	CommitStatusPending CommitStatusState = "pending"
	CommitStatusSuccess CommitStatusState = "success"
	CommitStatusFailure CommitStatusState = "failure"
	CommitStatusError   CommitStatusState = "error"
	CommitStatusUnknown CommitStatusState = "unknown"
)

func ClassifyCommitStatus(raw string) CommitStatusState {
	switch raw {
	case "pending":
		return CommitStatusPending
	case "success":
		return CommitStatusSuccess
	case "failure":
		return CommitStatusFailure
	case "error":
		return CommitStatusError
	default:
		return CommitStatusUnknown
	}
}

type PullRequestCommitStatus struct {
	ID          string            `json:"id"`
	NodeID      string            `json:"node_id"`
	Context     string            `json:"context"`
	State       CommitStatusState `json:"state"`
	NativeState string            `json:"native_state"`
	Description *string           `json:"description,omitempty"`
	Creator     *RepositoryActor  `json:"creator,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func (v PullRequestCommitStatus) Validate() error {
	if !PositiveDecimal(v.ID) || Text(v.NodeID, "status node identity", 256, true) != nil || Text(v.Context, "status context", 1024, true) != nil || Text(v.NativeState, "native status state", 64, true) != nil || v.State != ClassifyCommitStatus(v.NativeState) || v.CreatedAt.IsZero() || v.UpdatedAt.Before(v.CreatedAt) {
		return invalidPRObservation()
	}
	if v.Description != nil && Text(*v.Description, "status description", 4096, false) != nil || v.Creator != nil && v.Creator.Validate() != nil {
		return invalidPRObservation()
	}
	return nil
}

type PullRequestCommitStatuses struct {
	HeadSHA     string                    `json:"head_sha"`
	State       CommitStatusState         `json:"state"`
	NativeState string                    `json:"native_state"`
	TotalCount  string                    `json:"total_count"`
	Contexts    []PullRequestCommitStatus `json:"contexts"`
}

func (v PullRequestCommitStatuses) Validate(item RepositoryItem, pageSize uint32) error {
	if v.HeadSHA != item.HeadSHA || !unsignedDecimal(v.TotalCount) || Text(v.NativeState, "combined status", 64, true) != nil || v.State != ClassifyCommitStatus(v.NativeState) || v.Contexts == nil || len(v.Contexts) > int(pageSize) {
		return invalidPRObservation()
	}
	total, _ := strconv.ParseUint(v.TotalCount, 10, 64)
	if total < uint64(len(v.Contexts)) || total == 0 && v.State != CommitStatusPending && v.State != CommitStatusUnknown {
		return invalidPRObservation()
	}
	seen := map[string]bool{}
	for _, status := range v.Contexts {
		if status.Validate() != nil || seen[status.ID] {
			return invalidPRObservation()
		}
		seen[status.ID] = true
	}
	return nil
}
