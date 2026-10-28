// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeControlSquatterCannotIssueLaterStop(t *testing.T) {
	c, req, _ := controllerFixture(t)
	req.Mode = NativeCompatibilityClaudeFirstParty
	c.options.Models = func(context.Context, Profile, string) ([]ClientModel, error) { return nil, nil }
	var mu sync.Mutex
	var captured []*http.Request
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		captured = append(captured, r.Clone(context.Background()))
		mu.Unlock()
		http.Error(w, "foreign", 403)
	}))
	req.Port = foreign.Listener.Addr().(*net.TCPAddr).Port
	if _, e := c.Enable(context.Background(), req, controllerConsent(t, c, req)); e != nil {
		foreign.Close()
		t.Fatal(e)
	}
	if e := c.Stop(context.Background(), req.Profile.Name); e == nil {
		foreign.Close()
		t.Fatal("foreign stop accepted")
	}
	foreign.Close()
	mu.Lock()
	requests := append([]*http.Request(nil), captured...)
	mu.Unlock()
	if len(requests) == 0 {
		t.Fatal("no foreign probe")
	}
	for _, r := range requests {
		if r.Method != "GET" || r.URL.Path != nativeControlChallengePath || r.Header.Get("X-Tairouter-Proof") != "" {
			t.Fatal("actionable proof sent before listener authentication")
		}
	}
	ready := make(chan struct{})
	c.options.Run = func(ctx context.Context, cfg NativeTLSConfig) error {
		original := cfg.OnReady
		cfg.OnReady = func() { original(); close(ready) }
		return RunNativeTLS(ctx, cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Serve(ctx, req.Profile.Name) }()
	select {
	case <-ready:
	case e := <-done:
		t.Fatal(e)
	case <-time.After(3 * time.Second):
		t.Fatal("start timeout")
	}
	defer func() { cancel(); <-done }()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	for _, attack := range requests {
		replay, _ := http.NewRequest("POST", "http://"+nativeRuntimeAddress(req)+"/_tairouter/native/stop", nil)
		replay.Header = attack.Header.Clone()
		res, e := client.Do(replay)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode == 200 {
			t.Fatal("squatter capture stopped new listener")
		}
	}
	if _, e := c.Ready(context.Background(), req.Profile.Name); e != nil {
		t.Fatal(e)
	}
	if e := c.Stop(context.Background(), req.Profile.Name); e != nil {
		t.Fatal(e)
	}
}

// This subprocess runs only a synthetic runtime. A fresh OS process guarantees
// the replay regression cannot pass because of a prior process's nonce cache.
func TestNativeControlRestartProcess(t *testing.T) {
	if os.Getenv("TAIROUTER_TEST_NATIVE_CHILD") != "1" {
		return
	}
	ca, e := EnsureNativeCAWithOptions(os.Getenv("TAIROUTER_TEST_NATIVE_ROOT"), NativeCAOptions{Platform: "darwin", DNSNames: []string{"api.anthropic.com"}, Protection: controllerKeyProtection{}, FileProtection: NativeProtector{}})
	if e != nil {
		t.Fatal(e)
	}
	policy, e := NewNativeCompatibilityPolicy(NativeCompatibilityClaudeFirstParty, "darwin", NativeCompatibilityClaudeBuild)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	e = RunNativeTLS(ctx, NativeTLSConfig{Address: os.Getenv("TAIROUTER_TEST_NATIVE_ADDRESS"), Token: strings.Repeat("f", 64), CA: ca, Policy: policy, Gateway: "https://router.test", Key: "synthetic-router-key"})
	if e != nil {
		t.Fatal(e)
	}
}
func TestNativeControlOldGenerationRejectedAfterProcessRestart(t *testing.T) {
	root := realTemp(t)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := ln.Addr().String()
	ln.Close()
	token := strings.Repeat("f", 64)
	start := func() (*exec.Cmd, chan error, *bytes.Buffer) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestNativeControlRestartProcess$")
		cmd.Env = append(os.Environ(), "TAIROUTER_TEST_NATIVE_CHILD=1", "TAIROUTER_TEST_NATIVE_ROOT="+root, "TAIROUTER_TEST_NATIVE_ADDRESS="+address)
		output := &bytes.Buffer{}
		cmd.Stdout = output
		cmd.Stderr = output
		if e := cmd.Start(); e != nil {
			t.Fatal(e)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		until := time.Now().Add(5 * time.Second)
		for time.Now().Before(until) {
			if _, e := nativeControlRequest(context.Background(), address, token, "GET", "/_tairouter/native/health"); e == nil {
				return cmd, done, output
			}
			time.Sleep(10 * time.Millisecond)
		}
		cmd.Process.Kill()
		<-done
		t.Fatalf("child did not bind: %s", output.String())
		return nil, nil, nil
	}
	stop := func(cmd *exec.Cmd, done chan error, output *bytes.Buffer) {
		t.Helper()
		if _, e := nativeControlRequest(context.Background(), address, token, "POST", "/_tairouter/native/stop"); e != nil {
			cmd.Process.Kill()
			<-done
			t.Fatal(e)
		}
		select {
		case e := <-done:
			if e != nil {
				t.Fatalf("child failed: %v %s", e, output.String())
			}
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
			<-done
			t.Fatal("child did not stop")
		}
	}
	cmd, done, output := start()
	defer cmd.Process.Kill()
	// Capture a legitimate action proof but withhold its first delivery entirely.
	// This is stronger than replaying a nonce already accepted by the old process.
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	challenge, e := nativeControlNewRequest(context.Background(), address, "GET", nativeControlChallengePath)
	if e != nil {
		t.Fatal(e)
	}
	body, e := nativeControlExchange(client, challenge, token, "tairouter/native/challenge/v2")
	if e != nil {
		t.Fatal(e)
	}
	var binding struct {
		Generation string `json:"generation"`
	}
	if json.Unmarshal(body, &binding) != nil {
		t.Fatal("invalid binding")
	}
	attack, e := nativeControlNewRequest(context.Background(), address, "POST", "/_tairouter/native/stop")
	if e != nil {
		t.Fatal(e)
	}
	attack.Header.Set("X-Tairouter-Generation", binding.Generation)
	attack.Header.Set("X-Tairouter-Proof", nativeControlProof(attack, token, "tairouter/native/request/v2"))
	stop(cmd, done, output)
	cmd, done, output = start()
	defer cmd.Process.Kill()
	res, e := client.Do(attack)
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("old generation accepted after restart: %d", res.StatusCode)
	}
	if _, e = nativeControlRequest(context.Background(), address, token, "GET", "/_tairouter/native/health"); e != nil {
		t.Fatal("old request stopped new runtime", e)
	}
	stop(cmd, done, output)
}
