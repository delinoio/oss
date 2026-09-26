package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type PermissionAction string

const (
	PermissionAsk   PermissionAction = "ask"
	PermissionAllow PermissionAction = "allow"
	PermissionDeny  PermissionAction = "deny"
)

type PermissionRule struct {
	Permission string           `json:"permission"`
	Pattern    string           `json:"pattern"`
	Action     PermissionAction `json:"action"`
}

type SessionSettings struct {
	Title      string           `json:"-"`
	Agent      string           `json:"-"`
	Provider   string           `json:"-"`
	Model      string           `json:"-"`
	Permission []PermissionRule `json:"-"`
}

type SessionMutation string

const (
	CreateSessionMutation   SessionMutation = "create-session"
	SubmitInputMutation     SessionMutation = "submit-input"
	ReplyPermissionMutation SessionMutation = "reply-permission"
	ReplyQuestionMutation   SessionMutation = "reply-question"
	RejectQuestionMutation  SessionMutation = "reject-question"
)

// SessionClaim contains no prompt, credentials or workspace paths. The owning
// coordinator must synchronize this exact claim before returning success. An
// existing or uncertain claim must refuse a new attempt, even across processes.
// Claim persistence, account authority and native process ownership remain
// mandatory integrations; this private transport cannot substitute for them.
type SessionClaim struct {
	RequestID      domain.ID       `json:"request_id"`
	Kind           SessionMutation `json:"kind"`
	SessionID      string          `json:"session_id,omitempty"`
	MessageID      string          `json:"message_id,omitempty"`
	PartID         string          `json:"part_id,omitempty"`
	BodyDigest     string          `json:"body_digest"`
	InputRequestID domain.ID       `json:"input_request_id,omitempty"`
	InteractionID  string          `json:"interaction_id,omitempty"`
	ArrivalID      string          `json:"arrival_id,omitempty"`
	CallID         string          `json:"call_id,omitempty"`
}

type InputReceipt struct {
	RequestID    domain.ID
	SessionID    string
	MessageID    string
	PartID       string
	HTTPAccepted bool
	Recorded     bool
}

// sessionAPI is deliberately not constructible outside this package. Before a
// production owner may supply these fields, its execution initializer must
// prove managed configuration, effective providers and owned server authority.
// Discovery cannot create one or expose session mutations. The native fixtures
// exercise this transport separately from that still-required integration.
type sessionAPI struct {
	client          *http.Client
	origin          string
	password        string
	cwd             string
	claim           func(context.Context, SessionClaim) error
	alive           func() error
	logger          *slog.Logger
	owner           domain.ID
	gate            chan struct{}
	creation        *sessionCreation
	input           *sessionInput
	problem         *domain.Error
	events          *eventStream
	eventAttempt    bool
	observer        *inputObserver
	replyAttempt    *interactionHTTPAttempt
	rejectionPolicy RejectionPolicy
}

type sessionCreation struct {
	request  domain.ID
	settings SessionSettings
	identity sessionIdentity
}

type sessionIdentity struct {
	id      string
	project string
	slug    string
	created int64
}

type sessionInput struct {
	receipt InputReceipt
	digest  [sha256.Size]byte
}

func sessionProblem() *domain.Error {
	return domain.Fail(domain.Unsupported, "OpenCode session evidence does not match its native profile.", "Retain the original claim and native state; do not recreate the session or resend input.")
}

func sessionUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "OpenCode mutation completion could not be confirmed.", "Inspect the original claim and native identities without repeating the mutation.")
}

func sessionConflict() *domain.Error {
	return domain.Fail(domain.Conflict, "This OpenCode mutation scope is already claimed.", "Reconcile its original attempt; observation does not authorize another send.")
}

func sessionInvalid() *domain.Error {
	return domain.Fail(domain.InvalidArgument, "Invalid OpenCode session operation.", "Use the original native identities and complete bounded explicit settings.")
}

func (s *sessionAPI) enter(ctx context.Context) error {
	select {
	case s.gate <- struct{}{}:
		if ctx.Err() == nil {
			return nil
		}
		<-s.gate
	case <-ctx.Done():
	}
	return unavailable()
}

func (s *sessionAPI) leave() { <-s.gate }

func (s *sessionAPI) diagnostic(ctx context.Context, kind SessionMutation, err error, facts ...any) {
	if s.logger != nil {
		fields := []any{"owner_id", s.owner, "operation", kind, "code", domain.SafeError(err).Code}
		s.logger.WarnContext(ctx, "OpenCode session operation needs reconciliation", append(fields, facts...)...)
	}
}

func validSessionSettings(settings SessionSettings) bool {
	if domain.Text(settings.Title, "title", 1024, true) != nil || domain.Text(settings.Agent, "agent", 128, true) != nil || domain.Text(settings.Provider, "provider", 256, true) != nil || domain.Text(settings.Model, "model", 256, true) != nil || len(settings.Permission) == 0 || len(settings.Permission) > 128 {
		return false
	}
	for _, rule := range settings.Permission {
		if domain.Text(rule.Permission, "permission", 128, true) != nil || domain.Text(rule.Pattern, "permission pattern", 4096, true) != nil {
			return false
		}
		switch rule.Action {
		case PermissionAsk, PermissionAllow, PermissionDeny:
		default:
			return false
		}
	}
	return true
}

// Native identifiers are preserved verbatim. This profile follows the pinned
// native identifier generator: twelve lowercase hex timestamp/counter digits
// and fourteen base-62 random characters after the typed prefix. Native session
// IDs are never fabricated from a DeliDev UUID or selected by create callers.
func nativeID(value, prefix string) bool {
	if len(value) != len(prefix)+27 || !strings.HasPrefix(value, prefix+"_") {
		return false
	}
	for i, c := range value[len(prefix)+1:] {
		if i < 12 {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func mutationDigest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func (s *sessionAPI) create(ctx context.Context, request domain.ID, settings SessionSettings) (string, error) {
	if err := s.enter(ctx); err != nil {
		return "", err
	}
	defer s.leave()
	if s.creation != nil {
		return "", sessionConflict()
	}
	if request.Validate() != nil || !validSessionSettings(settings) || s.claim == nil || s.alive == nil || !filepath.IsAbs(s.cwd) || strings.ContainsAny(s.cwd, "\r\n\x00") {
		return "", sessionInvalid()
	}
	if err := s.alive(); err != nil {
		return "", launchError(err)
	}
	settings.Permission = slices.Clone(settings.Permission)
	body, _ := json.Marshal(struct {
		Title      string           `json:"title"`
		Agent      string           `json:"agent"`
		Model      sessionModel     `json:"model"`
		Permission []PermissionRule `json:"permission"`
		Metadata   sessionMetadata  `json:"metadata"`
	}{settings.Title, settings.Agent, sessionModel{settings.Model, settings.Provider}, settings.Permission, sessionMetadata{sessionMarker{request}}})
	if len(body) > maxHTTPBody {
		return "", sessionInvalid()
	}
	// Retain the attempt even if the durable callback loses its response. Neither
	// cancellation nor failure proves that its claim was not synchronized.
	s.creation = &sessionCreation{request: request, settings: settings}
	if err := s.claim(ctx, SessionClaim{RequestID: request, Kind: CreateSessionMutation, BodyDigest: mutationDigest(body)}); err != nil {
		s.diagnostic(ctx, CreateSessionMutation, err, "phase", "claim")
		return "", sessionUncertain()
	}
	raw, status, err := s.request(ctx, http.MethodPost, "/session", body, http.StatusOK)
	if err != nil {
		s.diagnostic(ctx, CreateSessionMutation, err, "phase", "http", "http_status", status)
		return "", sessionUncertain()
	}
	identity, err := validateSession(raw, s.cwd, s.creation, true)
	if err != nil {
		s.diagnostic(ctx, CreateSessionMutation, err, "phase", "native-identity")
		return "", sessionUncertain()
	}
	s.creation.identity = identity
	return identity.id, nil
}

func (s *sessionAPI) inspectSession(ctx context.Context) (string, error) {
	if err := s.enter(ctx); err != nil {
		return "", err
	}
	defer s.leave()
	return s.readSession(ctx)
}

func (s *sessionAPI) readSession(ctx context.Context) (string, error) {
	if s.creation == nil || !nativeID(s.creation.identity.id, "ses") {
		return "", sessionUncertain()
	}
	raw, _, err := s.request(ctx, http.MethodGet, "/session/"+s.creation.identity.id, nil, http.StatusOK)
	if err != nil {
		if domain.SafeError(err).Code == domain.Unsupported {
			s.problem = sessionProblem()
		}
		return "", err
	}
	identity, err := validateSession(raw, s.cwd, s.creation, false)
	if err != nil || identity != s.creation.identity {
		s.problem = sessionProblem()
		return "", sessionProblem()
	}
	return identity.id, nil
}

// submit uses the native API's explicit client-provided message/part IDs. The
// caller must allocate and retain those original native IDs before the claim.
// HTTP 204 acknowledges asynchronous scheduling only. It proves neither a
// stored user message, a model response, completion nor mid-turn Steer support.
func (s *sessionAPI) submit(ctx context.Context, request domain.ID, messageID, partID, text string) (InputReceipt, error) {
	if err := s.enter(ctx); err != nil {
		return InputReceipt{}, err
	}
	defer s.leave()
	if s.input != nil {
		return InputReceipt{}, sessionConflict()
	}
	if s.problem != nil {
		return InputReceipt{}, s.problem
	}
	if s.events != nil {
		if problem := s.events.status(); problem != nil {
			return InputReceipt{}, problem
		}
	}
	if request.Validate() != nil || !nativeID(messageID, "msg") || !nativeID(partID, "prt") || domain.Text(text, "input", 256<<10, true) != nil || s.creation == nil || request == s.creation.request {
		return InputReceipt{}, sessionInvalid()
	}
	id, err := s.readSession(ctx)
	if err != nil {
		return InputReceipt{}, err
	}
	settings := s.creation.settings
	body, _ := json.Marshal(struct {
		MessageID string      `json:"messageID"`
		Model     inputModel  `json:"model"`
		Agent     string      `json:"agent"`
		Parts     []inputPart `json:"parts"`
	}{messageID, inputModel{settings.Provider, settings.Model}, settings.Agent, []inputPart{{partID, "text", text}}})
	if len(body) > maxHTTPBody {
		return InputReceipt{}, sessionInvalid()
	}
	s.input = &sessionInput{receipt: InputReceipt{RequestID: request, SessionID: id, MessageID: messageID, PartID: partID}, digest: sha256.Sum256([]byte(text))}
	claim := SessionClaim{RequestID: request, Kind: SubmitInputMutation, SessionID: id, MessageID: messageID, PartID: partID, BodyDigest: mutationDigest(body)}
	if err := s.claim(ctx, claim); err != nil {
		s.diagnostic(ctx, SubmitInputMutation, err)
		return s.input.receipt, sessionUncertain()
	}
	if _, _, err := s.request(ctx, http.MethodPost, "/session/"+id+"/prompt_async", body, http.StatusNoContent); err != nil {
		s.diagnostic(ctx, SubmitInputMutation, err)
		return s.input.receipt, sessionUncertain()
	}
	s.input.receipt.HTTPAccepted = true
	return s.input.receipt, nil
}

// inspectInput may establish the original user message's exact native storage
// fact after a lost HTTP response. Absence never proves rejection. This cannot
// complete an assistant turn, clear unrelated recovery or permit another send.
func (s *sessionAPI) inspectInput(ctx context.Context) (InputReceipt, error) {
	if err := s.enter(ctx); err != nil {
		return InputReceipt{}, err
	}
	defer s.leave()
	if s.input == nil {
		return InputReceipt{}, sessionInvalid()
	}
	receipt := s.input.receipt
	if _, err := s.readSession(ctx); err != nil {
		return receipt, err
	}
	raw, status, err := s.request(ctx, http.MethodGet, "/session/"+receipt.SessionID+"/message/"+receipt.MessageID, nil, http.StatusOK)
	if err != nil {
		return receipt, err
	}
	if status == http.StatusNotFound {
		// A later disappearance contradicts retained storage evidence; do not
		// erase it or mistake it for permission to replay the original input.
		if receipt.Recorded {
			return receipt, sessionProblem()
		}
		return receipt, nil
	}
	recorded, err := validateStoredInput(raw, s.creation.settings, *s.input)
	if err != nil {
		return receipt, err
	}
	if !recorded {
		if receipt.Recorded {
			return receipt, sessionProblem()
		}
		return receipt, nil
	}
	s.input.receipt.Recorded = true
	return s.input.receipt, nil
}
