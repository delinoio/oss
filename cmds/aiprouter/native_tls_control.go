// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const nativeControlLimit = 4096
const nativeControlWindow = 30 * time.Second

const nativeControlChallengePath = "/_aiprouter/native/challenge"

// Each bound listener owns a fresh generation and replay set. Nothing survives
// into a replacement listener, even when its durable profile token is unchanged.
type nativeControlServer struct {
	token, generation string
	mu                sync.Mutex
	seen              map[string]time.Time
}

func newNativeControlServer(token string) (*nativeControlServer, error) {
	generation := make([]byte, 32)
	if _, err := rand.Read(generation); err != nil {
		return nil, err
	}
	return &nativeControlServer{token: token, generation: hex.EncodeToString(generation), seen: make(map[string]time.Time)}, nil
}

// Challenge authenticates the listener to a fresh client nonce. It authorizes
// no action and receives no actionable request proof or reusable credential.
func (s *nativeControlServer) Challenge(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != nativeControlChallengePath {
		return false
	}
	if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Origin") != "" || !nativeControlFresh(r) || r.Header.Get("X-Aiprouter-Generation") != "" || r.Header.Get("X-Aiprouter-Proof") != "" {
		http.Error(w, "invalid native challenge", 403)
		return true
	}
	body, _ := json.Marshal(map[string]string{"generation": s.generation})
	nativeControlRespondDomain(w, r, s.token, "aiprouter/native/challenge/v2", http.StatusOK, body)
	return true
}
func nativeControlFresh(r *http.Request) bool {
	b, e := hex.DecodeString(r.Header.Get("X-Aiprouter-Nonce"))
	if e != nil || len(b) != 32 || r.ContentLength != 0 {
		return false
	}
	stamp, e := strconv.ParseInt(r.Header.Get("X-Aiprouter-Time"), 10, 64)
	if e != nil {
		return false
	}
	delta := time.Since(time.Unix(stamp, 0))
	return delta <= nativeControlWindow && delta >= -nativeControlWindow
}
func nativeControlMAC(token, domain string, fields ...string) string {
	b, _ := json.Marshal(append([]string{domain}, fields...))
	m := hmac.New(sha256.New, []byte(token))
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil))
}

// NativeProxyPassword cannot be used to authenticate runtime control requests.
func NativeProxyPassword(token string) string {
	return nativeControlMAC(token, "aiprouter/native/proxy/v1")
}
func nativeControlProof(r *http.Request, token, domain string, extra ...string) string {
	return nativeControlMAC(token, domain, append([]string{r.Header.Get("X-Aiprouter-Generation"), r.Header.Get("X-Aiprouter-Nonce"), r.Header.Get("X-Aiprouter-Time"), r.Method, r.URL.RequestURI()}, extra...)...)
}
func (s *nativeControlServer) Authorize(r *http.Request) bool {
	if !nativeControlFresh(r) || !hmac.Equal([]byte(s.generation), []byte(r.Header.Get("X-Aiprouter-Generation"))) {
		return false
	}
	want := nativeControlProof(r, s.token, "aiprouter/native/request/v2")
	if !hmac.Equal([]byte(want), []byte(r.Header.Get("X-Aiprouter-Proof"))) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.seen {
		if now.After(v) {
			delete(s.seen, k)
		}
	}
	nonce := r.Header.Get("X-Aiprouter-Nonce")
	if _, ok := s.seen[nonce]; ok || len(s.seen) >= 8192 {
		return false
	}
	s.seen[nonce] = now.Add(2 * nativeControlWindow)
	return true
}
func nativeControlRespond(w http.ResponseWriter, r *http.Request, token string, status int, body []byte) {
	nativeControlRespondDomain(w, r, token, "aiprouter/native/response/v2", status, body)
}
func nativeControlRespondDomain(w http.ResponseWriter, r *http.Request, token, domain string, status int, body []byte) {
	if len(body) > nativeControlLimit {
		status = http.StatusInternalServerError
		body = []byte(`{"error":"control response too large"}`)
	}
	w.Header().Set("X-Aiprouter-Proof", nativeControlProof(r, token, domain, strconv.Itoa(status), string(body)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
func nativeControlRequest(ctx context.Context, address, token, method, path string) ([]byte, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return nil, errors.New("invalid native control address")
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("native control redirect refused") }}
	defer client.CloseIdleConnections()
	challenge, err := nativeControlNewRequest(ctx, address, http.MethodGet, nativeControlChallengePath)
	if err != nil {
		return nil, err
	}
	body, err := nativeControlExchange(client, challenge, token, "aiprouter/native/challenge/v2")
	if err != nil {
		return nil, err
	}
	var binding struct {
		Generation string `json:"generation"`
	}
	if json.Unmarshal(body, &binding) != nil {
		return nil, errors.New("invalid native listener generation")
	}
	generation, err := hex.DecodeString(binding.Generation)
	if err != nil || len(generation) != 32 {
		return nil, errors.New("invalid native listener generation")
	}
	req, err := nativeControlNewRequest(ctx, address, method, path)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Aiprouter-Generation", binding.Generation)
	req.Header.Set("X-Aiprouter-Proof", nativeControlProof(req, token, "aiprouter/native/request/v2"))
	return nativeControlExchange(client, req, token, "aiprouter/native/response/v2")
}
func nativeControlNewRequest(ctx context.Context, address, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, nil)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	req.Header.Set("X-Aiprouter-Nonce", hex.EncodeToString(nonce))
	req.Header.Set("X-Aiprouter-Time", strconv.FormatInt(time.Now().Unix(), 10))
	return req, nil
}
func nativeControlExchange(client *http.Client, req *http.Request, token, domain string) ([]byte, error) {
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, nativeControlLimit+1))
	if err != nil {
		return nil, err
	}
	want := nativeControlProof(req, token, domain, strconv.Itoa(res.StatusCode), string(body))
	if len(body) > nativeControlLimit || !hmac.Equal([]byte(want), []byte(res.Header.Get("X-Aiprouter-Proof"))) || res.StatusCode != http.StatusOK {
		return nil, errors.New("native control response authentication failed")
	}
	return body, nil
}
