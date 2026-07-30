// Package authn implements Deck's dual-audience credential boundary.
package authn

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1/deckv1connect"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/rpcerr"
	"github.com/delinoio/oss/servers/internal/auth"
)

const (
	ForwardedDelibaseTokenHeader = "X-Devhud-Deck-Forwarded-Delibase-Token"
	DeviceRevocationGrantHeader  = "X-Devhud-Deck-Device-Revocation-Grant"
	LifecycleScope               = "deck:lifecycle:delete"
)

var forwardedDirectoryScopes = []string{
	"delibase:account:read",
	"delibase:organizations:read",
	"delibase:teams:read",
}

type Validator interface {
	ValidateUser(context.Context, string, ...string) (*auth.UserClaims, error)
	ValidateM2M(context.Context, string, ...string) (*auth.M2MClaims, error)
}

type Dependencies struct {
	DeckValidator            Validator
	DelibaseValidator        Validator
	Directory                contracts.Directory
	LifecycleClientID        string
	RequireLifecycleClientID bool
}

type viewerContextKey struct{}
type forwardedTokenContextKey struct{}
type cleanupGrantContextKey struct{}
type lifecycleContextKey struct{}

func ViewerFromContext(ctx context.Context) (contracts.Viewer, bool) {
	viewer, ok := ctx.Value(viewerContextKey{}).(contracts.Viewer)
	return viewer, ok
}

// ForwardedDelibaseTokenFromContext returns the already validated live-user
// bearer only while the originating request is active. The interceptor removes
// the credential from request headers, and no refresh code may persist it or
// copy it into a detached context.
func ForwardedDelibaseTokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(forwardedTokenContextKey{}).(string)
	return token, ok && token != ""
}

func CleanupGrantFromContext(ctx context.Context) (string, bool) {
	grant, ok := ctx.Value(cleanupGrantContextKey{}).(string)
	return grant, ok && grant != ""
}

func IsLifecycle(ctx context.Context) bool {
	value, _ := ctx.Value(lifecycleContextKey{}).(bool)
	return value
}

func New(dependencies Dependencies) (*Interceptor, error) {
	if dependencies.DeckValidator == nil || dependencies.DelibaseValidator == nil ||
		dependencies.Directory == nil {
		return nil, errors.New("deck auth: validators and directory are required")
	}
	if dependencies.RequireLifecycleClientID &&
		(strings.TrimSpace(dependencies.LifecycleClientID) == "" ||
			strings.ContainsAny(dependencies.LifecycleClientID, " \t\r\n")) {
		return nil, errors.New("deck auth: lifecycle client ID is required")
	}
	return &Interceptor{dependencies: dependencies}, nil
}

type Interceptor struct {
	dependencies Dependencies
}

func (interceptor *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		request connect.AnyRequest,
	) (connect.AnyResponse, error) {
		procedure := request.Spec().Procedure
		if lifecycleRequest(procedure, request.Any()) {
			principal, err := interceptor.authenticateLifecycle(ctx, request.Header())
			stripCredentials(request.Header())
			if err != nil {
				return nil, err
			}
			ctx = auth.WithPrincipal(ctx, auth.Principal{M2M: principal})
			ctx = context.WithValue(ctx, lifecycleContextKey{}, true)
			return next(ctx, request)
		}
		if procedure == deckv1connect.DeckDeviceServiceUnregisterDeviceProcedure {
			grant := singleHeader(request.Header(), DeviceRevocationGrantHeader)
			authorization := request.Header().Values("Authorization")
			if grant != "" {
				if len(authorization) != 0 ||
					len(request.Header().Values(ForwardedDelibaseTokenHeader)) != 0 {
					stripCredentials(request.Header())
					return nil, rpcerr.New(connect.CodeUnauthenticated,
						deckv1.ErrorReason_ERROR_REASON_INVALID_CREDENTIALS)
				}
				stripCredentials(request.Header())
				return next(context.WithValue(ctx, cleanupGrantContextKey{}, grant), request)
			}
		}
		viewer, deckClaims, forwardedToken, err := interceptor.authenticateHumanWithToken(
			ctx, request.Header(), deckScopes(procedure), procedure)
		stripCredentials(request.Header())
		if err != nil {
			return nil, err
		}
		ctx = auth.WithPrincipal(ctx, auth.Principal{User: deckClaims})
		ctx = context.WithValue(ctx, viewerContextKey{}, viewer)
		ctx = context.WithValue(ctx, forwardedTokenContextKey{}, forwardedToken)
		return next(ctx, request)
	}
}

func (interceptor *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (interceptor *Interceptor) WrapStreamingHandler(
	next connect.StreamingHandlerFunc,
) connect.StreamingHandlerFunc {
	return func(context.Context, connect.StreamingHandlerConn) error {
		return rpcerr.New(connect.CodeUnimplemented,
			deckv1.ErrorReason_ERROR_REASON_UNSUPPORTED_ACTION)
	}
}

func (interceptor *Interceptor) authenticateHuman(
	ctx context.Context,
	headers http.Header,
	scopes []string,
) (contracts.Viewer, *auth.UserClaims, error) {
	viewer, claims, _, err := interceptor.authenticateHumanWithToken(
		ctx, headers, scopes, "")
	return viewer, claims, err
}

func (interceptor *Interceptor) authenticateHumanWithToken(
	ctx context.Context,
	headers http.Header,
	scopes []string,
	procedure string,
) (contracts.Viewer, *auth.UserClaims, string, error) {
	if len(scopes) == 0 {
		return contracts.Viewer{}, nil, "", rpcerr.New(connect.CodePermissionDenied,
			deckv1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
	}
	deckToken, err := bearerToken(headers)
	if err != nil {
		return contracts.Viewer{}, nil, "", authenticationError(err)
	}
	forwardedToken, err := credentialHeader(headers, ForwardedDelibaseTokenHeader)
	if err != nil {
		return contracts.Viewer{}, nil, "", authenticationError(err)
	}
	deckClaims, err := interceptor.dependencies.DeckValidator.ValidateUser(
		ctx, deckToken, scopes...)
	if err != nil {
		return contracts.Viewer{}, nil, "", authenticationError(err)
	}
	forwardedClaims, err := interceptor.dependencies.DelibaseValidator.ValidateUser(
		ctx, forwardedToken, forwardedScopes(procedure)...)
	if err != nil {
		return contracts.Viewer{}, nil, "", authenticationError(err)
	}
	if deckClaims.Subject == "" || deckClaims.Subject != forwardedClaims.Subject {
		return contracts.Viewer{}, nil, "", rpcerr.New(connect.CodeUnauthenticated,
			deckv1.ErrorReason_ERROR_REASON_SUBJECT_MISMATCH)
	}
	viewer, err := interceptor.dependencies.Directory.ResolveViewer(ctx, deckClaims.Subject)
	if err != nil || viewer.Subject != deckClaims.Subject || viewer.AccountID.Version() != 7 {
		return contracts.Viewer{}, nil, "", rpcerr.New(connect.CodePermissionDenied,
			deckv1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
	}
	return viewer, deckClaims, forwardedToken, nil
}

func forwardedScopes(procedure string) []string {
	scopes := append([]string(nil), forwardedDirectoryScopes...)
	if procedure == deckv1connect.DeckViewServiceRefreshViewProcedure {
		scopes = append(scopes, "delibase:usage:execute")
	}
	return scopes
}

func (interceptor *Interceptor) authenticateLifecycle(
	ctx context.Context,
	headers http.Header,
) (*auth.M2MClaims, error) {
	if len(headers.Values(ForwardedDelibaseTokenHeader)) != 0 ||
		len(headers.Values(DeviceRevocationGrantHeader)) != 0 {
		return nil, rpcerr.New(connect.CodeUnauthenticated,
			deckv1.ErrorReason_ERROR_REASON_INVALID_CREDENTIALS)
	}
	token, err := bearerToken(headers)
	if err != nil {
		return nil, authenticationError(err)
	}
	claims, err := interceptor.dependencies.DeckValidator.ValidateM2M(
		ctx, token, LifecycleScope)
	if err != nil {
		return nil, authenticationError(err)
	}
	expected := interceptor.dependencies.LifecycleClientID
	if expected == "" || claims.Subject != expected || claims.ClientID != expected ||
		claims.ServiceID != expected {
		return nil, rpcerr.New(connect.CodePermissionDenied,
			deckv1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
	}
	return claims, nil
}

func lifecycleRequest(procedure string, message any) bool {
	if procedure != deckv1connect.DeckViewServiceDeleteFeatureDataProcedure {
		return false
	}
	request, ok := message.(*deckv1.DeleteFeatureDataRequest)
	return ok && request.GetDelibaseLifecycle() != nil
}

func deckScopes(procedure string) []string {
	switch procedure {
	case deckv1connect.DeckViewServiceListViewsProcedure,
		deckv1connect.DeckViewServiceGetViewProcedure,
		deckv1connect.DeckViewServiceListPullRequestsProcedure,
		deckv1connect.DeckViewServiceListPullRequestMutationCandidatesProcedure,
		deckv1connect.DeckViewServiceGetRefreshPreflightProcedure:
		return []string{"deck:views:read"}
	case deckv1connect.DeckViewServiceCreateViewProcedure,
		deckv1connect.DeckViewServiceUpdateViewProcedure,
		deckv1connect.DeckViewServiceDeleteViewProcedure,
		deckv1connect.DeckViewServiceRefreshViewProcedure,
		deckv1connect.DeckViewServiceMutatePullRequestProcedure,
		deckv1connect.DeckViewServiceDeleteFeatureDataProcedure:
		return []string{"deck:views:write"}
	case deckv1connect.DeckIntegrationServiceGetGitHubConnectionProcedure,
		deckv1connect.DeckIntegrationServiceListGitHubInstallationsProcedure:
		return []string{"deck:integrations:read"}
	case deckv1connect.DeckIntegrationServiceStartGitHubConnectionProcedure,
		deckv1connect.DeckIntegrationServiceDisconnectGitHubConnectionProcedure:
		return []string{"deck:integrations:write"}
	case deckv1connect.DeckDeviceServiceGetDeviceProcedure,
		deckv1connect.DeckDeviceServiceRegisterDeviceProcedure,
		deckv1connect.DeckDeviceServiceUpdateDeviceProcedure,
		deckv1connect.DeckDeviceServiceUnregisterDeviceProcedure,
		deckv1connect.DeckDeviceServiceUpdateViewNotificationPreferenceProcedure,
		deckv1connect.DeckDeviceServiceResolveNotificationEventProcedure:
		return []string{"deck:devices:write"}
	default:
		return nil
	}
}

func bearerToken(headers http.Header) (string, error) {
	values := headers.Values("Authorization")
	if len(values) != 1 {
		return "", errors.New("missing bearer")
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", errors.New("malformed bearer")
	}
	return parts[1], nil
}

func credentialHeader(headers http.Header, name string) (string, error) {
	values := headers.Values(name)
	if len(values) != 1 {
		return "", errors.New("missing credential")
	}
	value := strings.TrimSpace(values[0])
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return "", errors.New("malformed credential")
	}
	return value, nil
}

func singleHeader(headers http.Header, name string) string {
	value, err := credentialHeader(headers, name)
	if err != nil {
		return ""
	}
	return value
}

func stripCredentials(headers http.Header) {
	headers.Del("Authorization")
	headers.Del("Proxy-Authorization")
	headers.Del(ForwardedDelibaseTokenHeader)
	headers.Del(DeviceRevocationGrantHeader)
}

func authenticationError(err error) error {
	var failure *auth.Error
	if errors.As(err, &failure) && failure.Kind == auth.ErrorKeyUnavailable {
		return rpcerr.New(connect.CodeUnavailable,
			deckv1.ErrorReason_ERROR_REASON_DEPENDENCY_UNAVAILABLE)
	}
	return rpcerr.New(connect.CodeUnauthenticated,
		deckv1.ErrorReason_ERROR_REASON_INVALID_CREDENTIALS)
}
