// SPDX-License-Identifier: Apache-2.0
// Protocol mappings adapted from the sources identified in LICENSE-MIT.
package devin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

const Wire inference.Wire = "devin"
const chatPath = "/exa.api_server_pb.ApiServerService/GetChatMessage"
const searchPath = "/exa.api_server_pb.ApiServerService/GetWebSearchResults"

// Adapter is stateless and safe for concurrent requests. It never sends requests.
type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ inference.Adapter = (*Adapter)(nil)
var jwtPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*$`)

func token(s string) string {
	v := strings.TrimSpace(s)
	if !strings.Contains(v, "$") && jwtPattern.MatchString(v) {
		return "devin-session-token$" + v
	}
	return s
}

func baseURL(raw string) (string, error) {
	if raw == "" {
		raw = "https://server.codeium.com"
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", inference.ErrDestination
	}
	host := strings.ToLower(u.Hostname())
	allowed := host == "server.codeium.com" || host == "server-staging.codeium.com" || host == "server-beta.codeium.com" || host == "windsurf.com" || strings.HasSuffix(host, ".windsurf.com") || host == "windsurf.fedstart.com"
	if !allowed || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return "", inference.ErrDestination
	}
	path := strings.TrimRight(u.Path, "/")
	if path != "" && !validPath(path) {
		return "", inference.ErrDestination
	}
	return "https://" + host + path, nil
}

func validPath(path string) bool {
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, c := range segment {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
				return false
			}
		}
	}
	return strings.HasPrefix(path, "/")
}

func uuid() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func metadata(secret string, chat bool) ([]byte, error) {
	name, version, osName := "windsurf", "3.9.19", runtime.GOOS
	if chat {
		name, version, osName = "chisel", "2026.8.18", "windows"
	}
	var b []byte
	for _, f := range []struct {
		tag   int
		value string
	}{{1, name}, {2, version}, {3, token(secret)}, {4, "en"}, {5, osName}, {7, version}, {12, name}} {
		b = textField(b, f.tag, f.value)
	}
	if chat {
		fingerprint := make([]byte, 366)
		if _, err := rand.Read(fingerprint); err != nil {
			return nil, err
		}
		return textField(b, 31, hex.EncodeToString(fingerprint)), nil
	}
	session, err := uuid()
	if err != nil {
		return nil, err
	}
	trigger, err := uuid()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	b = number(b, 9, uint64(now.UnixMilli()))
	b = textField(b, 10, session)
	stamp := number(nil, 1, uint64(now.Unix()))
	stamp = number(stamp, 2, uint64(now.Nanosecond()))
	b = blob(b, 16, stamp)
	b = textField(b, 25, trigger)
	b = textField(b, 26, "Unset")
	return textField(b, 28, "windsurf"), nil
}

func (a *Adapter) Build(ctx context.Context, in inference.AdapterInput) (inference.AdapterRequest, error) {
	if s := attempt(ctx); s != nil {
		s.mu.Lock()
		err := s.bind(in)
		options := s.options
		s.mu.Unlock()
		if err != nil {
			return inference.AdapterRequest{}, err
		}
		request, state, err := a.BuildPrepared(ctx, in, options)
		if err != nil {
			return request, err
		}
		s.mu.Lock()
		s.response = state
		s.mu.Unlock()
		return request, nil
	}
	return a.build(ctx, in, Options{})
}

func (a *Adapter) build(ctx context.Context, in inference.AdapterInput, options Options) (inference.AdapterRequest, error) {
	var out inference.AdapterRequest
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if in.Operation != inference.Generate && in.Operation != inference.Search {
		return out, inference.ErrUnsupported
	}
	base, err := baseURL(in.Target.BaseURL)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(in.Credential.Secret) == "" {
		return out, inference.ErrNoCredential
	}
	if len(in.Body) > maxFrame {
		return out, &inference.Error{Status: 413, Code: "devin_request_too_large"}
	}
	var input object
	if json.Unmarshal(in.Body, &input) != nil || input == nil {
		return out, badRequest()
	}
	meta, err := metadata(in.Credential.Secret, in.Operation == inference.Generate)
	if err != nil {
		return out, err
	}
	out.Headers = http.Header{"Connect-Protocol-Version": {"1"}}
	out.ResponseWire = inference.Chat
	if in.Operation == inference.Search {
		query, err := searchQuery(input)
		if err != nil {
			return out, err
		}
		out.URL = base + searchPath
		out.Headers.Set("Content-Type", "application/proto")
		out.Body = number(textField(blob(nil, 1, meta), 2, query), 3, 5)
		return out, nil
	}
	b, err := buildChat(input, in, meta, options)
	if err != nil {
		return out, err
	}
	if len(b) > maxFrame {
		return out, &inference.Error{Status: 413, Code: "devin_request_too_large"}
	}
	out.URL = base + chatPath
	out.Headers.Set("Content-Type", "application/connect+proto")
	out.Headers.Set("Connect-Accept-Encoding", "gzip")
	out.Headers.Set("Authorization", "Basic "+in.Credential.Secret+"-"+in.Credential.Secret)
	out.Headers.Set("User-Agent", "connect-es/2.0.0")
	out.Headers.Set("Accept", "*/*")
	out.Body = frame(b)
	return out, nil
}

func badRequest() error { return &inference.Error{Status: 400, Code: "invalid_devin_request"} }

func (a *Adapter) Decode(ctx context.Context, resp *http.Response) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, errWire
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	if resp.Request != nil && resp.Request.URL != nil && strings.HasSuffix(resp.Request.URL.Path, searchPath) {
		stop := context.AfterFunc(ctx, func() { _ = resp.Body.Close() })
		defer stop()
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, errWire
		}
		if len(b) > 64<<10 {
			return nil, errLimit
		}
		result, err := searchResponse(b)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		out := cloneResponse(resp, "application/json")
		out.Body = io.NopCloser(bytes.NewReader(body))
		out.ContentLength = int64(len(body))
		return out, nil
	}
	out := cloneResponse(resp, "text/event-stream")
	out.Body = newStream(ctx, resp.Body)
	return out, nil
}

func cloneResponse(resp *http.Response, contentType string) *http.Response {
	out := *resp
	out.Header = resp.Header.Clone()
	if out.Header == nil {
		out.Header = make(http.Header)
	}
	out.Header.Set("Content-Type", contentType)
	for _, key := range []string{"Content-Length", "Content-Encoding", "Etag", "Content-Md5", "Digest"} {
		out.Header.Del(key)
	}
	out.ContentLength = -1
	out.TransferEncoding = nil
	return &out
}
