package opencode

import (
	"context"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxHTTPBody = 1 << 20

func probeHTTPClient(address string) (*http.Client, *http.Transport) {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	transport := &http.Transport{
		Proxy: nil, DisableCompression: true, DisableKeepAlives: true,
		MaxResponseHeaderBytes: 16 << 10, ResponseHeaderTimeout: 2 * time.Second,
		DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
			if network != "tcp" || target != address {
				return nil, incompatible()
			}
			return dialer.DialContext(ctx, "tcp4", address)
		},
	}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport
}

func readHTTP(ctx context.Context, client *http.Client, origin, path, password string, expected int) ([]byte, error) {
	// This closed read set prevents discovery from creating project instances,
	// refreshing providers or mutating native configuration/session state.
	switch path {
	case "/global/health", "/global/config", "/doc":
	default:
		return nil, incompatible()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
	if err != nil {
		return nil, unavailable()
	}
	request.Header.Set("Accept", "application/json")
	if password != "" {
		request.SetBasicAuth("delidev", password)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, unavailable()
	}
	defer response.Body.Close()
	if response.StatusCode != expected || len(response.Header.Values("Content-Encoding")) != 0 {
		return nil, incompatible()
	}
	if response.ContentLength > maxHTTPBody {
		return nil, httpBound()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPBody+1))
	if err != nil {
		return nil, unavailable()
	}
	if len(raw) > maxHTTPBody {
		return nil, httpBound()
	}
	if expected == http.StatusUnauthorized {
		values := response.Header.Values("Www-Authenticate")
		if len(values) != 1 || values[0] != `Basic realm="Secure Area"` {
			return nil, incompatible()
		}
		return nil, nil
	}
	values := response.Header.Values("Content-Type")
	if len(values) != 1 {
		return nil, incompatible()
	}
	media, params, err := mime.ParseMediaType(values[0])
	if err != nil || media != "application/json" || len(params) != 0 {
		return nil, incompatible()
	}
	return raw, nil
}

func httpBound() *domain.Error {
	return domain.Fail(domain.ResourceExhausted, "OpenCode readiness response exceeded its bound.", "Use a supported native protocol profile and refresh discovery.")
}
