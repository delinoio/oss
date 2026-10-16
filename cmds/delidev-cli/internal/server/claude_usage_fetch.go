// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
)

// The one provider request of the Claude usage tracking. The endpoint is an
// internal Anthropic interface, not a documented stable API, so the request is
// pinned exactly: a fixed HTTPS origin and path, the usage OAuth Bearer, the
// OAuth beta header, a JSON content type and a DeliDev user agent. It is a
// read-only GET: no inference, no credit use, no website session and no
// transcript scan.
const (
	claudeUsageEndpoint     = "https://api.anthropic.com/api/oauth/usage"
	claudeUsageBetaHeader   = "oauth-2025-04-20"
	claudeUsageUserAgent    = "DeliDev/0.1.0"
	claudeUsageFetchTimeout = 5 * time.Second
)

// claudeUsageFetchKind is the closed classification of one provider answer.
type claudeUsageFetchKind string

const (
	claudeFetchOK           claudeUsageFetchKind = "fetched"
	claudeFetchUnauthorized claudeUsageFetchKind = "unauthorized"
	claudeFetchForbidden    claudeUsageFetchKind = "forbidden"
	claudeFetchRateLimited  claudeUsageFetchKind = "rate-limited"
	claudeFetchUnavailable  claudeUsageFetchKind = "unavailable"
	claudeFetchMalformed    claudeUsageFetchKind = "malformed"
	claudeFetchEmpty        claudeUsageFetchKind = "empty"
	// claudeFetchInterrupted marks a request that the owner canceled. It is never
	// a provider answer and never produces a failure record.
	claudeFetchInterrupted claudeUsageFetchKind = "interrupted"
)

// claudeUsageFetchResult is the secret-free result of one GET.
type claudeUsageFetchResult struct {
	Kind  claudeUsageFetchKind
	Quota domain.ClaudeUsageQuota
	// RetryAfter is the raw Retry-After header of a throttling answer.
	RetryAfter string
}

// claudeUsageClient builds the one-shot client of a usage read. It never
// follows a redirect, reuses no connection and applies the configured server
// network route: a route that cannot carry the request fails the read and the
// request never falls back to a direct connection.
func (s *Service) claudeUsageClient() (*http.Client, func()) {
	if s.claudeUsageTransport != nil {
		return &http.Client{Transport: s.claudeUsageTransport, Timeout: claudeUsageFetchTimeout, CheckRedirect: refuseClaudeUsageRedirect}, func() {}
	}
	base := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: claudeUsageFetchTimeout}).DialContext, TLSHandshakeTimeout: claudeUsageFetchTimeout, ResponseHeaderTimeout: claudeUsageFetchTimeout, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true}
	return &http.Client{Transport: &outbound.Transport{Base: base, Resolve: s.outboundResolver()}, Timeout: claudeUsageFetchTimeout, CheckRedirect: refuseClaudeUsageRedirect}, base.CloseIdleConnections
}

func refuseClaudeUsageRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// fetchClaudeUsage performs the one usage GET with the Bearer token and
// normalizes a successful answer. The token is never logged; callers log only
// the closed kind.
func (s *Service) fetchClaudeUsage(ctx context.Context, token []byte) claudeUsageFetchResult {
	client, release := s.claudeUsageClient()
	defer release()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeUsageEndpoint, nil)
	if err != nil {
		return claudeUsageFetchResult{Kind: claudeFetchUnavailable}
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	request.Header.Set("anthropic-beta", claudeUsageBetaHeader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", claudeUsageUserAgent)
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return claudeUsageFetchResult{Kind: claudeFetchInterrupted}
		}
		return claudeUsageFetchResult{Kind: claudeFetchUnavailable}
	}
	defer response.Body.Close()
	result := claudeUsageFetchResult{RetryAfter: response.Header.Get("Retry-After")}
	switch {
	case response.StatusCode == http.StatusUnauthorized:
		result.Kind = claudeFetchUnauthorized
	case response.StatusCode == http.StatusForbidden:
		result.Kind = claudeFetchForbidden
	case response.StatusCode == http.StatusTooManyRequests:
		result.Kind = claudeFetchRateLimited
	case response.StatusCode < 200 || response.StatusCode > 299:
		// Includes an unfollowed redirect and every server error.
		result.Kind = claudeFetchUnavailable
	default:
		// One byte past the bound proves an oversized body without reading it all.
		body, readErr := io.ReadAll(io.LimitReader(response.Body, domain.MaxClaudeUsageResponseBytes+1))
		defer clear(body)
		if readErr != nil {
			if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return claudeUsageFetchResult{Kind: claudeFetchInterrupted}
			}
			// A truncated or timed out body is an unusable answer.
			result.Kind = claudeFetchUnavailable
			break
		}
		observedAt := s.claudeUsageNow()
		quota, parseErr := domain.ParseClaudeUsageQuota(body, observedAt)
		switch {
		case parseErr == nil:
			result.Kind, result.Quota = claudeFetchOK, quota
		case domain.SafeError(parseErr).Cause == domain.ClaudeUsageEmptyCause:
			result.Kind = claudeFetchEmpty
		default:
			result.Kind = claudeFetchMalformed
		}
	}
	return result
}

// failure maps a non-success kind to the closed durable failure.
func (k claudeUsageFetchKind) failure() domain.ClaudeUsageFailure {
	switch k {
	case claudeFetchUnauthorized:
		return domain.ClaudeUsageFailureUnauthorized
	case claudeFetchForbidden:
		return domain.ClaudeUsageFailureForbidden
	case claudeFetchRateLimited:
		return domain.ClaudeUsageFailureRateLimited
	case claudeFetchMalformed:
		return domain.ClaudeUsageFailureMalformed
	case claudeFetchEmpty:
		return domain.ClaudeUsageFailureEmpty
	}
	return domain.ClaudeUsageFailureUnavailable
}

func (s *Service) claudeUsageNow() time.Time {
	if s.claudeUsageClock != nil {
		return s.claudeUsageClock().UTC()
	}
	return time.Now().UTC()
}
