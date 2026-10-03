package rpc

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const Version = "0.1.0"
const ProtocolVersion = 1
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
	if !result.Valid() {
		return "", domain.Fail(domain.InvalidArgument, "Unknown entity kind.", "Use a supported versioned kind.")
	}
	return result, nil
}
func WireKind(kind domain.Kind) pb.EntityKind {
	return pb.EntityKind(pb.EntityKind_value["ENTITY_KIND_"+strings.ToUpper(string(kind))])
}
func Resource(record store.Record) *pb.Resource {
	document := record.Data
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
	var identity struct {
		Type                    domain.AccountType     `json:"type"`
		SourceKind              domain.ModelSourceKind `json:"source_kind"`
		ReconfigurationRequired bool                   `json:"reconfiguration_required"`
		Retired                 bool                   `json:"retired"`
	}
	if json.Unmarshal(raw, &identity) == nil && ((kind == domain.AccountKind && identity.Type == domain.SubscriptionAccount) || (kind == domain.ModelKind && identity.SourceKind == domain.SubscriptionModel) || identity.ReconfigurationRequired || identity.Retired) {
		return 2
	}
	return 1
}

func SubscriptionService(v pb.SubscriptionServiceIdentity) domain.SubscriptionService {
	switch v {
	case pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_UNSPECIFIED:
		return ""
	case pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CHATGPT:
		return domain.SubscriptionChatGPT
	case pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CLAUDE:
		return domain.SubscriptionClaude
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
	case domain.SubscriptionGrok:
		return pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_GROK
	default:
		return pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_UNSPECIFIED
	}
}
