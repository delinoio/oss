// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const hostedSentinel = "private-hosted-key-sentinel"

func hostedFixture(p profile, id, next string) string {
	switch p {
	case geminiModels:
		return fmt.Sprintf(`{"models":[{"name":"models/%s","supportedGenerationMethods":["generateContent"],"inputTokenLimit":9007199254740993,"thinking":false},{"name":"models/embed-%s","supportedGenerationMethods":["embedContent"]}],"nextPageToken":%q}`, id, id, next)
	case togetherModels:
		return fmt.Sprintf(`[{"id":%q,"display_name":"Chat model","type":"chat"},{"id":"embed-%s","type":"embedding"}]`, id, id)
	case fireworksModels:
		return fmt.Sprintf(`{"models":[{"name":"accounts/fireworks/models/%s","state":"READY","kind":"HF_BASE_MODEL","supportsServerless":true,"supportsTools":true,"supportsImageInput":false},{"name":"accounts/fireworks/models/private-%s","state":"READY","kind":"HF_BASE_MODEL","supportsServerless":false}],"nextPageToken":%q}`, id, id, next)
	case cohereModels:
		return fmt.Sprintf(`{"models":[{"name":%q,"endpoints":["chat"],"context_length":65536},{"name":"deprecated-%s","is_deprecated":true}],"next_page_token":%q}`, id, id, next)
	case basetenModels:
		return fmt.Sprintf(`{"items":[{"name":%q,"display_name":"Model","context_length":12345,"invoke_url":"https://untrusted.invalid/private-hosted-key-sentinel","pricing":{"key":"private-hosted-key-sentinel"},"organization":"private-hosted-key-sentinel"}],"pagination":{"has_more":%t,"cursor":%q}}`, id, next != "", next)
	case qianfanModels:
		return fmt.Sprintf(`{"data":[{"id":%q,"type":"chat","architecture":{"output_modalities":["text"]}},{"id":"audio-%s","type":"chat","architecture":{"output_modalities":["audio"]}}]}`, id, id)
	case mistralModels:
		return fmt.Sprintf(`{"data":[{"id":%q,"capabilities":{"completion_chat":true,"function_calling":false}},{"id":"embed-%s","capabilities":{"completion_chat":false}}]}`, id, id)
	case deepInfraModels:
		return fmt.Sprintf(`{"data":[{"id":%q,"metadata":{"context_length":131072},"pricing":"private-hosted-key-sentinel"}]}`, id)
	case alibabaModels:
		return fmt.Sprintf(`{"success":true,"output":{"total":1,"page_no":1,"page_size":100,"models":[{"model":%q,"name":"Model","model_info":{"context_window":9007199254740993},"features":["function-calling"]}]}}`, id)
	default:
		return fmt.Sprintf(`{"data":[{"id":%q,"context_length":null,"ignored":"private-hosted-key-sentinel"}]}`, id)
	}
}
func hostedPrivateFixture(p profile) string {
	switch p {
	case novitaModels:
		return `{"availableBalance":"0.0001","cashBalance":"-1.25","creditLimit":"0","pendingCharges":"0","outstandingInvoices":"0","ignored":"private-hosted-key-sentinel"}`
	case deepInfraModels:
		return `{"uid":"private-hosted-key-sentinel","account_balance":12345}`
	case huggingFaceModels:
		return `{"id":"private-hosted-key-sentinel","name":"private-hosted-key-sentinel","type":"user","auth":{"type":"access_token","accessToken":{"secret":"private-hosted-key-sentinel"}}}`
	case veniceModels:
		return `{"data":{"accessPermitted":true,"balance":"private-hosted-key-sentinel"}}`
	}
	return ""
}
func TestAdditionalHostedProfilesUseExactTargetsAndPrivateVerification(t *testing.T) {
	for _, preset := range additionalHostedPresets() {
		t.Run(string(preset.ID), func(t *testing.T) {
			p := preset.Provider
			base, _ := url.Parse(p.Endpoint)
			selected := endpointProfile(*base, p)
			if selected == custom {
				t.Fatal("official profile was not selected")
			}
			calls := 0
			client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.Scheme != "https" || r.Header.Get("Cookie") != "" || strings.Contains(r.URL.String(), hostedSentinel) {
					t.Fatal("inspection leaked credential or sent inference")
				}
				gemini := selected == geminiModels
				if gemini && (r.Header.Get("x-goog-api-key") != hostedSentinel || r.Header.Get("Authorization") != "") || !gemini && (r.Header.Get("Authorization") != "Bearer "+hostedSentinel || r.Header.Get("x-goog-api-key") != "") {
					t.Fatal("unexpected credential header")
				}
				w := httptest.NewRecorder()
				verification := privateVerification(selected)
				if verification != "" && calls == 1 {
					if r.URL.String() != verification {
						t.Fatal("private verification skipped")
					}
					fmt.Fprint(w, hostedPrivateFixture(selected))
				} else {
					target, _ := hostedTarget(selected, *base, 0, "")
					if r.URL.String() != target.String() {
						t.Fatal("listing changed closed target")
					}
					fmt.Fprint(w, hostedFixture(selected, "model-a", ""))
				}
				return w.Result(), nil
			})}
			o := inspect(context.Background(), client, p, []byte(hostedSentinel))
			wantCalls := 1
			if privateVerification(selected) != "" {
				wantCalls++
			}
			if o.Failure != NoFailure || o.Authentication != CredentialAccepted || len(o.Models) != 1 || calls != wantCalls {
				t.Fatalf("inspection failed: failure=%s auth=%s models=%d calls=%d", o.Failure, o.Authentication, len(o.Models), calls)
			}
			raw, _ := json.Marshal(o)
			if strings.Contains(string(raw), hostedSentinel) || strings.Contains(string(raw), "untrusted") {
				t.Fatal("private metadata escaped")
			}
			for _, changed := range []domain.Provider{
				{Name: p.Name, Endpoint: strings.Replace(p.Endpoint, base.Hostname(), "untrusted.invalid", 1), Protocol: p.Protocol, Authentication: p.Authentication},
				{Name: p.Name, Endpoint: p.Endpoint + "/untrusted", Protocol: p.Protocol, Authentication: p.Authentication},
				{Name: p.Name, Endpoint: p.Endpoint, Protocol: domain.OpenAIResponses, Authentication: p.Authentication},
				{Name: p.Name, Endpoint: p.Endpoint, Protocol: p.Protocol, Authentication: domain.APIKeyAuth},
			} {
				u, _ := url.Parse(changed.Endpoint)
				if endpointProfile(*u, changed) != custom {
					t.Fatal("changed profile inherited authentication authority")
				}
			}
		})
	}
}
func TestHostedPrivateVerificationCannotBeBypassedByPublicModels(t *testing.T) {
	for _, p := range []profile{novitaModels, deepInfraModels, huggingFaceModels, veniceModels} {
		for _, mode := range []string{"invalid", "401", "403", "429", "denied", "redirect"} {
			t.Run(fmt.Sprintf("%d/%s", p, mode), func(t *testing.T) {
				calls := 0
				client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: testTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if calls != 1 || r.URL.String() != privateVerification(p) {
						t.Fatal("catalog bypassed failed private verification")
					}
					w := httptest.NewRecorder()
					w.Header().Set("Retry-After", "7")
					switch mode {
					case "401":
						w.WriteHeader(401)
					case "403":
						w.WriteHeader(403)
					case "429":
						w.WriteHeader(429)
					case "redirect":
						w.Header().Set("Location", "https://untrusted.invalid")
						w.WriteHeader(307)
					case "denied":
						if p == veniceModels {
							fmt.Fprint(w, `{"data":{"accessPermitted":false}}`)
						} else {
							fmt.Fprint(w, `{}`)
						}
					default:
						fmt.Fprint(w, `{"data":[{"id":"public-model"}],"uid":null}`)
					}
					return w.Result(), nil
				})}
				provider := domain.Provider{Authentication: domain.BearerAuth, Protocol: domain.OpenAIChat, Enabled: new(true)}
				u, _ := url.Parse("https://unused.invalid/v1")
				o := inspectHosted(context.Background(), client, provider, []byte(hostedSentinel), p, *u)
				want := InvalidResponse
				switch mode {
				case "401":
					want = AuthenticationRejected
				case "403":
					want = AccessDenied
				case "429":
					want = RateLimited
				case "redirect":
					want = RedirectRefused
				case "denied":
					if p == veniceModels {
						want = AccessDenied
					}
				}
				if o.Failure != want || o.Authentication != AuthenticationUnknown || len(o.Models) != 0 || calls != 1 {
					t.Fatalf("private failure lost: failure=%s auth=%s", o.Failure, o.Authentication)
				}
				if mode == "429" && (o.RetryAfterSeconds == nil || *o.RetryAfterSeconds != 7) {
					t.Fatal("retry delay not retained")
				}
			})
		}
	}
}
func TestHostedCursorPaginationAndFailurePublication(t *testing.T) {
	for _, p := range []profile{geminiModels, fireworksModels, cohereModels, basetenModels} {
		for _, mode := range []string{"complete", "repeat", "cycle", "duplicate", "empty", "secret-token", "bad-second", "page-limit"} {
			t.Run(fmt.Sprintf("%d/%s", p, mode), func(t *testing.T) {
				calls := 0
				base, _ := url.Parse("https://fixed.example/v1")
				previous := ""
				client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
					target, _ := hostedTarget(p, *base, calls, previous)
					calls++
					if r.URL.String() != target.String() {
						t.Fatal("cursor followed external metadata")
					}
					next := ""
					if calls == 1 {
						next = "cursor-a"
					}
					id := fmt.Sprintf("model-%d", calls)
					switch mode {
					case "repeat":
						next = "cursor-a"
					case "cycle":
						if calls == 2 {
							next = "cursor-b"
						}
						if calls == 3 {
							next = "cursor-a"
						}
					case "duplicate":
						if calls == 2 {
							id = "model-1"
						}
					case "secret-token":
						next = hostedSentinel
					case "page-limit":
						next = fmt.Sprintf("cursor-%d", calls)
					}
					previous = next
					raw := hostedFixture(p, id, next)
					if mode == "bad-second" && calls == 2 {
						raw = `{"data":null}`
					}
					if mode == "empty" {
						switch p {
						case geminiModels, fireworksModels:
							raw = `{"models":[],"nextPageToken":"cursor-a"}`
						case cohereModels:
							raw = `{"models":[],"next_page_token":"cursor-a"}`
						case basetenModels:
							raw = `{"items":[],"pagination":{"has_more":true,"cursor":"cursor-a"}}`
						}
					}
					w := httptest.NewRecorder()
					fmt.Fprint(w, raw)
					return w.Result(), nil
				})}
				provider := domain.Provider{Authentication: domain.BearerAuth, Protocol: domain.OpenAIChat, Enabled: new(true)}
				o := inspectHosted(context.Background(), client, provider, []byte(hostedSentinel), p, *base)
				if mode == "complete" {
					if o.Failure != NoFailure || len(o.Models) != 2 || calls != 2 {
						t.Fatalf("incomplete pagination: failure=%s count=%d calls=%d", o.Failure, len(o.Models), calls)
					}
				} else {
					want := InvalidResponse
					if mode == "page-limit" {
						want = ResponseTooLarge
					}
					if o.Failure != want || len(o.Models) != 0 {
						t.Fatal("incomplete inventory published")
					}
				}
			})
		}
	}
}
func TestHostedKnownMetadataAndMalformedResponses(t *testing.T) {
	for _, p := range []profile{geminiModels, togetherModels, fireworksModels, cohereModels, qianfanModels, mistralModels, deepInfraModels, alibabaModels, basetenModels} {
		raw := hostedFixture(p, "model", "")
		page, err := parseHostedPage([]byte(raw), p, 0, 0, []byte(hostedSentinel))
		if err != nil || len(page.models) != 1 {
			t.Fatalf("valid profile %d failed", p)
		}
		switch p {
		case geminiModels, alibabaModels:
			if page.models[0].ContextLimit == nil || *page.models[0].ContextLimit != 9007199254740993 {
				t.Fatal("integer precision lost")
			}
		case deepInfraModels:
			if page.models[0].ContextLimit == nil || *page.models[0].ContextLimit != 131072 {
				t.Fatal("nested context lost")
			}
		}
		for _, malformed := range []string{raw + `{}`, `{"data":[],"data":[]}`, strings.ReplaceAll(raw, "model", hostedSentinel)} {
			if _, err := parseHostedPage([]byte(malformed), p, 0, 0, []byte(hostedSentinel)); err == nil {
				t.Fatal("malformed or reflected metadata accepted")
			}
		}
	}
	for _, raw := range []string{`{"items":[],"pagination":{"has_more":true}}`, `{"items":[],"pagination":{"has_more":true,"cursor":""}}`, `{"items":[],"pagination":{"has_more":null}}`, `{"items":[{"name":"valid","context_length":1.1}],"pagination":{"has_more":false}}`} {
		if _, err := parseHostedPage([]byte(raw), basetenModels, 0, 0, nil); err == nil {
			t.Fatal("invalid Baseten page accepted")
		}
	}
}
func TestAlibabaTotalPaginationIsCompleteAndStable(t *testing.T) {
	for _, mode := range []string{"complete", "changed-total", "incomplete", "wrong-page", "duplicate", "null-success"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "dashscope-intl.aliyuncs.com" || r.URL.Path != "/api/v1/models" || r.URL.Query().Get("page_no") != fmt.Sprint(calls) || r.URL.Query().Get("page_size") != "100" || r.URL.Query().Get("capabilities") != "TG" {
					t.Fatal("fixed Alibaba pagination escaped")
				}
				count := 100
				if calls == 2 || mode == "incomplete" {
					count = 1
				}
				models := []map[string]any{}
				for i := 0; i < count; i++ {
					id := fmt.Sprintf("m-%d-%d", calls, i)
					if calls == 2 && mode == "duplicate" {
						id = "m-1-0"
					}
					models = append(models, map[string]any{"model": id})
				}
				total := 101
				if calls == 2 && mode == "changed-total" {
					total = 102
				}
				number := calls
				if mode == "wrong-page" {
					number++
				}
				var success any = true
				if mode == "null-success" {
					success = nil
				}
				w := httptest.NewRecorder()
				json.NewEncoder(w).Encode(map[string]any{"success": success, "output": map[string]any{"models": models, "total": total, "page_no": number, "page_size": 100}})
				return w.Result(), nil
			})}
			p := domain.Provider{Endpoint: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth, Enabled: new(true)}
			o := inspect(context.Background(), client, p, []byte(hostedSentinel))
			if mode == "complete" {
				if o.Failure != NoFailure || len(o.Models) != 101 || calls != 2 {
					t.Fatal("complete Alibaba inventory unavailable")
				}
			} else if o.Failure != InvalidResponse || len(o.Models) != 0 {
				t.Fatal("inconsistent Alibaba inventory published")
			}
		})
	}
}

func TestHostedFilteredIdentitiesAndAggregateBudgets(t *testing.T) {
	for _, mode := range []string{"filtered-duplicate", "filtered-limit", "aggregate-bytes", "private-then-failed-catalog"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			p := geminiModels
			if mode == "private-then-failed-catalog" {
				p = huggingFaceModels
			}
			client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				w := httptest.NewRecorder()
				if mode == "private-then-failed-catalog" {
					if calls == 1 {
						fmt.Fprint(w, hostedPrivateFixture(p))
					} else {
						w.WriteHeader(403)
					}
					return w.Result(), nil
				}
				next := fmt.Sprintf("page-%d", calls)
				values := []map[string]any{}
				count := 1
				if mode == "filtered-limit" {
					count = 1000
				}
				for i := 0; i < count; i++ {
					id := fmt.Sprintf("models/embed-%d-%d", calls, i)
					if mode == "filtered-duplicate" {
						id = "models/repeated-filtered"
					}
					values = append(values, map[string]any{"name": id, "supportedGenerationMethods": []string{"embedContent"}})
				}
				body := map[string]any{"models": values, "nextPageToken": next}
				if mode == "aggregate-bytes" {
					body["ignored"] = strings.Repeat("x", (maxBody*4)/5)
				}
				json.NewEncoder(w).Encode(body)
				return w.Result(), nil
			})}
			base, _ := url.Parse("https://fixed.example/v1")
			o := inspectHosted(context.Background(), client, domain.Provider{Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth, Enabled: new(true)}, []byte(hostedSentinel), p, *base)
			want, wantCalls := ResponseTooLarge, 5
			switch mode {
			case "filtered-duplicate":
				want, wantCalls = InvalidResponse, 2
			case "filtered-limit":
				wantCalls = 11
			case "private-then-failed-catalog":
				want, wantCalls = AccessDenied, 2
			}
			if o.Failure != want || len(o.Models) != 0 || calls != wantCalls {
				t.Fatal("incomplete/oversized filtered catalog published", o.Failure, calls)
			}
			if mode == "private-then-failed-catalog" && o.Authentication != CredentialAccepted {
				t.Fatal("separate private evidence lost")
			}
		})
	}
}
