package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/delinoio/oss/servers/devhud-api/internal/config"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

var ErrUnauthenticated = errors.New("request is unauthenticated")

const defaultVerificationTimeout = 5 * time.Second

type Verifier interface {
	Verify(context.Context, string) (domain.Identity, error)
}

type LogtoVerifier struct {
	issuer  string
	keys    [][]byte
	verify  *oidc.IDTokenVerifier
	timeout time.Duration
}

func NewLogtoVerifier(ctx context.Context, issuer, audience string, hmacKeys [][]byte) (*LogtoVerifier, error) {
	return newLogtoVerifier(ctx, issuer, audience, hmacKeys, defaultVerificationTimeout)
}

func newLogtoVerifier(ctx context.Context, issuer, audience string, hmacKeys [][]byte, timeout time.Duration) (*LogtoVerifier, error) {
	httpClient := &http.Client{Timeout: timeout}
	providerContext := oidc.ClientContext(context.WithoutCancel(ctx), httpClient)
	provider, err := oidc.NewProvider(providerContext, issuer)
	if err != nil {
		return nil, err
	}
	return &LogtoVerifier{
		issuer:  issuer,
		keys:    hmacKeys,
		verify:  provider.Verifier(&oidc.Config{ClientID: audience}),
		timeout: timeout,
	}, nil
}

func (v *LogtoVerifier) Verify(ctx context.Context, authorization string) (domain.Identity, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) || len(authorization) == len(prefix) || strings.Contains(authorization[len(prefix):], " ") {
		return domain.Identity{}, ErrUnauthenticated
	}
	verificationContext, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	token, err := v.verify.Verify(verificationContext, authorization[len(prefix):])
	if err != nil || token.Subject == "" || token.Issuer != v.issuer {
		return domain.Identity{}, ErrUnauthenticated
	}
	var claims struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := token.Claims(&claims); err != nil {
		return domain.Identity{}, ErrUnauthenticated
	}
	return domain.Identity{
		Issuer:                token.Issuer,
		Subject:               token.Subject,
		DisplayName:           claims.Name,
		Email:                 claims.Email,
		Fingerprint:           config.IdentityFingerprint(v.keys, token.Issuer, token.Subject),
		FingerprintCandidates: config.IdentityFingerprintCandidates(v.keys, token.Issuer, token.Subject),
	}, nil
}
