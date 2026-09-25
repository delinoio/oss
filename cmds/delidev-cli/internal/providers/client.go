// Package providers owns bounded, non-inference requests from the server to a
// configured model API. It never receives Worker-selected destinations/headers.
package providers

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	maxBody   = 4 << 20
	maxModels = 10000
	maxPages  = 32
	Timeout   = 20 * time.Second
)

type Failure string

const (
	NoFailure              Failure = ""
	AuthenticationRejected Failure = "authentication-rejected"
	AccessDenied           Failure = "access-denied"
	RateLimited            Failure = "rate-limited"
	Unsupported            Failure = "unsupported"
	InvalidResponse        Failure = "invalid-response"
	ResponseTooLarge       Failure = "response-too-large"
	RedirectRefused        Failure = "redirect-refused"
	NetworkFailure         Failure = "network-failure"
	TLSFailure             Failure = "tls-failure"
	TimedOut               Failure = "timed-out"
	Canceled               Failure = "canceled"
	ProviderUnavailable    Failure = "provider-unavailable"
)

// AuthenticationEvidence is deliberately separate from endpoint reachability.
// A compatible custom/public model catalog cannot prove credential validity.
type AuthenticationEvidence = domain.AuthenticationEvidence

const (
	CredentialAccepted    = domain.CredentialAccepted
	KeylessEndpoint       = domain.KeylessEndpoint
	AuthenticationUnknown = domain.AuthenticationUnknown
)

type Model struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	ContextLimit     *uint64  `json:"context_limit,omitempty"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
	Tools            *bool    `json:"tools,omitempty"`
	Reasoning        *bool    `json:"reasoning,omitempty"`
}
type Observation struct {
	Failure           Failure                `json:"failure,omitempty"`
	HTTPStatus        int                    `json:"http_status,omitempty"`
	RetryAfterSeconds *uint32                `json:"retry_after_seconds,omitempty"`
	Authentication    AuthenticationEvidence `json:"authentication"`
	ObservedAt        time.Time              `json:"observed_at"`
	Models            []Model                `json:"models,omitempty"`
}

func (o Observation) Problem() *domain.Error {
	code := domain.Unavailable
	message := "The provider could not complete the non-inference check."
	guidance := "Inspect the configured endpoint and retry explicitly; no inference was sent."
	switch o.Failure {
	case NoFailure:
		return nil
	case AuthenticationRejected:
		code, message = domain.Unauthenticated, "The provider rejected the configured credential."
		guidance = "Check the selected account's key; invalid, expired and revoked keys may share this response."
	case AccessDenied:
		code, message = domain.PermissionDenied, "The provider denied this non-inference operation."
	case RateLimited:
		code, message = domain.ResourceExhausted, "The provider limited the request."
		guidance = "Honor the observed retry delay and retry explicitly; rate limiting is not proof of quota exhaustion."
	case Unsupported:
		code, message = domain.Unsupported, "The endpoint does not support this non-inference check."
	case InvalidResponse:
		message = "The provider returned an invalid or incompatible model response."
	case ResponseTooLarge:
		code, message = domain.ResourceExhausted, "The provider response exceeds the catalog bounds."
	case RedirectRefused:
		message = "The provider redirected the request; the redirect was refused."
	case TLSFailure:
		message = "The provider's TLS identity could not be verified."
	case TimedOut:
		message = "The provider check exceeded its time bound."
	case Canceled:
		code, message = domain.Canceled, "The provider check was canceled."
	}
	return &domain.Error{Code: code, Message: message, Guidance: guidance, Cause: string(o.Failure)}
}

// Inspect validates a model listing and, for public gateway catalogs, performs
// their documented credential check separately. No inference, retries, fallback,
// quota interpretation, billing ingestion or account readiness mutation occurs.
// Callers retain ownership of key and must clear it after the call.
func Inspect(ctx context.Context, provider domain.Provider, key []byte) (Observation, error) {
	if err := provider.Validate(); err != nil {
		return Observation{}, err
	}
	if provider.Protocol == domain.NativeSubscription {
		return Observation{}, domain.Fail(domain.Unsupported, "Subscription providers require their native account interface.", "Use the isolated subscription lifecycle.")
	}
	if err := domain.ValidateAPIKey(key, provider.Authentication == domain.KeylessAuth); err != nil {
		return Observation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	transport := &http.Transport{
		// Environment proxies could redirect a server-owned credential. Explicit
		// protected network configuration must be integrated here before use.
		Proxy: nil, DialContext: directDial,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true, DisableCompression: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return inspect(ctx, client, provider, key), nil
}

func directDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(host, "localhost") {
		// localhost is always this server, even with a modified hosts/DNS entry.
		// Try both loopback families without resolving it through external DNS.
		conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		if err == nil {
			return conn, nil
		}
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort("::1", port))
	}
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
}

func inspect(ctx context.Context, client *http.Client, provider domain.Provider, key []byte) Observation {
	o := Observation{Authentication: AuthenticationUnknown, ObservedAt: time.Now().UTC().Truncate(time.Millisecond)}
	base, _ := url.Parse(provider.Endpoint)
	base.Path = strings.TrimSuffix(base.Path, "/")
	profile := endpointProfile(*base, provider)
	if provider.Authentication == domain.KeylessAuth {
		o.Authentication = KeylessEndpoint
	}
	if profile == openRouter || profile == vercelGateway {
		path := "key"
		if profile == vercelGateway {
			path = "credits"
		}
		raw, failure := get(ctx, client, provider, key, *base, path, nil)
		if failure.Failure != NoFailure {
			return failure.withTime(o.ObservedAt)
		}
		valid := validGatewayCredential(raw, profile)
		clear(raw)
		if !valid {
			o.Failure = InvalidResponse
			return o
		}
		o.Authentication = CredentialAccepted
	}
	models := []Model{}
	seen := map[string]bool{}
	after := ""
	remaining := 4 * maxBody
	for page := 0; page < maxPages; page++ {
		query := url.Values{}
		if profile == openRouter {
			query.Set("limit", "500")
			query.Set("offset", strconv.Itoa(page*500))
			query.Set("output_modalities", "all")
		}
		if provider.Protocol == domain.AnthropicMessages {
			query.Set("limit", "1000")
			if after != "" {
				query.Set("after_id", after)
			}
		}
		raw, failure := get(ctx, client, provider, key, *base, "models", query)
		if failure.Failure != NoFailure {
			return failure.withTime(o.ObservedAt)
		}
		remaining -= len(raw)
		o.HTTPStatus = http.StatusOK
		if remaining < 0 {
			clear(raw)
			o.Failure = ResponseTooLarge
			return o
		}
		items, next, err := parseModels(raw, provider.Protocol, key)
		moreRouter := false
		if err == nil && profile == openRouter {
			moreRouter, err = routerPage(raw, page*500, len(items))
		}
		clear(raw)
		if err != nil {
			o.Failure = InvalidResponse
			return o
		}
		if len(models)+len(items) > maxModels {
			o.Failure = ResponseTooLarge
			return o
		}
		for _, item := range items {
			if seen[item.ID] {
				o.Failure = InvalidResponse
				return o
			}
			seen[item.ID] = true
			models = append(models, item)
		}
		if moreRouter {
			continue
		}
		if next == "" {
			slices.SortFunc(models, func(a, b Model) int { return strings.Compare(a.ID, b.ID) })
			o.Models = models
			o.HTTPStatus = http.StatusOK
			if profile == authenticatedModels {
				o.Authentication = CredentialAccepted
			}
			return o
		}
		if next == after {
			o.Failure = InvalidResponse
			return o
		}
		after = next
	}
	o.Failure = ResponseTooLarge
	return o
}
func (o Observation) withTime(t time.Time) Observation { o.ObservedAt = t; return o }

// Only fixed, explicitly recognized authorities establish documented credential
// evidence. A familiar display name or an equivalent-looking suffix does not.
type profile uint8

const (
	custom profile = iota
	authenticatedModels
	openRouter
	vercelGateway
)

func endpointProfile(base url.URL, p domain.Provider) profile {
	if base.Scheme != "https" || (base.Port() != "" && base.Port() != "443") {
		return custom
	}
	host := strings.ToLower(base.Hostname())
	if p.Authentication == domain.BearerAuth && (p.Protocol == domain.OpenAIChat || p.Protocol == domain.OpenAIResponses) {
		switch {
		case host == "openrouter.ai" && base.Path == "/api/v1":
			return openRouter
		case host == "ai-gateway.vercel.sh" && base.Path == "/v1":
			return vercelGateway
		case host == "api.openai.com" && base.Path == "/v1":
			return authenticatedModels
		case host == "api.x.ai" && base.Path == "/v1":
			return authenticatedModels
		case host == "api.deepseek.com" && (base.Path == "" || base.Path == "/v1"):
			return authenticatedModels
		}
	}
	if host == "api.anthropic.com" && base.Path == "/v1" && p.Protocol == domain.AnthropicMessages && p.Authentication == domain.APIKeyAuth {
		return authenticatedModels
	}
	return custom
}

func get(ctx context.Context, client *http.Client, p domain.Provider, key []byte, base url.URL, path string, query url.Values) ([]byte, Observation) {
	failure := Observation{Authentication: AuthenticationUnknown}
	base.Path += "/" + path
	base.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		failure.Failure = InvalidResponse
		return nil, failure
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("HTTP-Referer", "https://deli.dev")
	req.Header.Set("User-Agent", "delidev/0.1.0")
	if p.Protocol == domain.AnthropicMessages {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	switch p.Authentication {
	case domain.BearerAuth:
		req.Header.Set("Authorization", "Bearer "+string(key))
	case domain.APIKeyAuth:
		req.Header.Set("x-api-key", string(key))
	}
	defer req.Header.Del("Authorization")
	defer req.Header.Del("x-api-key")
	response, err := client.Do(req)
	if err != nil {
		failure.Failure = networkFailure(err)
		return nil, failure
	}
	defer response.Body.Close()
	failure.HTTPStatus = response.StatusCode
	if response.StatusCode != http.StatusOK {
		switch {
		case response.StatusCode == 401:
			failure.Failure = AuthenticationRejected
		case response.StatusCode == 403:
			failure.Failure = AccessDenied
		case response.StatusCode == 429:
			failure.Failure = RateLimited
		case response.StatusCode == 404 || response.StatusCode == 405 || response.StatusCode == 501:
			failure.Failure = Unsupported
		case response.StatusCode >= 300 && response.StatusCode < 400:
			failure.Failure = RedirectRefused
		default:
			failure.Failure = ProviderUnavailable
		}
		failure.RetryAfterSeconds = retryAfter(response.Header.Get("Retry-After"), time.Now())
		// Error bodies/Location/WWW-Authenticate/Set-Cookie/request IDs may contain
		// secrets and are never parsed, retained, returned or logged.
		return nil, failure
	}
	if response.ContentLength > maxBody {
		failure.Failure = ResponseTooLarge
		return nil, failure
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		clear(raw)
		failure.Failure = networkFailure(err)
		return nil, failure
	}
	if len(raw) > maxBody {
		clear(raw)
		failure.Failure = ResponseTooLarge
		return nil, failure
	}
	return raw, Observation{}
}
func networkFailure(err error) Failure {
	if errors.Is(err, context.Canceled) {
		return Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return TimedOut
	}
	var invalid *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	if errors.As(err, &invalid) || errors.As(err, &authority) || errors.As(err, &hostname) {
		return TLSFailure
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return TimedOut
	}
	return NetworkFailure
}
func retryAfter(value string, now time.Time) *uint32 {
	if len(value) > 128 {
		return nil
	}
	n, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		t, err := http.ParseTime(value)
		if err != nil {
			return nil
		}
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		n = uint64((d + time.Second - 1) / time.Second)
	}
	if n > 86400 {
		return nil
	}
	result := uint32(n)
	return &result
}

func validGatewayCredential(raw []byte, profile profile) bool {
	obj, err := object(raw)
	if err != nil {
		return false
	}
	if profile == openRouter {
		data, err := object(obj["data"])
		if err != nil {
			return false
		}
		var free bool
		return len(data["is_free_tier"]) > 0 && json.Unmarshal(data["is_free_tier"], &free) == nil && !bytes.Equal(data["is_free_tier"], []byte("null"))
	}
	for _, name := range []string{"balance", "total_used"} {
		var number string
		if json.Unmarshal(obj[name], &number) != nil || len(number) == 0 || len(number) > 128 {
			return false
		}
		// Keep monetary values out of this authentication-only result. Reject
		// arbitrary text without interpreting credit balance as quota or usage.
		for i, c := range number {
			if (c < '0' || c > '9') && c != '.' && !(i == 0 && c == '-') {
				return false
			}
		}
		var value json.Number
		if json.Unmarshal([]byte(number), &value) != nil {
			return false
		}
	}
	return true
}

func parseModels(raw []byte, protocol domain.APIProtocol, key []byte) ([]Model, string, error) {
	obj, err := object(raw)
	if err != nil {
		return nil, "", err
	}
	var items []json.RawMessage
	if len(obj["data"]) == 0 || json.Unmarshal(obj["data"], &items) != nil || items == nil {
		return nil, "", errors.New("invalid data")
	}
	if len(items) > maxModels {
		return nil, "", errors.New("too many models")
	}
	models := make([]Model, 0, len(items))
	for _, rawItem := range items {
		item, err := object(rawItem)
		if err != nil {
			return nil, "", err
		}
		var model Model
		if json.Unmarshal(item["id"], &model.ID) != nil || !modelID(model.ID) || containsKey(model.ID, key) {
			return nil, "", errors.New("invalid model identity")
		}
		model.Name = model.ID
		nameField := "name"
		if protocol == domain.AnthropicMessages {
			nameField = "display_name"
		}
		if len(item[nameField]) != 0 && string(item[nameField]) != "null" {
			if json.Unmarshal(item[nameField], &model.Name) != nil || !displayName(model.Name) || containsKey(model.Name, key) {
				return nil, "", errors.New("invalid model name")
			}
		}
		contextField := "context_length"
		if protocol == domain.AnthropicMessages {
			contextField = "max_input_tokens"
		}
		if len(item[contextField]) > 0 && string(item[contextField]) != "null" {
			var limit uint64
			if json.Unmarshal(item[contextField], &limit) != nil || limit == 0 || containsKey(strconv.FormatUint(limit, 10), key) {
				return nil, "", errors.New("invalid context limit")
			}
			model.ContextLimit = &limit
		}
		if err := advisoryMetadata(item, key, &model); err != nil {
			return nil, "", err
		}
		models = append(models, model)
	}
	var more bool
	if len(obj["has_more"]) > 0 && (json.Unmarshal(obj["has_more"], &more) != nil || string(obj["has_more"]) == "null") {
		return nil, "", errors.New("invalid pagination")
	}
	if !more {
		return models, "", nil
	}
	var next string
	if protocol != domain.AnthropicMessages || json.Unmarshal(obj["last_id"], &next) != nil || len(models) == 0 || next != models[len(models)-1].ID {
		return nil, "", errors.New("invalid pagination")
	}
	return models, next, nil
}
func modelID(s string) bool {
	if len(s) == 0 || len(s) > 256 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("-_.:/@+", r) {
			return false
		}
	}
	return s != "." && s != ".."
}
func displayName(s string) bool {
	if len(s) == 0 || len(s) > 256 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func containsKey(value string, key []byte) bool {
	if len(key) == 0 {
		return false
	}
	for _, candidate := range []string{string(key), base64.StdEncoding.EncodeToString(key), base64.RawStdEncoding.EncodeToString(key)} {
		if value == candidate || (len(candidate) >= 8 && strings.Contains(value, candidate)) {
			return true
		}
	}
	return false
}

// Decode only exact known keys, allowing future provider fields while rejecting
// duplicate keys, invalid UTF-8 and extra documents before interpreting any data.
func object(raw []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := uniqueValue(d, 0); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil || result == nil {
		return nil, errors.New("invalid object")
	}
	return result, nil
}
func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("excessive nesting")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || seen[key] {
				return errors.New("duplicate key")
			}
			seen[key] = true
			if err := uniqueValue(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueValue(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid delimiter")
	}
	_, err = d.Token()
	return err
}
