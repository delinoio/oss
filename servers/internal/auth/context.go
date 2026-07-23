package auth

import "context"

// Principal contains validated authentication identities. A usage request has
// both M2M and User set; human calls have only User set.
type Principal struct {
	User *UserClaims
	M2M  *M2MClaims
}

type principalContextKey struct{}

// WithPrincipal attaches validated claims to a request context.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext returns validated claims attached by shared middleware.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}
