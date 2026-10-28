// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func nativePolicyFixture(t *testing.T, mode NativeCompatibilityMode) NativeCompatibilityPolicy {
	t.Helper()
	platform, build := "windows", NativeCompatibilityClaudeBuild
	if mode == NativeCompatibilityCodexObserve || mode == NativeCompatibilityCodexAccountUITrial {
		build = NativeCompatibilityCodexBuild
	}
	p, err := NewNativeCompatibilityPolicy(mode, platform, build)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestNativeCompatibilityBuildAndRoutes(t *testing.T) {
	for _, tt := range []struct {
		mode            NativeCompatibilityMode
		platform, build string
	}{{NativeCompatibilityCodexObserve, "darwin", NativeCompatibilityCodexBuild}, {NativeCompatibilityCodexObserve, "windows", "26.924.2739.0"}, {NativeCompatibilityClaudePicker, "linux", NativeCompatibilityClaudeBuild}, {NativeCompatibilityClaudePicker, "windows", ""}, {"chatgpt", "windows", NativeCompatibilityCodexBuild}} {
		if _, err := NewNativeCompatibilityPolicy(tt.mode, tt.platform, tt.build); err == nil {
			t.Fatalf("accepted unsupported %+v", tt)
		}
	}
	p := nativePolicyFixture(t, NativeCompatibilityClaudePicker)
	for _, raw := range []string{"https://api.anthropic.com/v1/messages", "https://api.anthropic.com:443/v1/messages/count_tokens"} {
		u, _ := url.Parse(raw)
		d, err := p.Decide("POST", u)
		if err != nil || d.Action != NativeCompatibilityGateway || d.GatewayPath != u.Path {
			t.Fatalf("gateway %v %v", d, err)
		}
	}
	for _, raw := range []string{"https://api.anthropic.com.evil/v1/messages", "https://evil@api.anthropic.com/v1/messages", "https://api.anthropic.com:444/v1/messages", "http://api.anthropic.com/v1/messages", "https://api.anthropic.com/v1/%6dessages", "https://api.anthropic.com/v1/messages/", "https://api.anthropic.com/v1/../v1/messages", "https://api.anthropic.com/v1/messages#x", "https://claude.ai/api/chat", "https://chatgpt.com/backend-api/wham/usage"} {
		u, _ := url.Parse(raw)
		if _, err := p.Decide("POST", u); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := p.Decide("POST", nil); err == nil {
		t.Fatal("nil URL")
	}
	u, _ := url.Parse("https://api.anthropic.com/v1/messages")
	if _, err := p.Decide("GET", u); err == nil {
		t.Fatal("wrong method")
	}
	for _, path := range []string{"/api/bootstrap", "/edge-api/bootstrap/org-123/app_start/"} {
		u, _ := url.Parse("https://claude.ai" + path)
		d, err := p.Decide("GET", u)
		if err != nil || d.ResponseKind != NativeCompatibilityResponsePicker {
			t.Fatalf("picker %v %v", d, err)
		}
	}
	hosts := p.Hosts()
	hosts[0] = "evil"
	if p.Hosts()[0] == "evil" {
		t.Fatal("mutable allowlist")
	}
	h := NativeCompatibilityGatewayHeaders(http.Header{"Authorization": {"secret"}, "Cookie": {"secret"}, "X-Api-Key": {"secret"}, "Chatgpt-Account-Id": {"secret"}, "X-Unknown": {"secret"}, "Proxy-Authorization": {"secret"}, "Anthropic-Version": {"2023-06-01"}, "Content-Type": {"application/json"}})
	if len(h) != 2 || h.Get("Anthropic-Version") == "" {
		t.Fatalf("credential boundary %v", h)
	}
}
func TestNativeCompatibilityCLIClassification(t *testing.T) {
	p := nativePolicyFixture(t, NativeCompatibilityClaudeFirstParty)
	for _, ua := range []string{"claude-cli/2.1.296 (external, cli)", "claude-cli/2.1.296 (external, claude-desktop)", "claude-code/2.1.296", ""} {
		req, _ := http.NewRequest("GET", "https://api.anthropic.com/api/organizations/org-123/model_selector/cc", nil)
		req.Header.Set("User-Agent", ua)
		d, err := p.DecideRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		want := NativeCompatibilityResponseUnchanged
		if strings.Contains(ua, ", cli)") {
			want = NativeCompatibilityResponseCLISelector
		}
		if d.ResponseKind != want {
			t.Fatalf("classification %q: %v", ua, d)
		}
	}
}
func TestNativeCompatibilityPickerRewrite(t *testing.T) {
	p := nativePolicyFixture(t, NativeCompatibilityClaudePicker)
	raw := []byte(`{"model_selector_config":[{"id":"ccd","models":[{"id":"claude-native","name":"Native","disabled":false,"badge":"native","version":"old","supports_tools":true}]},{"id":"ccr","models":[{"id":"claude-native"}]}],"other":9007199254740993}`)
	h := http.Header{"Content-Type": {"application/json"}, "Etag": {"old"}, "Content-Length": {"123"}}
	d := NativeCompatibilityDecision{Action: NativeCompatibilityUpstream, ResponseKind: NativeCompatibilityResponsePicker}
	out, err := p.RewriteResponse(d, 200, h, raw, []NativeCompatibilityModel{{ID: "claude-routed", Name: "Routed", ContextWindow: 12345}})
	if err != nil || bytes.Equal(raw, out) {
		t.Fatalf("rewrite %s %v", out, err)
	}
	if !bytes.Contains(out, []byte(`9007199254740993`)) || h.Get("Etag") != "" || h.Get("Content-Length") != "" {
		t.Fatal("lost unknown integer or validators")
	}
	var value map[string]any
	json.Unmarshal(out, &value)
	surfaces := value["model_selector_config"].([]any)
	rows := nativeObject(surfaces[0])["models"].([]any)
	row := nativeObject(rows[1])
	if row["badge"] != nil || row["version"] != nil || row["supports_tools"] != true || row["context_window"] != float64(12345) {
		t.Fatalf("bad template %v", row)
	}
	if len(nativeObject(surfaces[1])["models"].([]any)) != 1 {
		t.Fatal("remote session changed")
	}
	again, _ := p.RewriteResponse(d, 200, h, out, []NativeCompatibilityModel{{ID: "claude-routed", Name: "Routed"}})
	if !bytes.Equal(out, again) {
		t.Fatal("duplicate alias")
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	gz.Write(raw)
	gz.Close()
	h.Set("Content-Encoding", "gzip")
	got, _ := p.RewriteResponse(d, 200, h, compressed.Bytes(), []NativeCompatibilityModel{{ID: "claude-routed", Name: "Routed"}})
	if bytes.Equal(got, compressed.Bytes()) || h.Get("Content-Encoding") != "" {
		t.Fatal("gzip rewrite")
	}
	for _, body := range [][]byte{[]byte(`{"unknown":true}`), []byte(`not json`), append(raw, []byte(` {}`)...)} {
		h := http.Header{"Content-Type": {"application/json"}, "Etag": {"keep"}}
		out, _ := p.RewriteResponse(d, 200, h, body, []NativeCompatibilityModel{{ID: "a", Name: "A"}})
		if !bytes.Equal(out, body) || h.Get("Etag") != "keep" {
			t.Fatal("failed transform did not preserve bytes")
		}
	}
}

const nativeUsageFixture = `{"account_id":"account","user_id":"user","plan_type":"plus","rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"used_percent":100}},"spend_control":{"reached":false},"credits":{"has_credits":false,"unlimited":false,"balance":"0"}}`

func nativeTrialFixture(t *testing.T) (NativeCompatibilityPolicy, *time.Time, *bool) {
	t.Helper()
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	valid := true
	p := nativePolicyFixture(t, NativeCompatibilityCodexAccountUITrial)
	p, err := p.WithAccountUITrial(NativeCompatibilityAccount{ID: "account", UserID: "user", Plan: "plus", Structure: "personal"}, now.Add(time.Minute), func() time.Time { return now }, func() bool { return valid })
	if err != nil {
		t.Fatal(err)
	}
	return p, &now, &valid
}
func TestNativeCompatibilityUsageGuards(t *testing.T) {
	p, now, valid := nativeTrialFixture(t)
	d := NativeCompatibilityDecision{Action: NativeCompatibilityUpstream, ResponseKind: NativeCompatibilityResponseUsage}
	headers := func() http.Header { return http.Header{"Content-Type": {"application/json"}} }
	raw := []byte(nativeUsageFixture)
	out, _ := p.RewriteResponse(d, 200, headers(), raw, nil)
	if !bytes.Contains(out, []byte(`"allowed":true`)) || !bytes.Contains(out, []byte(`"used_percent":100`)) || !bytes.Contains(out, []byte(`"balance":"0"`)) {
		t.Fatalf("bad bounded UI rewrite %s", out)
	}
	for _, pair := range [][2]string{{`"user_id":"user"`, `"user_id":"other"`}, {`"reached":false`, `"reached":true`}, {`"has_credits":false`, `"has_credits":true`}, {`"unlimited":false`, `"unlimited":null`}, {`"spend_control":{"reached":false},`, ``}, {`"balance":"0"`, `"balance":"0","overage_limit_reached":true`}} {
		body := []byte(strings.Replace(nativeUsageFixture, pair[0], pair[1], 1))
		got, _ := p.RewriteResponse(d, 200, headers(), body, nil)
		if !bytes.Equal(body, got) {
			t.Fatalf("changed protected state %s", body)
		}
	}
	observe := nativePolicyFixture(t, NativeCompatibilityCodexObserve)
	got, _ := observe.RewriteResponse(d, 200, headers(), raw, nil)
	if !bytes.Equal(raw, got) {
		t.Fatal("observe changed")
	}
	*valid = false
	got, _ = p.RewriteResponse(d, 200, headers(), raw, nil)
	if !bytes.Equal(raw, got) {
		t.Fatal("revoked consent changed")
	}
	*valid = true
	*now = now.Add(2 * time.Minute)
	got, _ = p.RewriteResponse(d, 200, headers(), raw, nil)
	if !bytes.Equal(raw, got) {
		t.Fatal("expired trial changed")
	}
}
func TestNativeCompatibilityUsageStream(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		t.Run(strings.ReplaceAll(newline, "\r", "CR"), func(t *testing.T) {
			p, _, _ := nativeTrialFixture(t)
			d := NativeCompatibilityDecision{Action: NativeCompatibilityUpstream, ResponseKind: NativeCompatibilityResponseUsageStream}
			h := http.Header{"Content-Type": {"text/event-stream"}}
			snapshot := `{"version":1,"stream_id":"stream","sequence":1,"usage":` + nativeUsageFixture + `}`
			raw := "id: 42" + newline + "event: usage.snapshot" + newline + "data: " + snapshot + newline + newline + "event: other" + newline + "data: untouched" + newline + newline
			var out bytes.Buffer
			if err := p.RewriteUsageStream(d, 200, h, &nativeByteReader{[]byte(raw)}, &out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), `"allowed":true`) || !strings.Contains(out.String(), "id: 42"+newline) || !strings.HasSuffix(out.String(), "event: other"+newline+"data: untouched"+newline+newline) {
				t.Fatalf("bad stream %q", out.String())
			}
			// An oversized event must stay untouched and must not disable later records.
			large := strings.Repeat("x", nativeUsageMaxBytes+1) + newline + newline
			out.Reset()
			p.RewriteUsageStream(d, 200, h, strings.NewReader(large+raw), &out)
			if !strings.HasPrefix(out.String(), large) || !strings.Contains(out.String(), `"allowed":true`) {
				t.Fatal("oversized event framing")
			}
		})
	}
}

type nativeByteReader struct{ b []byte }

func (r *nativeByteReader) Read(b []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	b[0] = r.b[0]
	r.b = r.b[1:]
	return 1, nil
}

func TestNativeCompatibilityCLICatalogBodies(t *testing.T) {
	p := nativePolicyFixture(t, NativeCompatibilityClaudeFirstParty)
	models := []NativeCompatibilityModel{{ID: "claude-routed", Name: "Routed", Description: "Gateway model"}}
	for _, tt := range []struct {
		kind   NativeCompatibilityResponseKind
		body   string
		needle string
	}{
		{NativeCompatibilityResponseCLIBootstrap, `{"additional_model_options":[{"model":"native","name":"Native"}],"unrelated":true}`, `"description":"Gateway model"`},
		{NativeCompatibilityResponseCLISelector, `{"model_selector_config":[{"id":"cc","models":[{"id":"claude-native","notice":"old","selection_notice":"old"}]},{"id":"ccd","models":[{"id":"claude-native"}]}]}`, `"id":"claude-routed"`},
	} {
		h := http.Header{"Content-Type": {"application/json"}}
		d := NativeCompatibilityDecision{Action: NativeCompatibilityUpstream, ResponseKind: tt.kind}
		got, err := p.RewriteResponse(d, 200, h, []byte(tt.body), models)
		if err != nil || !bytes.Contains(got, []byte(tt.needle)) {
			t.Fatalf("CLI transform %s %v", got, err)
		}
	}
}
func TestNativeCompatibilityRewriteBudgetAndTrialAdmission(t *testing.T) {
	p := nativePolicyFixture(t, NativeCompatibilityClaudePicker)
	d := NativeCompatibilityDecision{Action: NativeCompatibilityUpstream, ResponseKind: NativeCompatibilityResponsePicker}
	raw := []byte(`{"model_selector_config":[{"id":"ccd","models":[{"id":"claude-native"}]}]}`)
	models := make([]NativeCompatibilityModel, 4097)
	for i := range models {
		models[i] = NativeCompatibilityModel{ID: "a", Name: "A"}
	}
	h := http.Header{"Content-Type": {"application/json"}, "Etag": {"preserve"}}
	got, _ := p.RewriteResponse(d, 200, h, raw, models)
	if !bytes.Equal(raw, got) || h.Get("Etag") != "preserve" {
		t.Fatal("oversized catalog was partially rewritten")
	}
	trial := nativePolicyFixture(t, NativeCompatibilityCodexAccountUITrial)
	now := time.Now()
	account := NativeCompatibilityAccount{ID: "account", UserID: "user", Plan: "plus", Structure: "personal"}
	for _, until := range []time.Time{now, now.Add(3*time.Minute + time.Nanosecond)} {
		if _, err := trial.WithAccountUITrial(account, until, func() time.Time { return now }, func() bool { return true }); err == nil {
			t.Fatal("unbounded trial")
		}
	}
	if _, err := trial.WithAccountUITrial(NativeCompatibilityAccount{}, now.Add(time.Minute), func() time.Time { return now }, func() bool { return true }); err == nil {
		t.Fatal("missing account")
	}
	if _, err := trial.WithAccountUITrial(account, now.Add(time.Minute), func() time.Time { return now }, func() bool { return false }); err == nil {
		t.Fatal("missing consent")
	}
}
func TestNativeCompatibilityStreamRevocationAndIncompleteRecord(t *testing.T) {
	p, _, valid := nativeTrialFixture(t)
	record := "event: usage.snapshot\ndata: {\"version\":1,\"stream_id\":\"s\",\"sequence\":1,\"usage\":" + nativeUsageFixture + "}\n\n"
	d := NativeCompatibilityDecision{Action: NativeCompatibilityUpstream, ResponseKind: NativeCompatibilityResponseUsageStream}
	writer := &nativeRevokingWriter{valid: valid}
	if err := p.RewriteUsageStream(d, 200, http.Header{"Content-Type": {"text/event-stream"}}, strings.NewReader(record+record), writer); err != nil {
		t.Fatal(err)
	}
	if strings.Count(writer.String(), `"allowed":true`) != 1 || !strings.HasSuffix(writer.String(), record) {
		t.Fatal("consent revocation not fenced")
	}
	*valid = true
	var out bytes.Buffer
	partial := strings.TrimSuffix(record, "\n")
	p.RewriteUsageStream(d, 200, http.Header{"Content-Type": {"text/event-stream"}}, strings.NewReader(partial), &out)
	if out.String() != partial {
		t.Fatal("incomplete event modified")
	}
}

type nativeRevokingWriter struct {
	bytes.Buffer
	valid *bool
}

func (w *nativeRevokingWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	*w.valid = false
	return n, err
}
