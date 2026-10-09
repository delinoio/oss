package rpc

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Release builds pin both values with reviewed ldflags; neither is configuration.
var Version = "0.1.0"
var SourceRevision = ""

const ProtocolVersion = 2
const CorrelationHeader = "X-Delidev-Correlation-Id"

func Kind(kind pb.EntityKind) (domain.Kind, error) {
	if kind == pb.EntityKind_ENTITY_KIND_UNSPECIFIED {
		return "", domain.Fail(domain.InvalidArgument, "An entity kind is required.", "Select a resource kind.")
	}
	name, ok := pb.EntityKind_name[int32(kind)]
	if !ok {
		return "", domain.Fail(domain.InvalidArgument, "Unknown entity kind.", "Use a supported versioned kind.")
	}
	result := domain.Kind(strings.ToLower(strings.TrimPrefix(name, "ENTITY_KIND_")))
	if !result.Valid() || result == domain.ModelKind {
		return "", domain.Fail(domain.InvalidArgument, "Unknown entity kind.", "Use a supported versioned kind.")
	}
	return result, nil
}
func WireKind(kind domain.Kind) pb.EntityKind {
	return pb.EntityKind(pb.EntityKind_value["ENTITY_KIND_"+strings.ToUpper(string(kind))])
}
func Resource(record store.Record) *pb.Resource {
	document := record.Data
	if record.Kind == domain.ProviderKind {
		if p, err := store.Decode[domain.Provider](record); err == nil {
			if raw, err := json.Marshal(providers.WithAPIFormats(p)); err == nil {
				document = raw
			}
		}
	}
	if record.Kind == domain.InteractionKind {
		// The once-only policy decision is server-owned durable provenance.
		// Keep the original schema-1 native controller document compatible with
		// Workers that do not own project-settings configuration support.
		var fields map[string]json.RawMessage
		if json.Unmarshal(document, &fields) == nil {
			if _, exists := fields["plan_approval_policy"]; exists {
				delete(fields, "plan_approval_policy")
				if raw, err := json.Marshal(fields); err == nil {
					document = raw
				}
			}
		}
	}
	if record.Kind == domain.TerminalKind {
		document = terminalResourceDocument(document)
	}
	if record.Kind == domain.JobKind {
		document = nativeModelJobDocument(document)
	}
	return &pb.Resource{Id: string(record.ID), Kind: WireKind(record.Kind), Revision: record.Revision, SessionId: string(record.SessionID), ProjectId: string(record.ProjectID), SchemaVersion: ResourceSchemaVersion(record.Kind, document), DocumentJson: document, CreatedAt: record.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: record.UpdatedAt.Format(time.RFC3339Nano)}
}
func Error(err error, correlation string) error {
	if err == nil {
		return nil
	}
	safe := domain.SafeError(err)
	code := connect.CodeInternal
	switch safe.Code {
	case domain.InvalidArgument, domain.MissingInput:
		code = connect.CodeInvalidArgument
	case domain.NotFound:
		code = connect.CodeNotFound
	case domain.Conflict:
		code = connect.CodeAborted
	case domain.Unauthenticated:
		code = connect.CodeUnauthenticated
	case domain.PermissionDenied:
		code = connect.CodePermissionDenied
	case domain.Unavailable, domain.ServerUnavailable:
		code = connect.CodeUnavailable
	case domain.ConfirmationRequired, domain.RecoveryRequired, domain.BudgetReached, domain.ProviderDisabled:
		code = connect.CodeFailedPrecondition
	case domain.Unsupported:
		code = connect.CodeUnimplemented
	case domain.ResourceExhausted:
		code = connect.CodeResourceExhausted
	case domain.CursorExpired:
		code = connect.CodeOutOfRange
	case domain.Canceled:
		code = connect.CodeCanceled
	}
	result := connect.NewError(code, errors.New(safe.Message))
	result.Meta().Set(CorrelationHeader, correlation)
	detail, detailErr := connect.NewErrorDetail(&pb.ErrorDetail{Code: string(safe.Code), Guidance: safe.Guidance, Cause: safe.Cause, CorrelationId: correlation})
	if detailErr == nil {
		result.AddDetail(detail)
	}
	return result
}
func ClientError(err error) *domain.Error {
	var connected *connect.Error
	if errors.As(err, &connected) {
		for _, detail := range connected.Details() {
			value, e := detail.Value()
			if e != nil {
				continue
			}
			if v, ok := value.(*pb.ErrorDetail); ok {
				return &domain.Error{Code: domain.Code(v.Code), Message: connected.Message(), Guidance: v.Guidance, Cause: v.Cause, CorrelationID: v.CorrelationId}
			}
		}
		if connected.Code() == connect.CodeUnauthenticated {
			return domain.Fail(domain.Unauthenticated, "The server rejected authentication.", "Use the selected server's owner credential or pair this device again.")
		}
	}
	return domain.Fail(domain.ServerUnavailable, "The selected DeliDev server is unavailable.", "Start it explicitly with `delidev server start`, or check the configured remote connection.")
}
func CopyCorrelation[T any](response *connect.Response[T], request http.Header) {
	response.Header().Set(CorrelationHeader, request.Get(CorrelationHeader))
}

func ResourceSchemaVersion(kind domain.Kind, raw []byte) uint32 {
	if kind == domain.AgentKind {
		return 4
	}
	if kind == domain.ProjectKind || kind == domain.SettingsKind {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) == nil {
			var behavior map[string]json.RawMessage
			_ = json.Unmarshal(fields["settings"], &behavior)
			if fields["plan_mode_default"] != nil || fields["branch_prefix"] != nil || behavior["plan_mode_default"] != nil || behavior["branch_prefix"] != nil {
				return 3
			}
		}
		if json.Unmarshal(raw, &fields) == nil && (fields["settings"] != nil || fields["automatic_plan_approval"] != nil) {
			return 2
		}
	}
	var identity struct {
		APIProtocol             domain.APIProtocol         `json:"api_protocol"`
		APIFormats              []domain.ProviderAPIFormat `json:"api_formats"`
		Routes                  []json.RawMessage          `json:"routes"`
		Type                    domain.AccountType         `json:"type"`
		SourceKind              domain.ModelSourceKind     `json:"source_kind"`
		ReconfigurationRequired bool                       `json:"reconfiguration_required"`
		Retired                 bool                       `json:"retired"`
	}
	if json.Unmarshal(raw, &identity) == nil && (kind == domain.AccountKind && identity.Type == domain.APIAccount && identity.APIProtocol.API() || kind == domain.ProviderKind && len(identity.APIFormats) > 0) {
		return 3
	}
	if json.Unmarshal(raw, &identity) == nil && kind == domain.AgentKind && len(identity.Routes) > 0 {
		return 3
	}
	if json.Unmarshal(raw, &identity) == nil && ((kind == domain.AccountKind && identity.Type == domain.SubscriptionAccount) || (kind == domain.ModelKind && identity.SourceKind == domain.SubscriptionModel) || identity.ReconfigurationRequired || identity.Retired) {
		return 2
	}
	return 1
}

func APIProtocol(v pb.ApiProtocol) domain.APIProtocol {
	switch v {
	case pb.ApiProtocol_API_PROTOCOL_UNSPECIFIED:
		return ""
	case pb.ApiProtocol_API_PROTOCOL_OPENAI_RESPONSES:
		return domain.OpenAIResponses
	case pb.ApiProtocol_API_PROTOCOL_OPENAI_CHAT:
		return domain.OpenAIChat
	case pb.ApiProtocol_API_PROTOCOL_ANTHROPIC_MESSAGES:
		return domain.AnthropicMessages
	default:
		return "unsupported"
	}
}

func WireAPIFormat(f domain.ProviderAPIFormat) *pb.ProviderApiFormat {
	protocol := map[domain.APIProtocol]pb.ApiProtocol{domain.OpenAIResponses: pb.ApiProtocol_API_PROTOCOL_OPENAI_RESPONSES, domain.OpenAIChat: pb.ApiProtocol_API_PROTOCOL_OPENAI_CHAT, domain.AnthropicMessages: pb.ApiProtocol_API_PROTOCOL_ANTHROPIC_MESSAGES}[f.Protocol]
	auth := map[domain.Authentication]pb.ApiAuthentication{domain.BearerAuth: pb.ApiAuthentication_API_AUTHENTICATION_BEARER, domain.APIKeyAuth: pb.ApiAuthentication_API_AUTHENTICATION_API_KEY, domain.KeylessAuth: pb.ApiAuthentication_API_AUTHENTICATION_KEYLESS}[f.Authentication]
	return &pb.ProviderApiFormat{Protocol: protocol, Endpoint: f.Endpoint, Authentication: auth}
}

func SubscriptionService(v pb.SubscriptionServiceIdentity) domain.SubscriptionService {
	switch v {
	case pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_UNSPECIFIED:
		return ""
	case pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CHATGPT:
		return domain.SubscriptionChatGPT
	case pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CLAUDE:
		return domain.SubscriptionClaude
	case pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_OPENCODE_GO:
		return domain.SubscriptionOpenCodeGo
	case pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_GROK:
		return domain.SubscriptionGrok
	default:
		return "unsupported"
	}
}
func WireSubscriptionService(v domain.SubscriptionService) pb.SubscriptionServiceIdentity {
	switch v {
	case domain.SubscriptionChatGPT:
		return pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CHATGPT
	case domain.SubscriptionClaude:
		return pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CLAUDE
	case domain.SubscriptionOpenCodeGo:
		return pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_OPENCODE_GO
	case domain.SubscriptionGrok:
		return pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_GROK
	default:
		return pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_UNSPECIFIED
	}
}
