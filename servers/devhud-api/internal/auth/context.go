package auth

import (
	"context"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

type principalContextKey struct{}

func WithUser(ctx context.Context, user domain.User) context.Context {
	return context.WithValue(ctx, principalContextKey{}, user)
}

func UserFromContext(ctx context.Context) (domain.User, bool) {
	user, ok := ctx.Value(principalContextKey{}).(domain.User)
	return user, ok
}
