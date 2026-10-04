// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
)

const openRouterExchangeURL = "https://openrouter.ai/api/v1/auth/keys"

type ownedOAuthExchange struct{ route outbound.Resolver }

func (owner ownedOAuthExchange) Exchange(ctx context.Context, code, verifier []byte) ([]byte, error) {
	if owner.route == nil {
		return nil, oauthProblem()
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 20 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true}
	defer transport.CloseIdleConnections()
	return exchangeOAuthHTTP(ctx, code, verifier, &outbound.Transport{Base: transport, Resolve: owner.route})
}

// The transport seam permits isolated TLS fixtures without configuring an
// exchange destination. Production always supplies the owned explicit route.
func exchangeOAuthHTTP(ctx context.Context, code, verifier []byte, transport http.RoundTripper) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	payload := make([]byte, 0, 128+6*(len(code)+len(verifier)))
	payload = append(payload, `{"code":`...)
	payload = appendOAuthJSONString(payload, code)
	payload = append(payload, `,"code_verifier":`...)
	payload = appendOAuthJSONString(payload, verifier)
	payload = append(payload, `,"code_challenge_method":"S256"}`...)
	defer clear(payload)
	if !json.Valid(payload) {
		return nil, oauthProblem()
	}
	// A fresh transport with keep-alives disabled cannot replay on a reused
	// connection. An opaque reader also supplies no GetBody retry authority.
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterExchangeURL, io.NopCloser(bytes.NewReader(payload)))
	if err != nil {
		return nil, oauthProblem()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return nil, oauthProblem()
	}
	defer response.Body.Close()
	buffer := make([]byte, (64<<10)+1)
	defer clear(buffer)
	n, err := io.ReadFull(response.Body, buffer)
	if (err != io.EOF && err != io.ErrUnexpectedEOF) || n > 64<<10 || response.StatusCode != http.StatusOK {
		return nil, oauthProblem()
	}
	body := buffer[:n]
	fields, err := decodeOAuthSecretObject(body, 0)
	if err != nil {
		return nil, oauthProblem()
	}
	defer func() {
		for _, value := range fields {
			clear(value)
		}
	}()
	key, err := decodeOAuthASCIIKey(fields["key"])
	if err != nil || domain.ValidateAPIKey(key, false) != nil {
		clear(key)
		return nil, oauthProblem()
	}
	return key, nil
}

// Secret inputs and output remain owned byte buffers. No secret is converted
// to an immutable Go string while building or decoding the exchange payload.
func appendOAuthJSONString(dst, value []byte) []byte {
	dst = append(dst, '"')
	const digits = "0123456789abcdef"
	for _, b := range value {
		switch b {
		case '"', '\\':
			dst = append(dst, '\\', b)
		default:
			if b < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0', digits[b>>4], digits[b&15])
			} else {
				dst = append(dst, b)
			}
		}
	}
	return append(dst, '"')
}

// API keys have a closed printable-ASCII contract. Decode JSON escapes directly
// into that bounded output, refusing Unicode/control values outside the contract.
func decodeOAuthASCIIKey(raw []byte) (key []byte, returned error) {
	defer func() {
		if returned != nil {
			clear(key)
			key = nil
		}
	}()
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' || !json.Valid(raw) {
		return nil, oauthProblem()
	}
	key = make([]byte, 0, min(len(raw)-2, domain.MaxAPIKeyBytes))
	for i := 1; i < len(raw)-1; i++ {
		b := raw[i]
		if b == '\\' {
			i++
			if i >= len(raw)-1 {
				return key, oauthProblem()
			}
			switch raw[i] {
			case '"', '\\', '/':
				b = raw[i]
			case 'u':
				if i+4 >= len(raw)-1 {
					return key, oauthProblem()
				}
				var value uint16
				for _, h := range raw[i+1 : i+5] {
					value <<= 4
					switch {
					case h >= '0' && h <= '9':
						value |= uint16(h - '0')
					case h >= 'a' && h <= 'f':
						value |= uint16(h - 'a' + 10)
					case h >= 'A' && h <= 'F':
						value |= uint16(h - 'A' + 10)
					default:
						return key, oauthProblem()
					}
				}
				i += 4
				if value < 0x21 || value > 0x7e {
					return key, oauthProblem()
				}
				b = byte(value)
			default:
				return key, oauthProblem()
			}
		}
		if b < 0x21 || b > 0x7e || len(key) >= domain.MaxAPIKeyBytes {
			return key, oauthProblem()
		}
		key = append(key, b)
	}
	if len(key) == 0 {
		return key, oauthProblem()
	}
	return key, nil
}

// Decode secret response values exclusively as owned RawMessage bytes. Decoder
// tokens are limited to public object field names and structural delimiters;
// credential values never pass through Token or domain's string-backed reader.
func decodeOAuthSecretObject(raw []byte, depth int) (fields map[string]json.RawMessage, returned error) {
	fields = make(map[string]json.RawMessage)
	defer func() {
		if returned != nil {
			for _, value := range fields {
				clear(value)
			}
			fields = nil
		}
	}()
	if depth > 64 || !utf8.Valid(raw) || !json.Valid(raw) {
		return fields, oauthProblem()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return fields, oauthProblem()
	}
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return fields, oauthProblem()
		}
		if _, exists := fields[name]; exists {
			return fields, oauthProblem()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			clear(value)
			return fields, oauthProblem()
		}
		fields[name] = value
		if checkOAuthSecretJSON(value, depth+1) != nil {
			return fields, oauthProblem()
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return fields, oauthProblem()
	}
	var extra json.RawMessage
	defer clear(extra)
	if d.Decode(&extra) != io.EOF {
		return fields, oauthProblem()
	}
	return fields, nil
}
func checkOAuthSecretJSON(raw []byte, depth int) error {
	if depth > 64 {
		return oauthProblem()
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return oauthProblem()
	}
	if raw[0] == '{' {
		fields, err := decodeOAuthSecretObject(raw, depth)
		for _, value := range fields {
			clear(value)
		}
		return err
	}
	if raw[0] == '[' {
		d := json.NewDecoder(bytes.NewReader(raw))
		if _, err := d.Token(); err != nil {
			return oauthProblem()
		}
		for d.More() {
			var value json.RawMessage
			err := d.Decode(&value)
			if err == nil {
				err = checkOAuthSecretJSON(value, depth+1)
			}
			clear(value)
			if err != nil {
				return oauthProblem()
			}
		}
		token, err := d.Token()
		if err != nil || token != json.Delim(']') {
			return oauthProblem()
		}
	}
	return nil
}
