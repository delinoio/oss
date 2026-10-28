// SPDX-License-Identifier: Apache-2.0
package main

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// NativeCompatibilityMode selects a pinned protocol surface, not permission to
// install trust, mutate an application, or start an account UI trial.
type NativeCompatibilityMode string

const (
	NativeCompatibilityClaudeFirstParty    NativeCompatibilityMode = "claude-firstparty"
	NativeCompatibilityClaudePicker        NativeCompatibilityMode = "claude-picker"
	NativeCompatibilityCodexObserve        NativeCompatibilityMode = "codex-observe"
	NativeCompatibilityCodexAccountUITrial NativeCompatibilityMode = "codex-account-ui-trial"
	NativeCompatibilityClaudeBuild                                 = "1.18286.0"
	NativeCompatibilityCodexBuild                                  = "26.924.2738.0"
)

type NativeCompatibilityAction string

const (
	NativeCompatibilityGateway  NativeCompatibilityAction = "gateway"
	NativeCompatibilityUpstream NativeCompatibilityAction = "verified-upstream"
	NativeCompatibilityLocal    NativeCompatibilityAction = "local"
)

type NativeCompatibilityResponseKind string

const (
	NativeCompatibilityResponseUnchanged   NativeCompatibilityResponseKind = "unchanged"
	NativeCompatibilityResponsePicker      NativeCompatibilityResponseKind = "claude-picker"
	NativeCompatibilityResponseUsage       NativeCompatibilityResponseKind = "codex-usage"
	NativeCompatibilityResponseUsageStream NativeCompatibilityResponseKind = "codex-usage-stream"
)

const (
	NativeCompatibilityResponseCLISelector  NativeCompatibilityResponseKind = "claude-cli-selector"
	NativeCompatibilityResponseCLIBootstrap NativeCompatibilityResponseKind = "claude-cli-bootstrap"
)

type NativeCompatibilityDecision struct {
	Action       NativeCompatibilityAction
	GatewayPath  string
	ResponseKind NativeCompatibilityResponseKind
}
type NativeCompatibilityPolicy struct {
	mode  NativeCompatibilityMode
	trial *nativeCompatibilityTrial
}

// NewNativeCompatibilityPolicy checks the caller's freshly observed build.
// It does not attest an installed package. The controller must prove package
// identity and retain that proof across consent and activation.
// Reference: opencodex 88aaa754566d119b6b8fc898f8fb47602387d82e,
// claude/desktop-3p-paths.ts and codex/desktop-compatibility/installed-build.ts.
// Claude's inspected build is a conservative gate until more builds are assessed.
func NewNativeCompatibilityPolicy(mode NativeCompatibilityMode, platform, build string) (NativeCompatibilityPolicy, error) {
	switch mode {
	case NativeCompatibilityClaudeFirstParty, NativeCompatibilityClaudePicker:
		if (platform != "darwin" && platform != "windows") || build != NativeCompatibilityClaudeBuild {
			return NativeCompatibilityPolicy{}, errors.New("unsupported Claude Desktop platform or build")
		}
	case NativeCompatibilityCodexObserve, NativeCompatibilityCodexAccountUITrial:
		if platform != "windows" || build != NativeCompatibilityCodexBuild {
			return NativeCompatibilityPolicy{}, errors.New("unsupported experimental Codex Desktop platform or build")
		}
	default:
		return NativeCompatibilityPolicy{}, errors.New("unknown native compatibility mode")
	}
	return NativeCompatibilityPolicy{mode: mode}, nil
}
func (p NativeCompatibilityPolicy) Hosts() []string {
	switch p.mode {
	case NativeCompatibilityClaudeFirstParty:
		return []string{"api.anthropic.com"}
	case NativeCompatibilityClaudePicker:
		return []string{"api.anthropic.com", "claude.ai"}
	case NativeCompatibilityCodexObserve, NativeCompatibilityCodexAccountUITrial:
		return []string{"chatgpt.com"}
	default:
		return nil
	}
}
func (p NativeCompatibilityPolicy) Routes() []string {
	switch p.mode {
	case NativeCompatibilityClaudeFirstParty:
		return []string{"POST api.anthropic.com/v1/messages", "POST api.anthropic.com/v1/messages/count_tokens", "GET api.anthropic.com/api/[organizations/{id}/]model_selector/cc[/]", "GET api.anthropic.com/api/claude_cli/bootstrap[/]"}
	case NativeCompatibilityClaudePicker:
		return append((NativeCompatibilityPolicy{mode: NativeCompatibilityClaudeFirstParty}).Routes(), "GET claude.ai/{api,edge-api}/bootstrap[/{alphanumeric-or-hyphen}/app_start][/]")
	case NativeCompatibilityCodexObserve, NativeCompatibilityCodexAccountUITrial:
		return []string{"HTTPS chatgpt.com:443 native HTTP and WebSocket passthrough (no other host)", "rewrite only GET chatgpt.com/backend-api/wham/usage", "rewrite only GET chatgpt.com/backend-api/wham/usage/stream"}
	default:
		return nil
	}
}

var nativeCLISelectorPath = regexp.MustCompile(`^/api/(?:organizations/[A-Za-z0-9-]+/)?model_selector/cc/?$`)
var nativeCLIUserAgent = regexp.MustCompile(`^claude-cli/[^\s()]+ \(external, ([A-Za-z0-9._-]+)(?:, [^,()]+)*\)$`)
var nativePickerPath = regexp.MustCompile(`^/(?:edge-api|api)/bootstrap(?:/[A-Za-z0-9-]+/app_start)?/?$`)

// Decide restricts destinations to the pinned protocol. Codex PAC selects the
// entire fixed chatgpt.com origin, so preserve its native HTTP and upgrades while
// limiting response rewrites to usage routes. No arbitrary-host relay is allowed.
// Upstream transport verifies TLS and never follows redirects. Gateway callers
// must use GatewayHeaders and GatewayPath,
// discarding native URL queries rather than forwarding possible credentials.
func (p NativeCompatibilityPolicy) Decide(method string, target *url.URL) (NativeCompatibilityDecision, error) {
	denied := errors.New("native compatibility destination or route refused")
	if target == nil || target.Scheme != "https" || target.User != nil || target.Opaque != "" || target.Fragment != "" || (target.Port() != "" && target.Port() != "443") {
		return NativeCompatibilityDecision{}, denied
	}
	host := target.Hostname()
	allowed := false
	for _, candidate := range p.Hosts() {
		if target.Host == candidate || target.Host == candidate+":443" {
			allowed = true
		}
	}
	if !allowed || !strings.HasPrefix(target.Path, "/") || strings.HasPrefix(target.Path, "//") {
		return NativeCompatibilityDecision{}, denied
	}
	if host != "chatgpt.com" && (target.RawPath != "" || strings.ContainsAny(target.Path, "%\\")) {
		return NativeCompatibilityDecision{}, denied
	}
	if host == "api.anthropic.com" && method == http.MethodPost && (target.Path == "/v1/messages" || target.Path == "/v1/messages/count_tokens") {
		return NativeCompatibilityDecision{Action: NativeCompatibilityGateway, GatewayPath: target.Path, ResponseKind: NativeCompatibilityResponseUnchanged}, nil
	}
	if host == "api.anthropic.com" && method == http.MethodGet {
		if nativeCLISelectorPath.MatchString(target.Path) {
			return NativeCompatibilityDecision{Action: NativeCompatibilityUpstream, ResponseKind: NativeCompatibilityResponseUnchanged}, nil
		}
		if target.Path == "/api/claude_cli/bootstrap" || target.Path == "/api/claude_cli/bootstrap/" {
			return NativeCompatibilityDecision{Action: NativeCompatibilityUpstream, ResponseKind: NativeCompatibilityResponseCLIBootstrap}, nil
		}
	}
	if host == "claude.ai" && method == http.MethodGet && nativePickerPath.MatchString(target.Path) {
		return NativeCompatibilityDecision{Action: NativeCompatibilityUpstream, ResponseKind: NativeCompatibilityResponsePicker}, nil
	}
	if host == "chatgpt.com" && method != http.MethodConnect {
		kind := NativeCompatibilityResponseUnchanged
		if method == http.MethodGet && target.Path == "/backend-api/wham/usage" && target.RawPath == "" {
			kind = NativeCompatibilityResponseUsage
		} else if method == http.MethodGet && target.Path == "/backend-api/wham/usage/stream" && target.RawPath == "" {
			kind = NativeCompatibilityResponseUsageStream
		}
		return NativeCompatibilityDecision{Action: NativeCompatibilityUpstream, ResponseKind: kind}, nil
	}
	return NativeCompatibilityDecision{}, denied
}

// NativeCompatibilityGatewayHeaders is a positive allowlist: native cookies,
// account IDs, API keys, bearer tokens and arbitrary extension headers never
// cross the gateway boundary. The runtime supplies its own profile credential.
func NativeCompatibilityGatewayHeaders(source http.Header) http.Header {
	out := make(http.Header)
	for _, name := range []string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta"} {
		if value := source.Get(name); value != "" {
			out.Set(name, value)
		}
	}
	return out
}

// DecideRequest enables the CLI-only selector rewrite only for a CLI entrypoint.
// Desktop processes must not seed the shared CLI catalog cache with added rows.
func (p NativeCompatibilityPolicy) DecideRequest(req *http.Request) (NativeCompatibilityDecision, error) {
	if req == nil {
		return NativeCompatibilityDecision{}, errors.New("missing native request")
	}
	d, err := p.Decide(req.Method, req.URL)
	if err != nil {
		return d, err
	}
	if req.URL.Hostname() == "api.anthropic.com" && req.Method == http.MethodGet && nativeCLISelectorPath.MatchString(req.URL.Path) {
		m := nativeCLIUserAgent.FindStringSubmatch(req.UserAgent())
		if len(m) > 1 && m[1] != "claude-desktop" && m[1] != "claude-desktop-3p" && m[1] != "local-agent" {
			d.ResponseKind = NativeCompatibilityResponseCLISelector
		}
	}
	return d, nil
}

func (p NativeCompatibilityPolicy) isCodex() bool {
	return p.mode == NativeCompatibilityCodexObserve || p.mode == NativeCompatibilityCodexAccountUITrial
}
