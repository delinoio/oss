package server

import (
	"net"
	"net/http"
	"strings"

	"github.com/delinoio/oss/servers/devhud-api/internal/config"
)

func requireHTTPS(environment config.Environment, trustedProxyCIDRs []*net.IPNet, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		developmentLoopback := environment == config.EnvironmentDevelopment && loopbackPeer(request.RemoteAddr)
		if request.TLS != nil || developmentLoopback || forwardedHTTPS(request, trustedProxyCIDRs) {
			next.ServeHTTP(response, request)
			return
		}
		http.Error(response, "HTTPS is required outside loopback", http.StatusUpgradeRequired)
	})
}

func forwardedHTTPS(request *http.Request, trustedProxyCIDRs []*net.IPNet) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	trusted := false
	for _, network := range trustedProxyCIDRs {
		if network.Contains(ip) {
			trusted = true
			break
		}
	}
	forwardedProtocols := request.Header.Values("X-Forwarded-Proto")
	return trusted && len(forwardedProtocols) == 1 && strings.EqualFold(strings.TrimSpace(forwardedProtocols[0]), "https")
}

func loopbackPeer(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = strings.Trim(remoteAddress, "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
