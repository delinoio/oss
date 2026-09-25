package rpc

import (
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func ValidateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return domain.Fail(domain.InvalidArgument, "Invalid server URL.", "Use an origin without credentials, query, or path.")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return domain.Fail(domain.PermissionDenied, "Remote server connections require HTTPS.", "Select an authenticated TLS endpoint.")
	}
	return nil
}
func HTTPClient() (*http.Client, *http.Transport) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 15 * time.Second
	transport.MaxResponseHeaderBytes = 32 << 10
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport
}
