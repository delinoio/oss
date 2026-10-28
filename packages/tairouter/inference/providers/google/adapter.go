// SPDX-License-Identifier: Apache-2.0
// Package google implements Gemini, Vertex and Cloud Code Assist HTTP wire adapters.
// Pinned behavior adaptations retain the notice in UPSTREAM-LICENSE.
package google

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

const (
	WireAPI         inference.Wire = "google-gemini"
	WireAntigravity inference.Wire = "google-antigravity"
	WireGeminiCLI   inference.Wire = "google-gemini-cli"
	// ProjectHeader is credential metadata. Build consumes it without forwarding it.
	ProjectHeader     = "X-Tairouter-Google-Project"
	defaultBodyLimit  = 16 << 20
	defaultFrameLimit = 1 << 20
	maxSignatureBytes = 64 << 10
)

type Options struct {
	// SchemaObserver receives only bounded category counts, never schema content.
	SchemaObserver func(context.Context, SchemaLossReport)
	SchemaPolicy   SchemaPolicy
	// ReasoningEfforts overrides the pinned direct model ladder. Empty disables it.
	ReasoningEfforts          []string
	DisableDirectModelRenames bool
	// NativeMedia exposes bounded inline media in extra_content.google.media.
	// Storage and client URL projection remain the central artifact owner's job.
	NativeMedia   bool
	MaxBodyBytes  int
	MaxFrameBytes int
	// ShowThinkingSummaries exposes provider-authored summaries as reasoning_content.
	// Raw thought parts remain opaque metadata when this option is false.
	ShowThinkingSummaries bool
	// HideThinkingSummaries overrides the pinned CCA default of showing summaries.
	HideThinkingSummaries bool
}

// Adapter is immutable and can be shared between concurrent requests. It has no
// credential, conversation or signature cache, and never sends an HTTP request.
type Adapter struct {
	cca     bool
	vertex  bool
	options Options
}

var _ inference.Adapter = (*Adapter)(nil)

func NewAPI(options Options) *Adapter         { return newAdapter(false, options) }
func NewAntigravity(options Options) *Adapter { return newAdapter(true, options) }

// NewGeminiCLI selects the pinned Cloud Code Assist HTTP implementation. It does
// not execute Gemini CLI or claim a separate CLI fingerprint absent from the pin.
func NewGeminiCLI(options Options) *Adapter { return newAdapter(true, options) }

func newAdapter(cca bool, options Options) *Adapter {
	if cca {
		options.ShowThinkingSummaries = true
	}
	if options.HideThinkingSummaries {
		options.ShowThinkingSummaries = false
	}
	if options.ReasoningEfforts != nil {
		options.ReasoningEfforts = append([]string{}, options.ReasoningEfforts...)
	}
	if options.MaxBodyBytes <= 0 || options.MaxBodyBytes > defaultBodyLimit {
		options.MaxBodyBytes = defaultBodyLimit
	}
	if options.MaxFrameBytes <= 0 || options.MaxFrameBytes > defaultFrameLimit {
		options.MaxFrameBytes = defaultFrameLimit
	}
	return &Adapter{cca: cca, options: options}
}

type object = map[string]any

func obj(v any) object                      { m, _ := v.(map[string]any); return m }
func array(v any) []any                     { a, _ := v.([]any); return a }
func text(v any) string                     { s, _ := v.(string); return s }
func failure(status int, code string) error { return &inference.Error{Status: status, Code: code} }
func invalid() error                        { return failure(400, "google_invalid_request") }
func unsupported() error                    { return failure(400, "google_unsupported_field") }
func protocol() error                       { return failure(502, "google_invalid_response") }

func decodeJSON(data []byte) (object, error) {
	var root object
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if d.Decode(&root) != nil || root == nil {
		return nil, protocol()
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return nil, protocol()
	}
	return root, nil
}

var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]*$`)
var cliModelPattern = regexp.MustCompile(`^gemini-4-argon(?:-(?:low|medium|high))?$`)
var signaturePattern = regexp.MustCompile(`^[A-Za-z0-9+/_=-]+$`)
var foreignSignaturePattern = regexp.MustCompile(`(?i)^(fc|ctc|tsc|call|msg|rs|resp|reasoning|item|ws|toolu|tool|func|function)[-_]`)

func realSignature(s string) bool {
	return len(s) >= 16 && len(s) <= maxSignatureBytes && s != "skip_thought_signature_validator" && !foreignSignaturePattern.MatchString(s) && signaturePattern.MatchString(s)
}
func signature(part object) string {
	s := text(part["thoughtSignature"])
	if s == "" {
		s = text(part["thought_signature"])
	}
	if s == "" {
		s = text(obj(obj(part["extra_content"])["google"])["thought_signature"])
	}
	return s
}
func opaqueSignature(s string) object { return object{"google": object{"thought_signature": s}} }

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", failure(500, "google_request_id_failed")
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

func (a *Adapter) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	var out inference.AdapterRequest
	var observation *SchemaLossReport
	defer func() {
		if observation != nil && a.options.SchemaObserver != nil {
			a.options.SchemaObserver(ctx, observation.clone())
		}
	}()
	state, _ := ctx.Value(requestKey{}).(*requestState)
	if state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.built {
			return out, failure(500, "google_request_context_reused")
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if in.Operation != inference.Generate {
		return out, inference.ErrUnsupported
	}
	if len(in.Body) > a.options.MaxBodyBytes {
		return out, failure(413, "google_request_too_large")
	}
	root, err := decodeJSON(in.Body)
	if err != nil {
		return out, invalid()
	}
	model := in.Target.Model
	if !modelPattern.MatchString(model) {
		return out, invalid()
	}
	secret := strings.TrimSpace(in.Credential.Secret)
	if secret == "" || strings.ContainsAny(secret, "\r\n") {
		return out, inference.ErrNoCredential
	}
	if (a.cca && in.Target.Auth != inference.OAuth) || (!a.cca && !a.vertex && in.Target.Auth != inference.APIKey) {
		return out, inference.ErrUnsupported
	}
	// Resolve the toggle per request; constructors remain safe to share. Nil
	// preserves the selected transport's defaults, including CCA summaries.
	compiler := *a
	if in.Target.Policy != nil {
		if show := in.Target.Policy.ShowThinkingSummary(); show != nil {
			compiler.options.ShowThinkingSummaries = *show
		}
	}
	body, anchor, err := compiler.request(root, in.Wire, model)
	if err != nil {
		return out, err
	}
	model = compiler.normalizeGeneration(body, root, model)
	aliases := compileToolNames(body)
	if state != nil {
		state.restoreCalls(body, aliases)
	}
	report := SchemaLossReport{Version: 1, EndpointClass: "ai-studio", Categories: map[string]int{}}
	if a.cca {
		report.EndpointClass = "cloud-code-assist"
	}
	if a.vertex {
		report.EndpointClass = "vertex"
	}
	for _, tool := range array(body["tools"]) {
		for _, raw := range array(obj(tool)["functionDeclarations"]) {
			d := obj(raw)
			schema, loss := SanitizeToolSchema(d["parameters"], a.cca)
			d["parameters"] = schema
			report.merge(loss)
		}
	}
	if state != nil {
		state.report = report.clone()
	}
	observation = &report
	if a.options.SchemaPolicy != "" && a.options.SchemaPolicy != SchemaCompatible && a.options.SchemaPolicy != SchemaRejectLossy {
		return out, failure(500, "google_invalid_schema_policy")
	}
	if a.options.SchemaPolicy == SchemaRejectLossy && (report.Lossy || report.UncertainComparisons > 0) {
		return out, failure(400, "google_tool_schema_loss_refused")
	}
	needsContext := false
	for alias, name := range aliases {
		if alias != name {
			needsContext = true
			break
		}
	}
	if needsContext && state == nil {
		return out, failure(500, "google_request_context_required")
	}
	var vertexProject, vertexRegion, vertexBase string
	if a.vertex {
		vertexProject, vertexRegion, vertexBase, err = vertexSelection(in)
		if err != nil {
			return out, err
		}
	}
	base := in.Target.BaseURL
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
		if a.vertex {
			base = vertexBase
		}
		if a.cca {
			base = "https://daily-cloudcode-pa.googleapis.com"
		}
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return out, inference.ErrDestination
	}
	method := "generateContent"
	if root["stream"] == true {
		method = "streamGenerateContent"
		u.RawQuery = "alt=sse"
	}
	headers := make(http.Header)
	// Forward configured non-auth headers only. Credentials cannot overwrite the
	// selected auth scheme or leak an unrelated key into another provider family.
	for _, source := range []http.Header{in.Target.Headers, in.Credential.Headers} {
		for name, values := range source {
			switch strings.ToLower(name) {
			case "authorization", "proxy-authorization", "x-goog-api-key", "x-api-key", "cookie", "host", "content-length", "connection", "transfer-encoding", "x-tairouter-google-project", "x-tairouter-google-location":
				// The executor overlays adapter headers after generic headers.
				// An empty entry removes the earlier value without sending a header.
				headers[http.CanonicalHeaderKey(name)] = nil
				continue
			}
			for _, value := range values {
				if strings.ContainsAny(name+value, "\r\n") {
					return out, invalid()
				}
				headers.Add(name, value)
			}
		}
	}
	headers.Set("Content-Type", "application/json")
	if root["stream"] == true {
		headers.Set("Accept", "text/event-stream")
	} else {
		headers.Set("Accept", "application/json")
	}
	if a.cca {
		project := strings.TrimSpace(in.Credential.Headers.Get(ProjectHeader))
		if project == "" || strings.ContainsAny(project, "\r\n") {
			return out, failure(400, "google_project_required")
		}
		id, e := randomID()
		if e != nil {
			return out, e
		}
		if header := headerAnchor(in); header != "" {
			anchor = header
		}
		if anchor == "" {
			anchor = id
		}
		// Domain-separate tenants and selected accounts. No process-local replay
		// cache may use the resulting upstream session as an authorization key.
		sum := sha256.Sum256([]byte(in.Principal.OrganizationID + "\x00" + in.Principal.UserID + "\x00" + in.Credential.AccountID + "\x00" + anchor))
		body["sessionId"] = fmt.Sprintf("-%d", binary.BigEndian.Uint64(sum[:8])&0x7fffffffffffffff)
		body = object{"model": model, "project": project, "userAgent": "antigravity", "requestType": "agent", "requestId": "agent-" + id, "request": body}
		u.Path = strings.TrimRight(u.Path, "/") + "/v1internal:" + method
		headers.Set("Authorization", "Bearer "+secret)
		ua := "antigravity/ide/2.5.5 (os_type=windows; arch=amd64; aidev_client; auth_method=oauth)"
		if cliModelPattern.MatchString(model) {
			ua = "antigravity/cli/1.3.4 (os_type=darwin; arch=arm64; aidev_client; auth_method=oauth)"
		}
		headers.Set("User-Agent", ua)
	} else if a.vertex {
		if err = vertexPath(u, vertexProject, vertexRegion, model, method); err != nil {
			return out, err
		}
		if in.Target.Auth == inference.OAuth {
			headers.Set("Authorization", "Bearer "+secret)
		} else {
			headers.Set("X-Goog-Api-Key", secret)
		}
	} else {
		u.Path = strings.TrimRight(u.Path, "/") + "/v1beta/models/" + model + ":" + method
		headers.Set("X-Goog-Api-Key", secret)
	}
	if !a.vertex {
		u.RawPath = ""
	}
	data, err := json.Marshal(body)
	if err != nil {
		return out, invalid()
	}
	if len(data) > a.options.MaxBodyBytes {
		return out, failure(413, "google_request_too_large")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	responseWire := inference.Chat
	if a.cca && in.Wire == inference.Anthropic && state != nil {
		responseWire = inference.Anthropic
	}
	if state != nil {
		state.binding = replayBinding(in)
		state.built = true
		state.aliases = aliases
		state.report = report
		state.model = model
		state.wire = responseWire
	}
	return inference.AdapterRequest{URL: u.String(), Headers: headers, Body: data, ResponseWire: responseWire}, nil
}
