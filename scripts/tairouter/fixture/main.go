// SPDX-License-Identifier: Apache-2.0
// Fixture identities and responses are synthetic. Never deploy this server publicly.
package main

import (
	"bufio"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type grant struct{ Subject, Nonce, Challenge string }
type fixture struct {
	key    *rsa.PrivateKey
	issuer string
	mu     sync.Mutex
	codes  map[string]grant
	mail   int
}

func encode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (f *fixture) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"fixture": true}) })
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"issuer": f.issuer, "authorization_endpoint": f.issuer + "/authorize", "token_endpoint": f.issuer + "/token", "jwks_uri": f.issuer + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"}, "code_challenge_methods_supported": []string{"S256"}})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "fixture", "use": "sig", "alg": "RS256", "n": encode(f.key.N.Bytes()), "e": encode(big.NewInt(int64(f.key.E)).Bytes())}}})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redirect, err := url.Parse(q.Get("redirect_uri"))
		if err != nil || redirect.Scheme != "http" || redirect.Host != "127.0.0.1:8080" || redirect.Path != "/auth/callback" || q.Get("client_id") != "tairouter-fixture" || q.Get("response_type") != "code" {
			http.Error(w, "invalid fixture authorization request", 400)
			return
		}
		subject := q.Get("login_hint")
		if subject == "" {
			subject = "alice"
		}
		if subject != "alice" && subject != "bob" && subject != "unverified" {
			http.Error(w, "unknown fixture identity", 400)
			return
		}
		if q.Get("code_challenge") != "" && q.Get("code_challenge_method") != "S256" {
			http.Error(w, "S256 required", 400)
			return
		}
		raw := make([]byte, 32)
		_, _ = rand.Read(raw)
		code := encode(raw)
		f.mu.Lock()
		f.codes[code] = grant{subject, q.Get("nonce"), q.Get("code_challenge")}
		f.mu.Unlock()
		target := redirect.Query()
		target.Set("code", code)
		target.Set("state", q.Get("state"))
		redirect.RawQuery = target.Encode()
		http.Redirect(w, r, redirect.String(), 302)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if r.ParseForm() != nil {
			http.Error(w, "invalid form", 400)
			return
		}
		f.mu.Lock()
		g, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		if !ok || r.Form.Get("grant_type") != "authorization_code" {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		if g.Challenge != "" {
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if encode(sum[:]) != g.Challenge {
				writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
		}
		now := time.Now().Unix()
		claims, _ := json.Marshal(map[string]any{"iss": f.issuer, "sub": g.Subject, "aud": "tairouter-fixture", "exp": now + 600, "iat": now, "nonce": g.Nonce, "email": g.Subject + "@example.invalid", "email_verified": g.Subject != "unverified"})
		header := encode([]byte(`{"alg":"RS256","kid":"fixture","typ":"JWT"}`))
		payload := header + "." + encode(claims)
		sum := sha256.Sum256([]byte(payload))
		signature, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
		writeJSON(w, 200, map[string]any{"access_token": "synthetic-fixture-access", "token_type": "Bearer", "expires_in": 600, "id_token": payload + "." + encode(signature)})
	})
	mux.HandleFunc("GET /fixture/mail", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		count := f.mail
		f.mu.Unlock()
		writeJSON(w, 200, map[string]int{"received": count})
	})
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"object": "list", "data": []any{map[string]string{"id": "fixture-model", "object": "model", "owned_by": "fixture"}}})
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool   `json:"stream"`
			Model  string `json:"model"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		switch body.Model {
		case "fixture-429":
			w.Header().Set("Retry-After", "1")
			writeJSON(w, 429, map[string]any{"error": map[string]string{"type": "rate_limit_error", "message": "synthetic limit"}})
			return
		case "fixture-503":
			writeJSON(w, 503, map[string]string{"error": "synthetic unavailable"})
			return
		}
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, part := range []string{`{"id":"fixture","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"fixture response"},"finish_reason":null}]}`, `{"id":"fixture","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`, "[DONE]"} {
				fmt.Fprintf(w, "data: %s\n\n", part)
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
			}
			return
		}
		writeJSON(w, 200, map[string]any{"id": "fixture", "object": "chat.completion", "model": "fixture-model", "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": "fixture response"}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 8, "completion_tokens": 2, "total_tokens": 10}})
	})
	return mux
}
func (f *fixture) smtp(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(conn)
	fmt.Fprint(conn, "220 fixture ESMTP\r\n")
	data := false
	size := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		size += len(line)
		if size > 1<<20 {
			return
		}
		if data {
			if line == ".\r\n" {
				data = false
				f.mu.Lock()
				f.mail++
				f.mu.Unlock()
				fmt.Fprint(conn, "250 accepted\r\n")
			}
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			fmt.Fprint(conn, "250 fixture\r\n")
		case strings.HasPrefix(upper, "DATA"):
			data = true
			fmt.Fprint(conn, "354 end with dot\r\n")
		case strings.HasPrefix(upper, "QUIT"):
			fmt.Fprint(conn, "221 bye\r\n")
			return
		default:
			fmt.Fprint(conn, "250 OK\r\n")
		}
	}
}
func main() {
	listen := flag.String("listen", "127.0.0.1:18090", "HTTP listener")
	smtp := flag.String("smtp", "127.0.0.1:1025", "SMTP receiver")
	issuer := flag.String("issuer", "http://127.0.0.1:18090", "OIDC issuer")
	flag.Parse()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	f := &fixture{key: key, issuer: *issuer, codes: map[string]grant{}}
	ln, err := net.Listen("tcp", *smtp)
	if err != nil {
		panic(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.smtp(conn)
		}
	}()
	slog.Info("synthetic fixtures started", "http", *listen, "smtp", *smtp)
	server := http.Server{Addr: *listen, Handler: f.handler(), ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}
