// Package nativeprofile owns the persistent DeliDev-owned native profiles used
// by every Codex, Claude Code, OpenCode and Grok launch path.
//
// A profile is a private directory that keeps native configuration, history,
// caches and native-created helper files across operations. It is resolved from
// a semantic Key (owner, harness, purpose, account and conversation), never
// from an execution or request identifier. Per-operation process journals stay
// in the process package and are independent of profile lifetime.
package nativeprofile

import (
	"log/slog"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Version is the only supported on-disk version of the owner record and the
// local profile registry. Other versions are rejected without being rewritten.
const Version = 1

// Purpose is a closed set of profile purposes.
type Purpose string

const (
	// System purposes are accountless and keyed by harness only.
	PurposeDiscovery        Purpose = "discovery"
	PurposeModelObservation Purpose = "model_observation"
	PurposeTitleProbe       Purpose = "title_probe"
	// Account purposes are keyed by account and never hold conversation state.
	PurposeAuth              Purpose = "auth"
	PurposeQuota             Purpose = "quota"
	PurposeCredit            Purpose = "credit"
	PurposeSubscriptionProbe Purpose = "subscription_probe"
	// PurposeSharedOwner is the account-wide Codex history home shared by the
	// members of one managed Codex account owner. It is valid only for the Codex
	// harness, is registered in place at its fixed per-account path through
	// RegisterLegacy, is never created by Acquire, and never holds a
	// per-conversation history.
	PurposeSharedOwner Purpose = "shared_owner"
	// Conversation purposes are keyed by account and a logical native history.
	PurposeConversation Purpose = "conversation"
	PurposeFork         Purpose = "fork"
	PurposeCompaction   Purpose = "compaction"
	PurposeRevert       Purpose = "revert"
	PurposeSidechat     Purpose = "sidechat"
	PurposeTitle        Purpose = "title"
	PurposeReview       Purpose = "review"
)

// Scope classifies which Key fields a Purpose requires.
type Scope string

const (
	ScopeSystem       Scope = "system"
	ScopeAccount      Scope = "account"
	ScopeConversation Scope = "conversation"
)

var purposeScopes = map[Purpose]Scope{
	PurposeDiscovery: ScopeSystem, PurposeModelObservation: ScopeSystem, PurposeTitleProbe: ScopeSystem,
	PurposeAuth: ScopeAccount, PurposeQuota: ScopeAccount, PurposeCredit: ScopeAccount, PurposeSubscriptionProbe: ScopeAccount, PurposeSharedOwner: ScopeAccount,
	PurposeConversation: ScopeConversation, PurposeFork: ScopeConversation, PurposeCompaction: ScopeConversation,
	PurposeRevert: ScopeConversation, PurposeSidechat: ScopeConversation, PurposeTitle: ScopeConversation, PurposeReview: ScopeConversation,
}

// Scope returns the key scope required by the purpose, or false when unknown.
func (p Purpose) Scope() (Scope, bool) { s, ok := purposeScopes[p]; return s, ok }

// RegisteredOnly reports whether profiles of the purpose enter the registry
// only through RegisterLegacy. Acquire never creates a managed profile for
// them; it only locks an entry that was already registered in place.
func (p Purpose) RegisteredOnly() bool { return p == PurposeSharedOwner }

// harnessAllowed reports whether the purpose is defined for the harness. Every
// purpose except the registered-only shared owner is defined for all harnesses.
func (p Purpose) harnessAllowed(h domain.Harness) bool {
	if p == PurposeSharedOwner {
		return h == domain.Codex
	}
	return true
}

// OwnerKind identifies whether the server or a Worker owns a storage root.
type OwnerKind string

const (
	OwnerServer OwnerKind = "server"
	OwnerWorker OwnerKind = "worker"
)

// Key is the semantic identity of one profile. Credential rotation never
// changes a Key. Server and Worker files stay local to their own owner.
type Key struct {
	Owner        OwnerKind      `json:"owner"`
	Server       domain.ID      `json:"server,omitempty"`
	Machine      domain.ID      `json:"machine,omitempty"`
	Harness      domain.Harness `json:"harness"`
	Purpose      Purpose        `json:"purpose"`
	Account      domain.ID      `json:"account,omitempty"`
	Conversation domain.ID      `json:"conversation,omitempty"`
}

func invalidKey() error {
	return domain.Fail(domain.InvalidArgument, "The native profile key is invalid.", "Use a supported harness, purpose and DeliDev identifiers.")
}

// Validate rejects unsupported combinations before any filesystem access.
func (k Key) Validate() error {
	switch k.Harness {
	case domain.Codex, domain.ClaudeCode, domain.OpenCode, domain.GrokBuild:
	default:
		return invalidKey()
	}
	scope, ok := k.Purpose.Scope()
	if !ok || !k.Purpose.harnessAllowed(k.Harness) {
		return invalidKey()
	}
	switch k.Owner {
	case OwnerServer:
		if k.Machine != "" {
			return invalidKey()
		}
	case OwnerWorker:
		if k.Machine.Validate() != nil {
			return invalidKey()
		}
	default:
		return invalidKey()
	}
	if k.Server != "" && k.Server.Validate() != nil {
		return invalidKey()
	}
	switch scope {
	case ScopeSystem:
		if k.Account != "" || k.Conversation != "" {
			return invalidKey()
		}
	case ScopeAccount:
		if k.Account.Validate() != nil || k.Conversation != "" {
			return invalidKey()
		}
	case ScopeConversation:
		if k.Account.Validate() != nil || k.Conversation.Validate() != nil {
			return invalidKey()
		}
	}
	return nil
}

// String is a stable collision-free canonical form used only as a map key.
func (k Key) String() string {
	return strings.Join([]string{string(k.Owner), string(k.Server), string(k.Machine), string(k.Harness), string(k.Purpose), string(k.Account), string(k.Conversation)}, "|")
}

// Origin states how a profile directory entered the registry.
type Origin string

const (
	// OriginManaged profiles live at native-profiles/p/<id>.
	OriginManaged Origin = "managed"
	// OriginLegacy profiles are verified pre-registry history directories that
	// stay at their original path.
	OriginLegacy Origin = "legacy"
)

// ReferenceKind is a closed set of reasons a profile must be retained.
type ReferenceKind string

const (
	ReferenceSession    ReferenceKind = "session"
	ReferenceCheckpoint ReferenceKind = "checkpoint"
	ReferenceAccount    ReferenceKind = "account"
	ReferenceConnection ReferenceKind = "connection"
)

// Reference keeps a profile from deletion until its owner explicitly removes it.
type Reference struct {
	Kind ReferenceKind `json:"kind"`
	ID   domain.ID     `json:"id"`
}

// Reason is the closed vocabulary of profile log reasons and blocking causes.
type Reason string

const (
	ReasonCreated            Reason = "created"
	ReasonReused             Reason = "reused"
	ReasonRegisteredLegacy   Reason = "registered_legacy"
	ReasonHistoryRecorded    Reason = "history_recorded"
	ReasonBusy               Reason = "profile_busy"
	ReasonOwnerInvalid       Reason = "owner_invalid"
	ReasonIncompatible       Reason = "incompatible_version"
	ReasonRegistryInvalid    Reason = "registry_invalid"
	ReasonLegacyUnverified   Reason = "legacy_unverified"
	ReasonKeyConflict        Reason = "key_conflict"
	ReasonCredentialPresent  Reason = "credential_file_present"
	ReasonReferenced         Reason = "referenced"
	ReasonHelperRetained     Reason = "helper_retained"
	ReasonDeleted            Reason = "deleted"
	ReasonDeleteBlocked      Reason = "delete_blocked"
	ReasonCredentialCleaned  Reason = "credential_cleaned"
	ReasonCredentialBlocked  Reason = "credential_cleanup_blocked"
	ReasonCredentialRetained Reason = "credential_retained"
	ReasonProcessUncertain   Reason = "process_uncertain"
	ReasonMigrationVerified  Reason = "migration_verified"
	ReasonMigrationSettled   Reason = "migration_settled"
	ReasonMigrationRetained  Reason = "migration_fence_retained"
	ReasonMigrationCompleted Reason = "migration_already_complete"
	ReasonCreateRolledBack   Reason = "create_rolled_back"
)

// Failure is a safe profile error. Reason is also published as the domain
// error Cause so callers can distinguish retained data from process or
// credential uncertainty without parsing text.
func Failure(code domain.Code, reason Reason, message, guidance string) error {
	e := domain.Fail(code, message, guidance)
	e.Cause = string(reason)
	return e
}

// Reasoned extracts the closed reason of a profile failure.
func Reasoned(err error) (Reason, bool) {
	e := domain.SafeError(err)
	if e == nil || e.Cause == "" {
		return "", false
	}
	return Reason(e.Cause), true
}

// Info describes a registered profile without exposing native contents.
type Info struct {
	ID      domain.ID
	Key     Key
	Origin  Origin
	Root    string
	Created bool
}

// Log records a profile event with closed fields only. Paths, credentials and
// native contents never enter these logs.
func Log(logger *slog.Logger, event string, key Key, id domain.ID, reason Reason, extra ...any) {
	if logger == nil {
		return
	}
	attrs := []any{"profile_id", id, "owner", key.Owner, "harness", key.Harness, "purpose", key.Purpose, "reason", reason}
	logger.Info(event, append(attrs, extra...)...)
}
