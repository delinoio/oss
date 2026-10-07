// Package apiproxy owns the server-only native API forwarding boundary. It is
// not a general proxy or a source of public API keys. The execution lifecycle
// supplies revocable leases after authenticating its private execution token.
package apiproxy

import (
	"context"
	"encoding/base64"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const Prefix = "/api-proxy/v1"
const TokenPrefix = "ddv_exec_"

type Operation string

const (
	ChatCompletion     Operation = "chat-completion"
	ResponseCreate     Operation = "response-create"
	ResponseCompact    Operation = "response-compact"
	MessageCreate      Operation = "message-create"
	MessageCountTokens Operation = "message-count-tokens"
)

type ReferenceKind = domain.NativeReferenceKind

const (
	ResponseReference     = domain.NativeResponseReference
	ConversationReference = domain.NativeConversationReference
)

// Scope is an immutable server-resolved dispatch binding, never client input.
// Credential generation and revocation belong to the execution/account owner.
type Scope struct {
	CompactionSourceTurn domain.NativeIdentity
	SubscriptionService  domain.SubscriptionService
	ExecutionID          domain.ID
	SessionID            domain.ID
	AccountID            domain.ID
	ConnectionID         domain.ID
	ProviderID           domain.ID
	ModelID              domain.ID
	NativeModel          string
	ChildModel           *domain.ExecutionSubagentModel
	Purpose              domain.UsagePurpose
	TitlePrompt          string
	Effort               string
	ServiceTier          string
	Harness              domain.Harness
	Provider             domain.Provider
	Operations           []Operation
}

func (s Scope) Validate() error {
	if s.SubscriptionService != "" {
		return domain.Fail(domain.PermissionDenied, "Native subscription identity grants no API relay authority.", "Use the protected native subscription lease.")
	}
	for _, id := range []domain.ID{s.ExecutionID, s.SessionID, s.AccountID, s.ConnectionID, s.ProviderID, s.ModelID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if err := domain.Text(s.NativeModel, "proxy model", 256, true); err != nil {
		return err
	}
	if s.ChildModel != nil && (s.Harness != domain.Codex || s.Provider.Protocol != domain.OpenAIResponses || s.Purpose == domain.SessionTitleUsage || s.ChildModel.ModelID.Validate() != nil || s.ChildModel.ModelRevision == 0 || domain.Text(s.ChildModel.NativeModel, "authorized child model", 256, true) != nil) {
		return domain.Fail(domain.PermissionDenied, "Invalid child model relay scope.", "Retain only the canonical Codex child model from the original execution snapshot.")
	}
	if err := s.Provider.Validate(); err != nil {
		return err
	}
	if s.Provider.Protocol == domain.NativeSubscription || len(s.Operations) == 0 || len(s.Operations) > 5 {
		return domain.Fail(domain.Unsupported, "The execution has no compatible API proxy operations.", "Use the installed harness's directly compatible API protocol.")
	}
	if s.Purpose == domain.SessionTitleUsage {
		if s.Provider.Protocol != domain.OpenAIResponses || len(s.Operations) != 1 || s.Operations[0] != ResponseCreate || domain.Text(s.TitlePrompt, "first message for title generation", domain.MaxPromptBytes, true) != nil || domain.Text(s.Effort, "native reasoning effort", 64, false) != nil || domain.Text(s.ServiceTier, "native service tier", 64, false) != nil {
			return domain.Fail(domain.Unsupported, "The automatic title relay scope is not the pinned Codex profile.", "Use one bounded Responses request for the original first message and frozen native settings.")
		}
	} else if s.Purpose != "" && s.Purpose != domain.ConversationUsage {
		return domain.Fail(domain.Unsupported, "Unknown native usage purpose.", "Use the conversation or session-title authority profile.")
	}
	if s.CompactionSourceTurn != "" && (s.Harness != domain.Codex || s.Provider.Protocol != domain.OpenAIResponses || s.CompactionSourceTurn.Validate(domain.Codex, domain.NativeTurnIdentity) != nil || s.ChildModel != nil || s.Purpose == domain.SessionTitleUsage) {
		return domain.CompactionUncertain()
	}
	seen := map[Operation]bool{}
	for _, operation := range s.Operations {
		if seen[operation] || operation.protocol() != s.Provider.Protocol {
			return domain.Fail(domain.Unsupported, "The execution's API operations do not match its provider protocol.", "Use a compatible immutable execution binding.")
		}
		seen[operation] = true
	}
	return nil
}
func (o Operation) protocol() domain.APIProtocol {
	switch o {
	case ChatCompletion:
		return domain.OpenAIChat
	case ResponseCreate, ResponseCompact:
		return domain.OpenAIResponses
	case MessageCreate, MessageCountTokens:
		return domain.AnthropicMessages
	default:
		return ""
	}
}
func operationForPath(path string) Operation {
	switch path {
	case Prefix + "/chat/completions":
		return ChatCompletion
	case Prefix + "/responses":
		return ResponseCreate
	case Prefix + "/responses/compact":
		return ResponseCompact
	case Prefix + "/messages":
		return MessageCreate
	case Prefix + "/messages/count_tokens":
		return MessageCountTokens
	default:
		return ""
	}
}
func (s Scope) allows(operation Operation) bool {
	return operation.protocol() == s.Provider.Protocol && slices.Contains(s.Operations, operation)
}

// Lease.Context must be canceled on execution end/stop, connection revocation,
// machine revocation or server shutdown. Release joins the request's ownership
// in the authority. Key retrieves only this immutable connection's upstream key;
// it is called after request validation, and its returned bytes are cleared.
// Reference callbacks validate resource existence and protocol shape. Owner
// metadata is observational; absent callbacks cannot resolve native references.
type Credential struct {
	Key          []byte
	QuotaProject string
}

type Lease struct {
	Credential func(context.Context) (Credential, error)
	Scope      Scope
	// BindModel narrows this request to a canonical model already authorized by
	// the same original execution/account. It cannot acquire another credential
	// or widen provider, cancellation, operation or native-reference ownership.
	BindModel          func(context.Context, string, Operation) (*Lease, error)
	Context            context.Context
	Key                func(context.Context) ([]byte, error)
	Release            func()
	AuthorizeReference func(context.Context, ReferenceKind, string) error
	ObserveReference   func(context.Context, ReferenceKind, string) error
	BeforeSubmit       func(context.Context, Operation) error
	PublishDiagnostic  func(context.Context, domain.RequestDiagnostic) error
	// ObserveResponseUsage receives only a completed original HTTP response
	// after reflection guards. It is enabled exclusively for manual Codex
	// actions whose resumed native profile has no raw-response notifications.
	ObserveResponseUsage func(context.Context, domain.ID, domain.NativeResponseUsage) error
	// ObserveHistory records only full-history versus account-bound use before
	// any provider side effect. It never retains request content or identifiers.
	ObserveHistory func(context.Context, bool) error
}

type Authority interface {
	Acquire(context.Context, string) (*Lease, error)
}

// ValidToken checks only the private token format, never execution authority.
func ValidToken(value string) bool {
	if !strings.HasPrefix(value, TokenPrefix) {
		return false
	}
	raw := strings.TrimPrefix(value, TokenPrefix)
	bytes, err := base64.RawURLEncoding.DecodeString(raw)
	defer clear(bytes)
	return err == nil && len(bytes) == 32 && base64.RawURLEncoding.EncodeToString(bytes) == raw
}
