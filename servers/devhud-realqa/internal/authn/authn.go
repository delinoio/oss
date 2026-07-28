// Package authn implements RealQA's dual-audience authentication contract.
package authn

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1/realqav1connect"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/authmiddleware"
)

const LifecycleScope = "realqa:lifecycle:delete"

type Interceptor struct {
	feature           authmiddleware.Validator
	forwarded         authmiddleware.Validator
	lifecycleClientID string
}

func New(
	feature authmiddleware.Validator,
	forwarded authmiddleware.Validator,
	lifecycleClientID string,
) (*Interceptor, error) {
	if feature == nil || forwarded == nil {
		return nil, errors.New("realqa auth: both audience validators are required")
	}
	if !validClientID(lifecycleClientID) {
		return nil, errors.New("realqa auth: lifecycle client ID is invalid")
	}
	return &Interceptor{
		feature: feature, forwarded: forwarded,
		lifecycleClientID: lifecycleClientID,
	}, nil
}

func validClientID(value string) bool {
	return value != "" && len(value) <= 255 &&
		strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, " \t\r\n:/")
}

func (interceptor *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		headers := request.Header()
		defer strip(headers)
		if isLifecycleRequest(request) {
			if len(headers.Values(auth.ForwardedUserTokenHeader)) != 0 {
				return nil, &auth.Error{Kind: auth.ErrorMalformedToken}
			}
			token, err := bearer(headers)
			if err != nil {
				return nil, err
			}
			machine, err := interceptor.feature.ValidateM2M(ctx, token, LifecycleScope)
			if err != nil {
				return nil, err
			}
			if machine.Subject != interceptor.lifecycleClientID ||
				machine.ClientID != interceptor.lifecycleClientID ||
				machine.ServiceID != interceptor.lifecycleClientID {
				return nil, &auth.Error{Kind: auth.ErrorTokenType}
			}
			strip(headers)
			ctx = auth.WithPrincipal(ctx, auth.Principal{M2M: machine})
			return next(ctx, request)
		}
		featureScope, forwardedScopes, ok := scopes(request.Spec().Procedure)
		if !ok {
			return nil, errors.New("realqa auth: procedure policy missing")
		}
		token, err := bearer(headers)
		if err != nil {
			return nil, err
		}
		featureUser, err := interceptor.feature.ValidateUser(ctx, token, featureScope)
		if err != nil {
			return nil, err
		}
		forwardedToken, err := forwardedBearer(headers)
		if err != nil {
			return nil, err
		}
		forwardedUser, err := interceptor.forwarded.ValidateUser(
			ctx, forwardedToken, forwardedScopes...,
		)
		if err != nil {
			return nil, forwardedError(err)
		}
		if featureUser.Subject != forwardedUser.Subject ||
			featureUser.UserID != forwardedUser.UserID {
			return nil, &auth.Error{
				Kind: auth.ErrorTokenType, Credential: auth.CredentialForwardedUser,
			}
		}
		strip(headers)
		ctx = auth.WithPrincipal(ctx, auth.Principal{User: featureUser})
		return next(ctx, request)
	}
}

func (interceptor *Interceptor) WrapStreamingClient(
	next connect.StreamingClientFunc,
) connect.StreamingClientFunc {
	return next
}

func (interceptor *Interceptor) WrapStreamingHandler(
	next connect.StreamingHandlerFunc,
) connect.StreamingHandlerFunc {
	return func(context.Context, connect.StreamingHandlerConn) error {
		return errors.New("realqa auth: streaming procedures are not supported")
	}
}

func isLifecycleRequest(request connect.AnyRequest) bool {
	if request.Spec().Procedure !=
		realqav1connect.RealQAPresetServiceDeleteFeatureDataProcedure {
		return false
	}
	message, ok := request.Any().(*realqav1.DeleteFeatureDataRequest)
	if !ok || message == nil {
		return false
	}
	return message.TriggerKind ==
		realqav1.FeatureDeletionTriggerKind_FEATURE_DELETION_TRIGGER_KIND_DELIBASE_ACCOUNT_LIFECYCLE ||
		message.TriggerKind ==
			realqav1.FeatureDeletionTriggerKind_FEATURE_DELETION_TRIGGER_KIND_DELIBASE_ORGANIZATION_LIFECYCLE
}

func scopes(procedure string) (string, []string, bool) {
	const (
		accountRead = "delibase:account:read"
		usage       = "delibase:usage:execute"
	)
	switch procedure {
	case
		realqav1connect.RealQAPresetServiceListPresetsProcedure,
		realqav1connect.RealQAPresetServiceGetPresetProcedure:
		return "realqa:presets:read", []string{accountRead}, true
	case
		realqav1connect.RealQAPresetServiceCreatePresetProcedure,
		realqav1connect.RealQAPresetServiceUpdatePresetProcedure,
		realqav1connect.RealQAPresetServiceDeletePresetProcedure,
		realqav1connect.RealQAPresetServiceDeleteFeatureDataProcedure:
		return "realqa:presets:write", []string{accountRead}, true
	case
		realqav1connect.RealQATrackerServiceGetGitHubConnectionProcedure,
		realqav1connect.RealQATrackerServiceListGitHubInstallationsProcedure,
		realqav1connect.RealQATrackerServiceListRepositoriesProcedure,
		realqav1connect.RealQATrackerServiceGetRepositoryIssueSchemaProcedure:
		return "realqa:tracker:read", []string{accountRead}, true
	case
		realqav1connect.RealQATrackerServiceStartGitHubConnectionProcedure,
		realqav1connect.RealQATrackerServiceDisconnectGitHubConnectionProcedure:
		return "realqa:tracker:write", []string{accountRead}, true
	case
		realqav1connect.RealQASubmissionServiceListSubmissionsProcedure,
		realqav1connect.RealQASubmissionServiceGetSubmissionProcedure:
		return "realqa:submissions:read", []string{accountRead}, true
	case
		realqav1connect.RealQASubmissionServiceCreateSubmissionProcedure,
		realqav1connect.RealQASubmissionServiceCreateImageUploadProcedure,
		realqav1connect.RealQASubmissionServiceFinalizeImageUploadProcedure,
		realqav1connect.RealQASubmissionServiceSubmitIssueProcedure,
		realqav1connect.RealQASubmissionServiceRebindSubmissionStorageAuthorizationProcedure,
		realqav1connect.RealQASubmissionServiceDeleteImageProcedure,
		realqav1connect.RealQASubmissionServiceDeleteSubmissionAssetsProcedure:
		return "realqa:submissions:write", []string{usage}, true
	default:
		return "", nil, false
	}
}

func bearer(headers http.Header) (string, error) {
	values := headers.Values("Authorization")
	if len(values) != 1 {
		return "", &auth.Error{Kind: auth.ErrorMissingToken}
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", &auth.Error{Kind: auth.ErrorMalformedToken}
	}
	return parts[1], nil
}

func forwardedBearer(headers http.Header) (string, error) {
	values := headers.Values(auth.ForwardedUserTokenHeader)
	if len(values) != 1 || strings.TrimSpace(values[0]) != values[0] ||
		values[0] == "" || strings.ContainsAny(values[0], " \t\r\n") {
		return "", &auth.Error{
			Kind: auth.ErrorMissingToken, Credential: auth.CredentialForwardedUser,
		}
	}
	return values[0], nil
}

func forwardedError(err error) error {
	var failure *auth.Error
	if !errors.As(err, &failure) || failure == nil {
		return err
	}
	return &auth.Error{
		Kind: failure.Kind, Credential: auth.CredentialForwardedUser,
	}
}

func strip(headers http.Header) {
	headers.Del("Authorization")
	headers.Del("Proxy-Authorization")
	headers.Del(auth.ForwardedUserTokenHeader)
}
