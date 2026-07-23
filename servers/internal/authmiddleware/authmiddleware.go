// Package authmiddleware authenticates Logto identities for HTTP and Connect
// transports and attaches typed claims to context. It never makes delibase
// organization, team, billing, or meter authorization decisions.
package authmiddleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/safeerr"
)

// Mode declares the authentication identities required by a route.
type Mode uint8

const (
	ModeInvalid Mode = iota
	ModePublic
	ModeUser
	ModeM2M
	ModeM2MWithForwardedUser
)

// Requirement declares authentication and Logto API scopes. Scopes are
// authentication inputs only and do not replace local authorization.
type Requirement struct {
	Mode       Mode
	UserScopes []string
	M2MScopes  []string
}

// Validator is implemented by auth.Validator and is easy to fake in transport
// tests.
type Validator interface {
	ValidateUser(context.Context, string, ...string) (*auth.UserClaims, error)
	ValidateM2M(context.Context, string, ...string) (*auth.M2MClaims, error)
}

// HTTP authenticates requests according to a request-aware policy.
func HTTP(validator Validator, policy func(*http.Request) Requirement) (func(http.Handler) http.Handler, error) {
	if validator == nil {
		return nil, errors.New("auth middleware: validator is required")
	}
	if policy == nil {
		return nil, errors.New("auth middleware: policy is required")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			requirement := policy(request)
			principal, err := authenticate(request.Context(), request.Header, validator, requirement)
			stripCredentials(request.Header)
			if err != nil {
				safeerr.WriteHTTP(writer, err)
				return
			}
			ctx := request.Context()
			if requirement.Mode != ModePublic {
				ctx = auth.WithPrincipal(ctx, principal)
			}
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	}, nil
}

// ConnectPolicy maps a fully qualified Connect procedure to an auth contract.
type ConnectPolicy func(string) Requirement

// NewConnect constructs an authentication interceptor.
func NewConnect(validator Validator, policy ConnectPolicy) (*ConnectInterceptor, error) {
	if validator == nil {
		return nil, errors.New("auth middleware: validator is required")
	}
	if policy == nil {
		return nil, errors.New("auth middleware: policy is required")
	}
	return &ConnectInterceptor{validator: validator, policy: policy}, nil
}

// ConnectInterceptor authenticates unary and streaming handlers.
type ConnectInterceptor struct {
	validator Validator
	policy    ConnectPolicy
}

func (i *ConnectInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		requirement := i.policy(request.Spec().Procedure)
		principal, err := authenticate(ctx, request.Header(), i.validator, requirement)
		stripCredentials(request.Header())
		if err != nil {
			return nil, safeerr.Connect(err)
		}
		if requirement.Mode != ModePublic {
			ctx = auth.WithPrincipal(ctx, principal)
		}
		return next(ctx, request)
	}
}

func (i *ConnectInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *ConnectInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, connection connect.StreamingHandlerConn) error {
		requirement := i.policy(connection.Spec().Procedure)
		principal, err := authenticate(ctx, connection.RequestHeader(), i.validator, requirement)
		stripCredentials(connection.RequestHeader())
		if err != nil {
			return safeerr.Connect(err)
		}
		if requirement.Mode != ModePublic {
			ctx = auth.WithPrincipal(ctx, principal)
		}
		return next(ctx, connection)
	}
}

func authenticate(
	ctx context.Context,
	headers http.Header,
	validator Validator,
	requirement Requirement,
) (auth.Principal, error) {
	var principal auth.Principal
	switch requirement.Mode {
	case ModePublic:
		return principal, nil
	case ModeUser:
		token, err := bearerToken(headers)
		if err != nil {
			return principal, err
		}
		principal.User, err = validator.ValidateUser(ctx, token, requirement.UserScopes...)
		return principal, err
	case ModeM2M:
		token, err := bearerToken(headers)
		if err != nil {
			return principal, err
		}
		principal.M2M, err = validator.ValidateM2M(ctx, token, requirement.M2MScopes...)
		return principal, err
	case ModeM2MWithForwardedUser:
		token, err := bearerToken(headers)
		if err != nil {
			return principal, err
		}
		principal.M2M, err = validator.ValidateM2M(ctx, token, requirement.M2MScopes...)
		if err != nil {
			return principal, err
		}
		forwarded, err := forwardedUserToken(headers)
		if err != nil {
			return principal, err
		}
		principal.User, err = validator.ValidateUser(ctx, forwarded, requirement.UserScopes...)
		return principal, forwardedUserError(err)
	default:
		return principal, safeerr.New(safeerr.ClassInternal)
	}
}

func bearerToken(headers http.Header) (string, error) {
	values := headers.Values("Authorization")
	if len(values) != 1 {
		return "", &auth.Error{Kind: auth.ErrorMissingToken}
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", &auth.Error{Kind: auth.ErrorMalformedToken}
	}
	return parts[1], nil
}

func forwardedUserToken(headers http.Header) (string, error) {
	values := headers.Values(auth.ForwardedUserTokenHeader)
	if len(values) != 1 {
		return "", &auth.Error{
			Kind:       auth.ErrorMissingToken,
			Credential: auth.CredentialForwardedUser,
		}
	}
	token := strings.TrimSpace(values[0])
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", &auth.Error{
			Kind:       auth.ErrorMalformedToken,
			Credential: auth.CredentialForwardedUser,
		}
	}
	return token, nil
}

func forwardedUserError(err error) error {
	var failure *auth.Error
	if !errors.As(err, &failure) || failure == nil {
		return err
	}
	return &auth.Error{
		Kind:       failure.Kind,
		Credential: auth.CredentialForwardedUser,
	}
}

func stripCredentials(headers http.Header) {
	headers.Del("Authorization")
	headers.Del("Proxy-Authorization")
	headers.Del(auth.ForwardedUserTokenHeader)
}
