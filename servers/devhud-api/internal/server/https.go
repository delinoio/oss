package server

import (
	"net"
	"net/http"
	"strings"
)

func requireHTTPS(trustedProxyCIDRs []*net.IPNet, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.TLS != nil || loopbackPeer(request.RemoteAddr) || forwardedHTTPS(request, trustedProxyCIDRs) {
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
	return trusted && strings.EqualFold(strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func loopbackPeer(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = strings.Trim(remoteAddress, "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
