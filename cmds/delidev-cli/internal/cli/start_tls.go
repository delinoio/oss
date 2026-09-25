package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/url"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

// Startup is local infrastructure: the explicitly supplied certificate is the
// peer authority even when its DNS names do not name the listener's bind IP.
// This configuration is never used by ordinary or remote product clients.
func startupTLS(config server.Config) (*tls.Config, error) {
	if config.TLSCertificate == "" {
		return nil, nil
	}
	pair, err := tls.LoadX509KeyPair(config.TLSCertificate, config.TLSKey)
	if err != nil {
		return nil, domain.Fail(domain.InvalidArgument, "The TLS certificate or key could not be loaded.", "Check the configured files and matching certificate/key without printing key contents.")
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, domain.Fail(domain.InvalidArgument, "The TLS certificate is invalid.", "Supply a valid server certificate and matching key.")
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Replace hostname/CA discovery only for this local readiness request.
		// The handshake still proves possession of the exact configured key;
		// VerifyConnection enforces the certificate pin, lifetime and server use.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 || !bytes.Equal(state.PeerCertificates[0].Raw, certificate.Raw) {
				return errors.New("startup certificate mismatch")
			}
			_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
			return err
		},
	}, nil
}

func startupClient(o options, input io.Reader, tlsConfig *tls.Config) (client, error) {
	c, err := connectClient(o, input)
	if err != nil {
		return client{}, err
	}
	if tlsConfig == nil {
		return c, nil
	}
	endpoint, err := url.Parse(c.endpoint)
	if err != nil || o.server != "" || endpoint.Scheme != "https" {
		c.transport.CloseIdleConnections()
		return client{}, domain.Fail(domain.InvalidArgument, "TLS readiness requires the local server endpoint.", "Select the server data directory and its configured TLS files.")
	}
	c.transport.TLSClientConfig = tlsConfig.Clone()
	if ip := net.ParseIP(endpoint.Hostname()); ip != nil && ip.IsUnspecified() {
		// A wildcard is a bind address, not a portable dial destination.
		host := "::1"
		if ip.To4() != nil {
			host = "127.0.0.1"
		}
		destination := net.JoinHostPort(host, endpoint.Port())
		dial := c.transport.DialContext
		c.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != endpoint.Host {
				return nil, errors.New("startup destination mismatch")
			}
			return dial(ctx, network, destination)
		}
	}
	return c, nil
}
