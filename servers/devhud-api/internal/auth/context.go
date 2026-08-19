package auth

import (
	"context"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

type principalContextKey struct{}

type Principal struct {
	User  domain.User
	Roles []string
}

func WithUser(ctx context.Context, user domain.User) context.Context {
	return WithPrincipal(ctx, Principal{User: user})
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func UserFromContext(ctx context.Context) (domain.User, bool) {
	principal, ok := PrincipalFromContext(ctx)
	return principal.User, ok
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}
