// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type runtimeFixture struct {
	plan    NativeRuntimePlan
	status  NativeRuntimeStatus
	ready   NativeRuntimeReady
	events  []string
	consent NativeRuntimeConsent
}

func (r *runtimeFixture) Plan(context.Context, NativeRuntimeRequest) (NativeRuntimePlan, error) {
	r.events = append(r.events, "plan")
	return r.plan, nil
}
func (r *runtimeFixture) Enable(_ context.Context, _ NativeRuntimeRequest, c NativeRuntimeConsent) (NativeRuntimeStatus, error) {
	r.events = append(r.events, "enable")
	r.consent = c
	return r.status, nil
}
func (r *runtimeFixture) Status(context.Context, string) (NativeRuntimeStatus, error) {
	return r.status, nil
}
func (r *runtimeFixture) Ready(context.Context, string) (NativeRuntimeReady, error) {
	return r.ready, nil
}
func (r *runtimeFixture) Stop(context.Context, string) error {
	r.events = append(r.events, "stop")
	return nil
}
func (r *runtimeFixture) Disable(context.Context, string) error {
	r.events = append(r.events, "disable")
	return nil
}
func (r *runtimeFixture) Serve(context.Context, string) error {
	r.events = append(r.events, "serve")
	return nil
}

type promptSequence struct {
	answers []bool
	count   int
}

func (p *promptSequence) Confirm(string) bool {
	index := p.count
	p.count++
	return index < len(p.answers) && p.answers[index]
}

func TestNativeEnableRequiresSeparateConsent(t *testing.T) {
	for _, answers := range [][]bool{{false}, {true, false}, {true, true, false}, {true, true, true}} {
		p := testProfile()
		s := emptyState()
		s.Profiles[p.Name] = p
		r := &runtimeFixture{plan: NativeRuntimePlan{Fingerprint: "reviewed-fingerprint", PlanDigest: "reviewed-plan", RequiresTrust: true, RequiresApplication: true, RequiresAccountTrial: true}, status: NativeRuntimeStatus{Trusted: true}}
		var out bytes.Buffer
		prompt := &promptSequence{answers: answers}
		a := Application{Native: r, Prompt: prompt, Out: &out}
		err := a.runNative(context.Background(), &s, p.Name, "enable", NativeCompatibilityCodexAccountUITrial, NativeCompatibilityCodexBuild, DefaultNativePort)
		accepted := len(answers) == 3 && answers[2]
		if accepted {
			if err != nil || !r.consent.Trust || !r.consent.Application || !r.consent.AccountTrial || r.consent.PlanFingerprint != "reviewed-fingerprint" || r.consent.PlanDigest != "reviewed-plan" {
				t.Fatalf("consent not propagated: %v", err)
			}
		} else if err == nil || len(r.events) != 1 {
			t.Fatal("enabled without all independent consents")
		}
	}
}
func TestNativeStatusNeverFormatsAuthenticatedProxy(t *testing.T) {
	r := &runtimeFixture{status: NativeRuntimeStatus{Enabled: true, ProxyURL: "http://secret:token@127.0.0.1:17844"}}
	var out bytes.Buffer
	a := Application{Native: r, Out: &out}
	s := emptyState()
	if err := a.runNative(context.Background(), &s, "test", "status", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "token") || strings.Contains(out.String(), "secret") {
		t.Fatal("proxy credential leaked")
	}
}
func TestNativeDisableRetainsConflictingApplicationBackup(t *testing.T) {
	d, s, paths := restoreFixture(t, true)
	record := s.Records["cline"]
	record.RuntimeMode = NativeCompatibilityClaudePicker
	s.Records["cline"] = record
	if err := d.write(paths[0], []byte("foreign edit")); err != nil {
		t.Fatal(err)
	}
	r := &runtimeFixture{}
	var out bytes.Buffer
	a := Application{Disk: d, Native: r, Prompt: answer(true), Out: &out}
	err := a.runNative(context.Background(), &s, "test", "disable", "", "", 0)
	if err == nil || len(s.Records) == 0 {
		t.Fatal("conflict silently discarded")
	}
	if strings.Join(r.events, ",") != "stop,disable" {
		t.Fatal("cleanup failed to stop listener/remove trust after conflict")
	}
}
func TestNativeConfigurationRefusesForgedReady(t *testing.T) {
	p := testProfile()
	a := Application{Native: &runtimeFixture{ready: NativeRuntimeReady{Profile: p.Name, Mode: NativeCompatibilityClaudeFirstParty}}}
	if _, err := a.planConfiguration(context.Background(), emptyState(), "claude", p, "key", nil, NativeCompatibilityClaudeFirstParty); err == nil {
		t.Fatal("forged readiness accepted")
	}
	s := emptyState()
	s.Records["claude"] = Record{RuntimeMode: NativeCompatibilityClaudeFirstParty}
	if _, err := a.planConfiguration(context.Background(), s, "claude", p, "key", nil, ""); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatal("native mode replaced by gateway without restore")
	}
}

func TestNativeStopRemainsAvailableWithInterruptedSettings(t *testing.T) {
	d := testDisk(t)
	if err := d.write(d.Root+"/pending.json", []byte(`{"unfinished":true}`)); err != nil {
		t.Fatal(err)
	}
	r := &runtimeFixture{}
	var out bytes.Buffer
	a := Application{Disk: d, Native: r, Out: &out}
	if err := a.Run(context.Background(), []string{"native", "--action", "stop"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.events, ",") != "stop" {
		t.Fatal("pending settings blocked listener shutdown")
	}
}

func TestDisconnectStillRevokesAndStopsNativeWithPendingJournal(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	p := testProfile()
	s.Profiles[p.Name] = p
	if e := d.Save(s); e != nil {
		t.Fatal(e)
	}
	if e := d.write(d.Root+"/pending.json", []byte(`{"unfinished":true}`)); e != nil {
		t.Fatal(e)
	}
	api := &mockAPI{}
	store := &memoryStore{values: map[string]string{credentialID(p): "secret"}}
	r := &runtimeFixture{status: NativeRuntimeStatus{Enabled: true, Trusted: true}}
	var out bytes.Buffer
	app := Application{Disk: d, Store: store, Services: &mockServices{}, Native: r, API: func(string) (ClientAPI, error) { return api, nil }, Prompt: answer(true), Out: &out}
	if e := app.Run(context.Background(), []string{"disconnect", "--profile", p.Name}); e == nil || !strings.Contains(e.Error(), "transaction retained") {
		t.Fatalf("pending recovery not reported: %v", e)
	}
	if !api.revoked || len(store.values) != 0 || strings.Join(r.events, ",") != "stop,disable" {
		t.Fatal("pending journal prevented local or remote cleanup")
	}
	if !exists(d.Root + "/pending.json") {
		t.Fatal("pending recovery journal discarded")
	}
}

func TestNativeConfigurationRetainsRuntimeOwnershipForDisable(t *testing.T) {
	p := testProfile()
	home := realTemp(t)
	ready := NativeRuntimeReady{Profile: p.Name, Mode: NativeCompatibilityClaudeFirstParty, Build: NativeCompatibilityClaudeBuild, ProxyURL: "http://owned:token@127.0.0.1:17844", CAPath: home + "/ca.pem"}
	ready.proof = &nativeReadyProof{profile: ready.Profile, mode: ready.Mode, build: ready.Build, proxyURL: ready.ProxyURL, caPath: ready.CAPath, expires: time.Now().Add(time.Minute)}
	r := &runtimeFixture{ready: ready}
	d := testDisk(t)
	s, _ := d.Load()
	var out bytes.Buffer
	a := Application{Disk: d, Native: r, Resolver: Resolver{Home: home, OS: "darwin", Env: map[string]string{}}, Out: &out, Prompt: answer(true)}
	changes, e := a.planConfiguration(context.Background(), s, "claude", p, "remote-key", nil, ready.Mode)
	if e != nil {
		t.Fatal(e)
	}
	if len(changes) == 0 {
		t.Fatal("no native settings planned")
	}
	if e = d.Apply(&s, "claude", p.Name, changes, answer(true)); e != nil {
		t.Fatal(e)
	}
	if s.Records["claude"].RuntimeMode != ready.Mode {
		t.Fatal("runtime ownership missing")
	}
	if e = a.runNative(context.Background(), &s, p.Name, "disable", "", "", 0); e != nil {
		t.Fatal(e)
	}
	if len(s.Records) != 0 {
		t.Fatal("runtime settings not restored")
	}
}

func (r *runtimeFixture) Launch(context.Context, string) error {
	r.events = append(r.events, "launch")
	return nil
}

func TestNativeLaunchRequiresReadyCodexModeAndExplicitAcceptance(t *testing.T) {
	for _, accept := range []bool{false, true} {
		ready := NativeRuntimeReady{Profile: "test", Mode: NativeCompatibilityCodexObserve, Build: NativeCompatibilityCodexBuild, ProxyURL: "http://owned:secret@127.0.0.1:17844", CAPath: "/owned/ca.pem"}
		ready.proof = &nativeReadyProof{profile: ready.Profile, mode: ready.Mode, build: ready.Build, proxyURL: ready.ProxyURL, caPath: ready.CAPath, expires: time.Now().Add(time.Minute)}
		r := &runtimeFixture{ready: ready}
		var out bytes.Buffer
		a := Application{Native: r, Prompt: answer(accept), Out: &out}
		s := emptyState()
		err := a.runNative(context.Background(), &s, "test", "launch", "", "", 0)
		if accept {
			if err != nil || strings.Join(r.events, ",") != "launch" {
				t.Fatalf("launch: %v", err)
			}
		} else if err == nil || len(r.events) != 0 {
			t.Fatal("launch without confirmation")
		}
		if strings.Contains(out.String(), "secret") {
			t.Fatal("launch leaked proxy secret")
		}
	}
}
