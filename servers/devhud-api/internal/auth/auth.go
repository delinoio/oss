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

var (
	ErrUnauthenticated         = errors.New("request is unauthenticated")
	ErrVerificationUnavailable = errors.New("identity verification is unavailable")
)

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
	httpClient := &http.Client{
		Timeout:   timeout,
		Transport: availabilityTransport{base: http.DefaultTransport},
	}
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
	parts := strings.Fields(authorization)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return domain.Identity{}, ErrUnauthenticated
	}
	verificationContext, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	token, err := v.verify.Verify(verificationContext, parts[1])
	if err != nil {
		if ctx.Err() != nil {
			return domain.Identity{}, ctx.Err()
		}
		// go-oidc flattens key-set errors with %v, so the stable sentinel text is
		// required when the original error chain is no longer available.
		operationalFailure := errors.Is(err, ErrVerificationUnavailable) ||
			strings.Contains(err.Error(), ErrVerificationUnavailable.Error()) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) ||
			verificationContext.Err() != nil
		if operationalFailure {
			return domain.Identity{}, errors.Join(ErrVerificationUnavailable, err)
		}
		return domain.Identity{}, ErrUnauthenticated
	}
	if token.Subject == "" || token.Issuer != v.issuer {
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

type availabilityTransport struct {
	base http.RoundTripper
}

func (t availabilityTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, errors.Join(ErrVerificationUnavailable, err)
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError {
		_ = response.Body.Close()
		return nil, errors.Join(ErrVerificationUnavailable, errors.New("identity provider returned a transient HTTP failure"))
	}
	return response, nil
}
