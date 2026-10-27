// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"net/http"
	"strings"
)

// ValidateHeaders validates personal custom request headers. Authoritative auth,
// account identity, internal adapter controls and transport framing stay owned by
// the selected credential/adapter. Case duplicates are rejected, not map-order-dependent.
func ValidateHeaders(headers map[string]string) error {
	if len(headers) > 64 {
		return ErrConnection
	}
	seen := map[string]bool{}
	total := 0
	for name, value := range headers {
		if len(name) == 0 || len(name) > 256 || len(value) > 8192 {
			return ErrConnection
		}
		for _, b := range []byte(name) {
			if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b))) {
				return ErrConnection
			}
		}
		for _, b := range []byte(value) {
			if b < 32 || b == 127 {
				return ErrConnection
			}
		}
		key := strings.ToLower(name)
		if seen[key] || strings.HasPrefix(key, "x-aiprouter-") {
			return ErrConnection
		}
		seen[key] = true
		switch key {
		case "authorization", "proxy-authorization", "proxy-authenticate", "www-authenticate", "cookie", "set-cookie", "x-api-key", "api-key", "x-goog-api-key", "x-dsh-auth-token", "x-auth-token", "x-refresh-token", "x-amz-target", "x-goog-user-project", "chatgpt-account-id", "x-user-id", "x-userid", "x-enterprise-id", "x-tenant-id", "x-domain", "host", "connection", "proxy-connection", "keep-alive", "te", "trailer", "transfer-encoding", "upgrade", "content-length":
			return ErrConnection
		}
		total += len(name) + len(value)
		if total > 32768 {
			return ErrConnection
		}
	}
	return nil
}

// Per-model overrides are the upstream Chat/Responses switch. They cannot
// change an Anthropic/Google key scheme or bypass a specialized wire adapter.
func validModelWireOverride(p Preset, w Wire) bool {
	return (p.Wire == Chat || p.Wire == Responses) && (w == Chat || w == Responses)
}

func overlayHeaders(h http.Header, custom map[string]string) {
	for k, v := range custom {
		h.Set(k, v)
	}
}
