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
	ExecutionID  domain.ID
	SessionID    domain.ID
	AccountID    domain.ID
	ConnectionID domain.ID
	ProviderID   domain.ID
	ModelID      domain.ID
	NativeModel  string
	Provider     domain.Provider
	Operations   []Operation
}

func (s Scope) Validate() error {
	for _, id := range []domain.ID{s.ExecutionID, s.SessionID, s.AccountID, s.ConnectionID, s.ProviderID, s.ModelID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if err := domain.Text(s.NativeModel, "proxy model", 256, true); err != nil {
		return err
	}
	if err := s.Provider.Validate(); err != nil {
		return err
	}
	if s.Provider.Protocol == domain.NativeSubscription || len(s.Operations) == 0 || len(s.Operations) > 5 {
		return domain.Fail(domain.Unsupported, "The execution has no compatible API proxy operations.", "Use the installed harness's directly compatible API protocol.")
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
// Reference callbacks must enforce session/account/connection/model ownership;
// absent callbacks refuse native state references instead of trusting an ID.
type Lease struct {
	Scope              Scope
	Context            context.Context
	Key                func(context.Context) ([]byte, error)
	Release            func()
	AuthorizeReference func(context.Context, ReferenceKind, string) error
	ObserveReference   func(context.Context, ReferenceKind, string) error
}

type Authority interface {
	Acquire(context.Context, string) (*Lease, error)
}

func validToken(value string) bool {
	if !strings.HasPrefix(value, TokenPrefix) {
		return false
	}
	raw := strings.TrimPrefix(value, TokenPrefix)
	bytes, err := base64.RawURLEncoding.DecodeString(raw)
	defer clear(bytes)
	return err == nil && len(bytes) == 32 && base64.RawURLEncoding.EncodeToString(bytes) == raw
}
