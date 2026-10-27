// SPDX-License-Identifier: Apache-2.0
package providers_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/policy"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/google"
	"github.com/delinoio/oss/packages/aiprouter/providers"
)

// Expected requests are a frozen transport oracle for upstream
// 88aaa754566d119b6b8fc898f8fb47602387d82e, not computed from Catalog at runtime.
// Source: registry presets and adapters/{openai-chat,openai-responses,anthropic}.ts.
// Adapted reference data retains its MIT notice in snapshot/NOTICE and LICENSE-MIT.
// These tests exercise real request serialization and gated local HTTP/TLS sends.
// They do not prove live credentials, model entitlement, or remote availability.
type presetRequest struct{ id, model, wire, endpoint, authHeader string }

var genericRequests = []presetRequest{
	{"openai", "fixture-model", "openai-responses", "https://chatgpt.com/backend-api/codex/responses", "Authorization"},
	{"xai", "grok-4.5", "openai-chat", "https://api.x.ai/v1/chat/completions", "Authorization"},
	{"orcarouter-oauth", "openai/gpt-5.5", "openai-chat", "https://api.orcarouter.ai/v1/chat/completions", "Authorization"},
	{"anthropic", "claude-sonnet-5", "anthropic", "https://api.anthropic.com/v1/messages", "Authorization"},
	{"anthropic2", "claude-sonnet-5", "anthropic", "https://api.anthropic.com/v1/messages", "Authorization"},
	{"anthropic-apikey", "claude-sonnet-5", "anthropic", "https://api.anthropic.com/v1/messages", "X-Api-Key"},
	{"kimi", "kimi-for-coding", "openai-chat", "https://api.kimi.com/coding/v1/chat/completions", "Authorization"},
	{"kimi-responses", "kimi-for-coding", "openai-responses", "https://api.kimi.com/coding/v1/responses", "Authorization"},
	{"nous", "tencent/hy3:free", "openai-chat", "https://inference-api.nousresearch.com/v1/chat/completions", "Authorization"},
	{"openai-apikey", "gpt-5.5", "openai-responses", "https://api.openai.com/v1/responses", "Authorization"},
	{"meta-model", "muse-spark-1.3", "openai-responses", "https://api.meta.ai/v1/responses", "Authorization"},
	{"meta-muse", "muse-spark-1.3", "openai-responses", "https://api.meta.ai/v1/responses", "Authorization"},
	{"umans", "umans-coder", "anthropic", "https://api.code.umans.ai/v1/messages", "X-Api-Key"},
	{"opencode-go", "kimi-k2.7-code", "openai-chat", "https://opencode.ai/zen/go/v1/chat/completions", "Authorization"},
	{"neuralwatt", "glm-5.3", "openai-chat", "https://api.neuralwatt.com/v1/chat/completions", "Authorization"},
	{"openrouter", "fixture-model", "openai-chat", "https://openrouter.ai/api/v1/chat/completions", "Authorization"},
	{"cline-pass", "cline-pass/kimi-k3", "openai-chat", "https://api.cline.bot/api/v1/chat/completions", "Authorization"},
	{"cline", "anthropic/claude-sonnet-4-6", "openai-chat", "https://api.cline.bot/api/v1/chat/completions", "Authorization"},
	{"orcarouter", "openai/gpt-5.5", "openai-chat", "https://api.orcarouter.ai/v1/chat/completions", "Authorization"},
	{"packycode", "gpt-5.5", "openai-chat", "https://cf.api.fan/v1/chat/completions", "Authorization"},
	{"bizrouter", "openai/gpt-5.6-sol", "openai-chat", "https://api.bizrouter.ai/v1/chat/completions", "Authorization"},
	{"groq", "fixture-model", "openai-chat", "https://api.groq.com/openai/v1/chat/completions", "Authorization"},
	{"ollama", "fixture-model", "openai-chat", "http://localhost:11434/v1/chat/completions", ""},
	{"vllm", "fixture-model", "openai-chat", "http://localhost:8000/v1/chat/completions", ""},
	{"lm-studio", "fixture-model", "openai-chat", "http://localhost:1234/v1/chat/completions", ""},
	{"deepseek", "deepseek-flash", "openai-chat", "https://api.deepseek.com/chat/completions", "Authorization"},
	{"cerebras", "gpt-oss-120b", "openai-chat", "https://api.cerebras.ai/v1/chat/completions", "Authorization"},
	{"chutes", "fixture-model", "openai-chat", "https://llm.chutes.ai/v1/chat/completions", "Authorization"},
	{"deepinfra", "fixture-model", "openai-chat", "https://api.deepinfra.com/v1/openai/chat/completions", "Authorization"},
	{"hyperbolic", "fixture-model", "openai-chat", "https://api.hyperbolic.xyz/v1/chat/completions", "Authorization"},
	{"nscale", "meta-llama/Llama-3.1-8B-Instruct", "openai-chat", "https://inference.api.nscale.com/v1/chat/completions", "Authorization"},
	{"vultr", "kimi-k2-instruct", "openai-chat", "https://api.vultrinference.com/v1/chat/completions", "Authorization"},
	{"baseten", "fixture-model", "openai-chat", "https://inference.baseten.co/v1/chat/completions", "Authorization"},
	{"commandcode", "deepseek/deepseek-v4-flash", "openai-chat", "https://api.commandcode.ai/provider/v1/chat/completions", "Authorization"},
	{"sambanova", "fixture-model", "openai-chat", "https://api.sambanova.ai/v1/chat/completions", "Authorization"},
	{"nebius", "fixture-model", "openai-chat", "https://api.tokenfactory.nebius.com/v1/chat/completions", "Authorization"},
	{"crusoe", "fixture-model", "openai-chat", "https://api.inference.crusoecloud.com/v1/chat/completions", "Authorization"},
	{"digitalocean", "fixture-model", "openai-chat", "https://inference.do-ai.run/v1/chat/completions", "Authorization"},
	{"scaleway", "fixture-model", "openai-chat", "https://api.scaleway.ai/v1/chat/completions", "Authorization"},
	{"featherless", "fixture-model", "openai-chat", "https://api.featherless.ai/v1/chat/completions", "Authorization"},
	{"novita", "fixture-model", "openai-chat", "https://api.novita.ai/openai/v1/chat/completions", "Authorization"},
	{"together", "fixture-model", "openai-chat", "https://api.together.xyz/v1/chat/completions", "Authorization"},
	{"fireworks", "fixture-model", "openai-chat", "https://api.fireworks.ai/inference/v1/chat/completions", "Authorization"},
	{"firepass", "fixture-model", "openai-chat", "https://api.fireworks.ai/inference/v1/chat/completions", "Authorization"},
	{"moonshot", "kimi-k2.7-code", "openai-chat", "https://api.moonshot.ai/v1/chat/completions", "Authorization"},
	{"huggingface", "fixture-model", "openai-chat", "https://router.huggingface.co/v1/chat/completions", "Authorization"},
	{"nvidia", "fixture-model", "openai-chat", "https://integrate.api.nvidia.com/v1/chat/completions", "Authorization"},
	{"venice", "fixture-model", "openai-chat", "https://api.venice.ai/api/v1/chat/completions", "Authorization"},
	{"zai", "glm-5.3", "openai-responses", "https://api.z.ai/api/v1/responses", "Authorization"},
	{"zhipu-bigmodel", "glm-4.6", "openai-chat", "https://open.bigmodel.cn/api/paas/v4/chat/completions", "Authorization"},
	{"zhipu-bigmodel-coding", "glm-5.3", "openai-chat", "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions", "Authorization"},
	{"zhipu-bigmodel-responses", "glm-5.3", "openai-responses", "https://open.bigmodel.cn/api/v1/responses", "Authorization"},
	{"nanogpt", "fixture-model", "openai-chat", "https://nano-gpt.com/api/v1/chat/completions", "Authorization"},
	{"synthetic", "fixture-model", "openai-chat", "https://api.synthetic.new/openai/v1/chat/completions", "Authorization"},
	{"siliconflow", "fixture-model", "openai-chat", "https://api.siliconflow.cn/v1/chat/completions", "Authorization"},
	{"qwen-cloud", "fixture-model", "openai-chat", "https://token-plan.maas.qwencloudapi.com/compatible-mode/v1/chat/completions", "Authorization"},
	{"tencent-coding-plan", "tc-code-latest", "openai-chat", "https://api.lkeap.cloud.tencent.com/coding/v3/chat/completions", "Authorization"},
	{"volcengine", "doubao-seed-2-1-pro-260628", "openai-chat", "https://ark.cn-beijing.volces.com/api/v3/chat/completions", "Authorization"},
	{"volcengine-coding-plan", "ark-code-latest", "openai-responses", "https://ark.cn-beijing.volces.com/api/coding/v3/responses", "Authorization"},
	{"volcengine-agent-plan", "deepseek-v4-flash", "openai-responses", "https://ark.cn-beijing.volces.com/api/plan/v3/responses", "Authorization"},
	{"qianfan", "fixture-model", "openai-chat", "https://qianfan.baidubce.com/v2/chat/completions", "Authorization"},
	{"alibaba", "fixture-model", "openai-chat", "https://coding-intl.dashscope.aliyuncs.com/v1/chat/completions", "Authorization"},
	{"alibaba-token-plan", "qwen3.8-max", "openai-chat", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/chat/completions", "Authorization"},
	{"alibaba-token-plan-intl", "qwen3.7-max", "openai-chat", "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1/chat/completions", "Authorization"},
	{"parallel", "fixture-model", "openai-chat", "https://platform.parallel.ai/chat/completions", "Authorization"},
	{"zenmux", "fixture-model", "openai-chat", "https://zenmux.ai/api/v1/chat/completions", "Authorization"},
	{"litellm", "fixture-model", "openai-chat", "http://localhost:4000/v1/chat/completions", "Authorization"},
	{"mistral", "codestral-latest", "openai-chat", "https://api.mistral.ai/v1/chat/completions", "Authorization"},
	{"minimax", "MiniMax-M3.1-Flash", "openai-chat", "https://api.minimax.io/v1/chat/completions", "Authorization"},
	{"minimax-cn", "MiniMax-M3.1-Flash", "openai-chat", "https://api.minimaxi.com/v1/chat/completions", "Authorization"},
	{"kimi-code", "kimi-for-coding", "openai-chat", "https://api.kimi.com/coding/v1/chat/completions", "Authorization"},
	{"opencode-zen", "fixture-model", "openai-chat", "https://opencode.ai/zen/v1/chat/completions", "Authorization"},
	{"vercel-ai-gateway", "fixture-model", "openai-chat", "https://ai-gateway.vercel.sh/v1/chat/completions", "Authorization"},
	{"opper", "claude-sonnet-4-6", "openai-chat", "https://api.opper.ai/v3/compat/chat/completions", "Authorization"},
	{"opengateway", "z-ai/glm-5.3-flash-ultrafast", "openai-chat", "https://apis.opengateway.ai/v1/chat/completions", "Authorization"},
	{"tokenlab", "gpt-5.6-terra", "openai-chat", "https://api.tokenlab.sh/v1/chat/completions", "Authorization"},
	{"modelflare", "glm-5.3-flash", "openai-chat", "https://modelflare.dev/v1/chat/completions", "Authorization"},
	{"aistupidlevel", "auto-coding", "openai-chat", "https://aistupidlevel.info/v1/chat/completions", "Authorization"},
	{"xiaomi", "mimo-v2.6-pro", "anthropic", "https://api.xiaomimimo.com/anthropic/v1/messages", "X-Api-Key"},
	{"xiaomi-mimo", "mimo-v2.6-flash", "openai-chat", "https://api.xiaomimimo.com/v1/chat/completions", "Authorization"},
	{"kilo", "fixture-model", "openai-chat", "https://api.kilo.ai/api/gateway/chat/completions", "Authorization"},
	{"mimo", "mimo-v2.6-pro", "openai-chat", "https://token-plan-cn.xiaomimimo.com/v1/chat/completions", "Authorization"},
	{"cloudflare-ai-gateway", "fixture-model", "anthropic", "https://gateway.ai.cloudflare.com/v1/fixture-account/fixture-gateway/anthropic/v1/messages", "X-Api-Key"},
	{"cloudflare-workers-ai", "@cf/meta/llama-3.3-70b-instruct-fp8-fast", "openai-chat", "https://api.cloudflare.com/client/v4/accounts/fixture-account/ai/v1/chat/completions", "Authorization"},
	{"github-copilot", "gpt-4o", "openai-chat", "https://api.githubcopilot.com/chat/completions", "Authorization"},
	{"gitlab-duo", "fixture-model", "openai-chat", "https://cloud.gitlab.com/ai/v1/proxy/openai/v1/chat/completions", "Authorization"},
	{"stepfun", "step-5-preview", "openai-chat", "https://api.stepfun.com/v1/chat/completions", "Authorization"},
}

type presetHarness struct {
	target          inference.Target
	credential      inference.Credential
	sends, releases atomic.Int32
	usage           inference.UsageRecord
}

func (h *presetHarness) Admit(context.Context, string) (inference.Principal, func(), error) {
	return inference.Principal{OrganizationID: "org", UserID: "user", KeyID: "key", MembershipGeneration: 1}, func() { h.releases.Add(1) }, nil
}
func (h *presetHarness) BeforeSend(ctx context.Context, _ inference.Principal) error {
	return ctx.Err()
}
func (h *presetHarness) Dispatch(ctx context.Context, _ inference.Principal, send func(context.Context) error) error {
	h.sends.Add(1)
	return send(ctx)
}
func (h *presetHarness) Resolve(context.Context, inference.Principal, string, inference.Operation) (inference.Route, error) {
	return inference.Route{Targets: []inference.Target{h.target}, MaxSends: 1}, nil
}
func (h *presetHarness) Models(context.Context, inference.Principal) ([]inference.Model, error) {
	return nil, nil
}
func (h *presetHarness) Select(context.Context, inference.Principal, inference.Target, string) (inference.Credential, error) {
	return h.credential, nil
}
func (h *presetHarness) Refresh(context.Context, inference.Principal, inference.Target, inference.Credential) (inference.Credential, error) {
	return inference.Credential{}, inference.ErrNoCredential
}
func (h *presetHarness) Record(_ context.Context, _ inference.Principal, r inference.UsageRecord) error {
	h.usage = r
	return nil
}

type presetDNS struct{}

func (presetDNS) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

func presetConnection(t *testing.T, id string) providers.Connection {
	t.Helper()
	p, ok := providers.Lookup(id)
	if !ok {
		t.Fatal(id)
	}
	c := providers.Connection{Provider: id}
	switch p.Auth {
	case providers.Key:
		c.APIKey = "provider-secret"
	case providers.OAuth, providers.Forward:
		c.Credential = &providers.Credential{Provider: id, Scope: core.Scope{OrganizationID: "org", UserID: "user", Generation: 1}, AccessToken: "provider-secret", AccountID: "fixture-account", ProjectID: "fixture-project"}
	}
	if strings.Contains(p.BaseURL, "{") {
		c.BaseURL = strings.NewReplacer("{account-id}", "fixture-account", "{account_id}", "fixture-account", "{gateway}", "fixture-gateway").Replace(p.BaseURL)
	}
	return c
}

func presetPolicy(t *testing.T, c providers.Connection, model string, overrides json.RawMessage) (providers.Route, policy.Resolved) {
	t.Helper()
	r, err := providers.Resolve(c, model, providers.InboundChat)
	if err != nil {
		t.Fatal(err)
	}
	auth := r.Preset.Auth
	if c.Auth != "" {
		auth = c.Auth
	}
	p, err := policy.Resolve(policy.Config{ProviderID: c.Provider, Model: r.Model, BaseURL: r.BaseURL, Wire: r.Wire, Inbound: providers.InboundChat, Auth: auth, Registry: r.Preset.Metadata, Overrides: overrides})
	if err != nil {
		t.Fatal(err)
	}
	r, err = providers.ResolvePolicyWire(c, r.Model, providers.InboundChat, p.Wire)
	if err != nil {
		t.Fatal(err)
	}
	return r, p
}

func TestGenericPresetExecution(t *testing.T) {
	// One ephemeral CA certificate covers the pinned hosts. TLS verification remains
	// enabled; DNS and the dialer can reach only the local fixture listener.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, tc := range genericRequests {
		u, _ := url.Parse(tc.endpoint)
		cert.DNSNames = append(cert.DNSNames, u.Hostname())
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	seen := map[string]bool{}
	for _, tc := range genericRequests {
		t.Run(tc.id, func(t *testing.T) {
			if seen[tc.id] {
				t.Fatal("duplicate preset")
			}
			seen[tc.id] = true
			c := presetConnection(t, tc.id)
			if tc.id == "anthropic-apikey" {
				c.Headers = map[string]string{"aNtHrOpIc-VeRsIoN": "fixture-version", "AnThRoPiC-BeTa": "fixture-beta"}
			}
			// Omission exercises preset defaults; dynamic catalogs require an explicit ID.
			p, _ := providers.Lookup(tc.id)
			model := ""
			if p.DefaultModel == "" {
				model = tc.model
			}
			r, pol := presetPolicy(t, c, model, nil)
			if r.Model != tc.model || string(r.Wire) != tc.wire || pol.Endpoint != tc.endpoint {
				t.Fatalf("resolved model=%q wire=%q endpoint=%q; want %+v", r.Model, r.Wire, pol.Endpoint, tc)
			}
			auth := inference.APIKey
			if p.Auth == providers.Local {
				auth = inference.Anonymous
			} else if p.Auth == providers.OAuth || p.Auth == providers.Forward {
				auth = inference.OAuth
			}
			h := &presetHarness{target: inference.Target{Provider: tc.id, BaseURL: r.BaseURL, Model: r.Model, Wire: inference.Wire(r.Wire), Auth: auth, Policy: &pol, Endpoint: pol.Endpoint}, credential: inference.Credential{AccountID: "fixture-account", Headers: r.Headers.Clone()}}
			if auth != inference.Anonymous {
				h.credential.Secret = "provider-secret"
			}
			var received atomic.Int32
			wantURL, _ := url.Parse(tc.endpoint)
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				received.Add(1)
				if req.Method != "POST" || req.Host != wantURL.Host || req.URL.RequestURI() != wantURL.RequestURI() {
					t.Errorf("request %s %s%s", req.Method, req.Host, req.URL.RequestURI())
				}
				for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
					want := ""
					if name == tc.authHeader {
						want = "provider-secret"
						if name == "Authorization" {
							want = "Bearer " + want
						}
					}
					if got := req.Header.Get(name); got != want {
						t.Errorf("%s=%q want %q", name, got, want)
					}
				}
				version := "2023-06-01"
				if tc.id == "anthropic-apikey" {
					version = "fixture-version"
					if req.Header.Get("Anthropic-Beta") != "fixture-beta" {
						t.Error("custom Anthropic beta overwritten")
					}
				}
				if r.Wire == providers.Anthropic && req.Header.Get("Anthropic-Version") != version {
					t.Error("missing Anthropic version")
				}
				if tc.id == "openai" && req.Header.Get("ChatGPT-Account-Id") != "fixture-account" {
					t.Error("missing account identity")
				}
				if tc.id == "meta-muse" && (req.Header.Get("X-Api-Version") != "1.0.0" || req.Header.Get("User-Agent") != "muse-build/1.3.0 (opencodex compatibility)") {
					t.Error("missing pinned Muse fingerprint")
				}
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["model"] != tc.model {
					t.Errorf("serialized model=%v", body["model"])
				}
				field := "messages"
				if r.Wire == providers.Responses {
					field = "input"
				}
				if body[field] == nil {
					t.Errorf("missing %s payload", field)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.Wire {
				case providers.Responses:
					w.Write([]byte(`{"id":"resp_fixture","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":7,"output_tokens":2}}`))
				case providers.Anthropic:
					w.Write([]byte(`{"id":"msg_fixture","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":2}}`))
				default:
					w.Write([]byte(`{"id":"chat_fixture","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`))
				}
			}))
			if wantURL.Scheme == "https" {
				srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
				srv.StartTLS()
			} else {
				srv.Start()
			}
			defer srv.Close()
			handler, err := inference.New(inference.Config{Authorizer: h, Router: h, Credentials: h, Usage: h, Destinations: inference.DestinationPolicy{ApprovedOrigins: []string{wantURL.Scheme + "://" + wantURL.Host}, Resolver: presetDNS{}, TLSConfig: &tls.Config{RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, _, e := net.SplitHostPort(address)
				if e != nil || host != "127.0.0.1" {
					t.Errorf("unpinned dial %q", address)
					return nil, inference.ErrDestination
				}
				return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
			}}})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public-alias","messages":[{"role":"user","content":"hello"}],"stream":false}`))
			req.Header.Set("Authorization", "Bearer client-secret")
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, req)
			if out.Code != 200 {
				t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
			}
			if received.Load() != 1 || h.sends.Load() != 1 || h.releases.Load() != 1 || h.usage.Sends != 1 {
				t.Fatalf("ungated/repeated/missing send: received=%d dispatched=%d released=%d usage=%d", received.Load(), h.sends.Load(), h.releases.Load(), h.usage.Sends)
			}
		})
	}
	if len(seen) != 87 {
		t.Fatalf("pinned generic inventory=%d want87", len(seen))
	}
}

// Every preset outside the generic HTTP matrix has an explicit reason. This is
// deliberately separate from the runtime Wire field so a catalog change cannot
// silently expand or shrink the tested population.
func TestGenericPresetExecutionInventory(t *testing.T) {
	excluded := map[string]string{
		"cursor": "Connect adapter", "zed": "exchanged-token adapter", "devin": "Connect adapter",
		"command-code": "specialized account protocol", "workbuddy-ai": "specialized account protocol", "dsh-account": "signed account protocol",
		"kiro": "AWS eventstream", "azure-openai": "deployment adapter", "ollama-cloud": "native Ollama adapter", "mimo-free": "saved deprecated adapter",
		"jev": "credential-only decision service", "exa": "additive credential-only search service",
		"qoder": "excluded local execution", "qoder-cn": "excluded local execution", "codebuddy": "excluded local execution", "codebuddy-cn": "excluded local execution", "claude-cli": "excluded local execution",
		"google": "native Build test below", "google-antigravity": "project-bound Build test below",
		"google-vertex": "real handler/TLS coverage in TestVertexHandlerTLSDispatch",
	}
	seen := map[string]bool{}
	for _, tc := range genericRequests {
		seen[tc.id] = true
	}
	for _, p := range providers.Catalog() {
		if seen[p.ID] {
			if excluded[p.ID] != "" {
				t.Fatalf("overlap %s", p.ID)
			}
			delete(seen, p.ID)
			continue
		}
		if excluded[p.ID] == "" {
			t.Errorf("unaccounted preset %s", p.ID)
		}
		delete(excluded, p.ID)
	}
	if len(seen) != 0 || len(excluded) != 0 {
		t.Fatalf("stale inventory: generic=%v excluded=%v", seen, excluded)
	}
	for _, id := range []string{"qoder", "qoder-cn", "codebuddy", "codebuddy-cn", "claude-cli", "jev", "exa"} {
		if _, err := providers.Resolve(providers.Connection{Provider: id, APIKey: "provider-secret"}, "fixture-model", providers.InboundChat); err == nil {
			t.Errorf("excluded/credential-only preset %s became generically routable", id)
		}
	}
}

func TestGenericGoogleBuild(t *testing.T) {
	for _, tc := range []struct {
		id, model, endpoint string
		cca                 bool
	}{
		{"google", "gemini-3.5-flash", "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-flash:generateContent", false},
		{"google-antigravity", "gemini-3.8-flash", "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent", true},
	} {
		t.Run(tc.id, func(t *testing.T) {
			c := presetConnection(t, tc.id)
			r, p := presetPolicy(t, c, "", nil)
			a := google.NewAPI(google.Options{})
			auth := inference.APIKey
			if tc.cca {
				a = google.NewAntigravity(google.Options{})
				auth = inference.OAuth
			}
			cred := inference.Credential{Secret: "provider-secret", AccountID: "fixture-account", Headers: r.Headers.Clone()}
			if tc.cca {
				cred.Headers.Set(google.ProjectHeader, c.Credential.ProjectID)
			}
			input := inference.AdapterInput{Target: inference.Target{Provider: tc.id, BaseURL: r.BaseURL, Model: r.Model, Auth: auth, Policy: &p}, Credential: cred, Operation: inference.Generate, Wire: inference.Chat, Body: json.RawMessage(`{"messages":[{"role":"user","content":"hello"}],"stream":false}`)}
			got, err := a.Build(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if got.URL != tc.endpoint || (got.Method != "" && got.Method != "POST") {
				t.Fatalf("request %s %s", got.Method, got.URL)
			}
			var body map[string]any
			if err := json.Unmarshal(got.Body, &body); err != nil {
				t.Fatal(err)
			}
			// Pinned Antigravity bare 3.8 Flash selects its medium effort wire ID.
			if tc.cca {
				if body["project"] != "fixture-project" || body["model"] != tc.model+"-medium" || got.Headers.Get("Authorization") != "Bearer provider-secret" || got.Headers.Get("X-Goog-Api-Key") != "" || got.Headers.Get(google.ProjectHeader) != "" {
					t.Fatal("CCA envelope/auth mapping lost")
				}
				input.Credential.Headers.Del(google.ProjectHeader)
				if _, err := a.Build(context.Background(), input); err == nil {
					t.Fatal("CCA accepted missing discovered project")
				}
				body = body["request"].(map[string]any)
			} else if got.Headers.Get("X-Goog-Api-Key") != "provider-secret" || got.Headers.Get("Authorization") != "" {
				t.Fatal("wrong Google API auth")
			}
			if body["contents"] == nil {
				t.Fatal("missing translated Google contents")
			}
		})
	}
}

func TestGenericPinnedModelPolicy(t *testing.T) {
	for _, tc := range []struct {
		id, model       string
		context, output int64
	}{
		{"anthropic", "claude-sonnet-5", 1000000, 128000},
		{"openai-apikey", "gpt-6-astra", 1050000, 128000},
		{"openrouter", "openai/gpt-6.1-sol", 1050000, 0},
		{"deepseek", "deepseek-flash", 1048576, 0},
	} {
		t.Run(tc.id, func(t *testing.T) {
			_, p := presetPolicy(t, presetConnection(t, tc.id), tc.model, nil)
			m := p.ModelPolicy()
			if m.ContextWindow == nil || *m.ContextWindow != tc.context {
				t.Fatalf("context=%v want%d", m.ContextWindow, tc.context)
			}
			if tc.output > 0 && (m.MaxOutputTokens == nil || *m.MaxOutputTokens != tc.output) {
				t.Fatalf("output=%v want%d", m.MaxOutputTokens, tc.output)
			}
		})
	}
	t.Run("explicit-false-service-tier", func(t *testing.T) {
		c := presetConnection(t, "openrouter")
		_, p := presetPolicy(t, c, "openai/gpt-6.1-sol", json.RawMessage(`{"modelSupportsServiceTier":{"openai/gpt-6.1-sol":false}}`))
		m := p.ModelPolicy()
		if m.SupportsServiceTier == nil || *m.SupportsServiceTier {
			t.Fatal("explicit false lost")
		}
		b, err := p.TransformChatRequest(map[string]any{"model": "openai/gpt-6.1-sol", "messages": []any{}, "service_tier": "priority"})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := b["service_tier"]; ok {
			t.Fatal("disabled service tier sent")
		}
		c.BaseURL = "https://unrelated.example/v1"
		if _, err := providers.Resolve(c, "openai/gpt-6.1-sol", providers.InboundChat); err == nil {
			t.Fatal("fixed preset accepted changed destination")
		}
		// Policy is also defensive when called independently by a route planner.
		preset, _ := providers.Lookup("openrouter")
		p, err = policy.Resolve(policy.Config{ProviderID: c.Provider, Model: "openai/gpt-6.1-sol", BaseURL: c.BaseURL, Wire: providers.Chat, Inbound: providers.InboundChat, Auth: providers.Key, Registry: preset.Metadata})
		if err != nil {
			t.Fatal(err)
		}
		if p.TransportMatchedRegistry || p.ModelPolicy().SupportsServiceTier != nil {
			t.Fatal("canonical service-tier evidence followed changed destination")
		}
	})
	t.Run("volcengine-reasoning-explicit-false", func(t *testing.T) {
		for _, keep := range []bool{false, true} {
			var overrides json.RawMessage
			if keep {
				overrides = json.RawMessage(`{"dropResponsesReasoningItems":false}`)
			}
			_, p := presetPolicy(t, presetConnection(t, "volcengine-coding-plan"), "ark-code-latest", overrides)
			b, err := p.TransformRequest(map[string]any{"input": []any{map[string]any{"type": "reasoning", "id": "reason", "summary": []any{}}, map[string]any{"type": "message", "role": "user", "content": "hello"}}})
			if err != nil {
				t.Fatal(err)
			}
			n := 1
			if keep {
				n = 2
			}
			if len(b["input"].([]any)) != n {
				t.Fatalf("reasoning override keep=%t body=%v", keep, b)
			}
		}
	})
	t.Run("trusted-hardpins-auth", func(t *testing.T) {
		for _, tc := range []struct {
			id, model        string
			wire             providers.Wire
			endpoint, header string
		}{
			{"commandcode", "claude-sonnet-5", providers.Anthropic, "https://api.commandcode.ai/provider/v1/messages", "X-Api-Key"},
			{"tokenlab", "claude-sonnet-5", providers.Anthropic, "https://api.tokenlab.sh/v1/messages", "X-Api-Key"},
			{"opencode-go", "claude-haiku-5-5", providers.Anthropic, "https://opencode.ai/zen/go/v1/messages", "X-Api-Key"},
			{"github-copilot", "gpt-5.5", providers.Responses, "https://api.githubcopilot.com/responses", "Authorization"},
		} {
			r, p := presetPolicy(t, presetConnection(t, tc.id), tc.model, nil)
			if r.Wire != tc.wire || p.Endpoint != tc.endpoint || r.Headers.Get(tc.header) == "" {
				t.Fatalf("%s hardpin lost: wire=%s endpoint=%s headers=%v", tc.id, r.Wire, p.Endpoint, r.Headers)
			}
			if tc.wire == providers.Anthropic && r.Headers.Get("Authorization") != "" {
				t.Fatal("Bearer leaked across wire switch")
			}
		}
	})
	t.Run("deepseek-inbound-gate", func(t *testing.T) {
		c := presetConnection(t, "deepseek")
		for _, in := range []providers.Inbound{providers.InboundChat, providers.InboundResponses} {
			r, err := providers.Resolve(c, "deepseek-flash", in)
			if err != nil {
				t.Fatal(err)
			}
			p, err := policy.Resolve(policy.Config{ProviderID: c.Provider, Model: r.Model, BaseURL: r.BaseURL, Wire: r.Wire, Inbound: in, Auth: providers.Key, Registry: r.Preset.Metadata})
			if err != nil {
				t.Fatal(err)
			}
			want := "https://api.deepseek.com/chat/completions"
			if in == providers.InboundResponses {
				want = "https://api.deepseek.com/responses"
			}
			if p.Endpoint != want {
				t.Fatalf("%s endpoint=%s", in, p.Endpoint)
			}
		}
	})
}
