package domain

import (
	"strconv"
	"time"
)

type IntegrationProvider string

const GitHubCom IntegrationProvider = "github.com"

type PATKind string

const (
	FineGrainedPAT PATKind = "fine-grained"
	ClassicPAT     PATKind = "classic"
)

// IntegrationDefinition is the entire editable profile. Token type and owner
// are explicit selections, not inferred proof of permissions or repository access.
type IntegrationDefinition struct {
	Name          string              `json:"name"`
	Provider      IntegrationProvider `json:"provider"`
	TokenKind     PATKind             `json:"token_kind"`
	ResourceOwner string              `json:"resource_owner,omitempty"`
}

func (d IntegrationDefinition) Validate() error {
	if err := Text(d.Name, "integration name", 160, true); err != nil {
		return err
	}
	if d.Provider != GitHubCom || (d.TokenKind != FineGrainedPAT && d.TokenKind != ClassicPAT) {
		return Fail(InvalidArgument, "Unsupported integration profile.", "Select GitHub.com and a fine-grained or classic personal access token.")
	}
	if (d.TokenKind == FineGrainedPAT || d.ResourceOwner != "") && !GitHubOwner(d.ResourceOwner) {
		return Fail(InvalidArgument, "A valid GitHub resource owner is required.", "Name the user or organization that owns the selected repositories; use a separate fine-grained profile for another owner.")
	}
	return nil
}

func GitHubOwner(value string) bool {
	if len(value) == 0 || len(value) > 100 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

type GitHubIdentity struct {
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
	Login  string `json:"login"`
}

func (i GitHubIdentity) Validate() error {
	id, err := strconv.ParseUint(i.ID, 10, 64)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != i.ID || !GitHubOwner(i.Login) || Text(i.NodeID, "GitHub node identity", 256, true) != nil {
		return Fail(RecoveryRequired, "The observed GitHub identity is invalid.", "Validate the selected profile again; no identity or access is inferred.")
	}
	return nil
}

type IntegrationValidationState string

const (
	IntegrationIdentityVerified IntegrationValidationState = "identity-verified"
	IntegrationInvalidToken     IntegrationValidationState = "invalid-token"
	IntegrationAccessRestricted IntegrationValidationState = "access-restricted"
	IntegrationSSORequired      IntegrationValidationState = "sso-required"
	IntegrationRateLimited      IntegrationValidationState = "rate-limited"
	IntegrationUnavailable      IntegrationValidationState = "unavailable"
)

type IntegrationValidation struct {
	GenerationID ID                         `json:"generation_id"`
	State        IntegrationValidationState `json:"state"`
	CheckedAt    time.Time                  `json:"checked_at"`
	Identity     *GitHubIdentity            `json:"identity,omitempty"`
	Problem      *Error                     `json:"problem,omitempty"`
}

func (v IntegrationValidation) Validate() error {
	if v.GenerationID.Validate() != nil || v.CheckedAt.IsZero() {
		return integrationCorrupt()
	}
	if v.State == IntegrationIdentityVerified {
		if v.Identity == nil || v.Identity.Validate() != nil || v.Problem != nil {
			return integrationCorrupt()
		}
		return nil
	}
	switch v.State {
	case IntegrationInvalidToken, IntegrationAccessRestricted, IntegrationSSORequired, IntegrationRateLimited, IntegrationUnavailable:
		if v.Identity != nil || v.Problem == nil {
			return integrationCorrupt()
		}
	default:
		return integrationCorrupt()
	}
	return nil
}

type IntegrationConnection struct {
	GenerationID ID                     `json:"generation_id"`
	ConnectedAt  time.Time              `json:"connected_at"`
	Validation   *IntegrationValidation `json:"validation,omitempty"`
}

type IntegrationOperation string

const (
	IntegrationReplaceToken IntegrationOperation = "replace-token"
	IntegrationDelete       IntegrationOperation = "delete-profile"
)

// Pending operations contain no token. Their durable request identity and
// completion identity coordinate native storage and independent SQLite commits.
type IntegrationPending struct {
	RequestID        ID                   `json:"request_id"`
	CompletionID     ID                   `json:"completion_id"`
	ExpectedRevision uint64               `json:"expected_revision,string"`
	Operation        IntegrationOperation `json:"operation"`
	GenerationID     ID                   `json:"generation_id,omitempty"`
	StartedAt        time.Time            `json:"started_at"`
	Problem          *Error               `json:"problem,omitempty"`
}

type Integration struct {
	IntegrationDefinition
	Connection *IntegrationConnection `json:"connection,omitempty"`
	Pending    *IntegrationPending    `json:"pending,omitempty"`
}

func (i Integration) Validate() error {
	if err := i.IntegrationDefinition.Validate(); err != nil {
		return err
	}
	if p := i.Pending; p != nil {
		if i.Connection != nil || p.RequestID.Validate() != nil || p.CompletionID.Validate() != nil || p.RequestID == p.CompletionID || p.StartedAt.IsZero() || p.ExpectedRevision == 0 {
			return integrationCorrupt()
		}
		switch p.Operation {
		case IntegrationReplaceToken:
			if p.GenerationID != p.RequestID {
				return integrationCorrupt()
			}
		case IntegrationDelete:
			if p.GenerationID != "" {
				return integrationCorrupt()
			}
		default:
			return integrationCorrupt()
		}
	}
	if c := i.Connection; c != nil {
		if c.GenerationID.Validate() != nil || c.ConnectedAt.IsZero() {
			return integrationCorrupt()
		}
		if c.Validation != nil && (c.Validation.GenerationID != c.GenerationID || c.Validation.Validate() != nil) {
			return integrationCorrupt()
		}
	}
	return nil
}

func integrationCorrupt() error {
	return Fail(RecoveryRequired, "The integration profile requires reconciliation.", "Keep the original token generation and complete its pending operation before using it.")
}
