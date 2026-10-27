// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEveryFileBuilderProducesParseableSettings(t *testing.T) {
	for _, spec := range clients {
		t.Run(spec.ID, func(t *testing.T) {
			home := realTemp(t)
			r := Resolver{Home: home, OS: "darwin", Env: map[string]string{}}
			if spec.ID == "aside" {
				_ = os.MkdirAll(filepath.Join(home, ".aside"), 0700)
				_ = os.WriteFile(filepath.Join(home, ".aside", "accounts.json"), []byte(`{"currentAccountId":0}`), 0600)
			}
			changes, e := PlanSettings(r, emptyState(), spec.ID, testProfile(), "REMOTE_SECRET", []ClientModel{{ID: "vendor/model", Name: "Model", Context: 128000, Output: 8192, Vision: true, Tools: true, Efforts: []string{"low", "high"}}})
			if e != nil {
				t.Fatal(e)
			}
			if len(changes) == 0 {
				t.Fatal("no settings")
			}
			for _, change := range changes {
				format := spec.Format
				if spec.ID == "codex" || spec.ID == "claude" || spec.ID == "claude-desktop" {
					format = "json"
					if filepath.Ext(change.Path) == ".toml" {
						format = "toml"
					}
				}
				if _, e = parseDocument(change.After, format); e != nil {
					t.Fatal(e)
				}
				if spec.Loopback && bytes.Contains(change.After, []byte("REMOTE_SECRET")) {
					t.Fatal("remote key placed in loopback client")
				}
				if !spec.Loopback && bytes.Contains(change.After, []byte("REMOTE_SECRET")) && !change.RemoteKey {
					t.Fatal("missing separate remote-key consent")
				}
			}
		})
	}
}
func TestProfileSwitchPreservesPriorKeyAndOriginalBackup(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
	path, _ := r.Resolve("pi")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	original := []byte("{\"user\":true}\n")
	_ = os.WriteFile(path, original, 0600)
	models := []ClientModel{{ID: "v/m", Name: "M"}}
	p := testProfile()
	first, e := PlanSettings(r, s, "pi", p, "secret", models)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Apply(&s, "pi", p.Name, first, answer(true)); e != nil {
		t.Fatal(e)
	}
	p.Name = "second"
	p.Port++
	second, e := PlanSettings(r, s, "pi", p, "different", models)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Apply(&s, "pi", p.Name, second, answer(true)); e != nil {
		t.Fatal(e)
	}
	if s.Active["pi"] != "second" {
		t.Fatal("active profile unchanged")
	}
	if e = d.Restore(&s, "pi"); e != nil {
		t.Fatal(e)
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, original) {
		t.Fatal("original backup was replaced during switch")
	}
}

type mockServices struct {
	stops, removes int
	err            error
}

func (s *mockServices) Install(context.Context, Profile, string, string) error { return s.err }
func (s *mockServices) Status(context.Context, Profile) (string, error)        { return "mock", s.err }
func (s *mockServices) Stop(context.Context, Profile) error                    { s.stops++; return s.err }
func (s *mockServices) Remove(context.Context, Profile) error                  { s.removes++; return s.err }
func TestOfflineDisconnectCleansLocallyAndRetainsConflict(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	p := testProfile()
	p.KeyID = "key-id"
	p.Organization = "org"
	s.Profiles[p.Name] = p
	path := filepath.Join(realTemp(t), "config")
	_ = os.WriteFile(path, []byte("edited"), 0600)
	s.Records["pi"] = Record{Profile: p.Name, Client: "pi", Files: []Backup{{Path: path, Before: []byte("original"), After: []byte("applied"), Existed: true}}}
	store := &memoryStore{values: map[string]string{credentialID(p): "secret"}}
	services := &mockServices{}
	api := &mockAPI{revokeErr: errors.New("offline")}
	app := Application{Disk: d, Store: store, Services: services, API: func(string) (ClientAPI, error) { return api, nil }, Prompt: answer(true), Out: io.Discard}
	e := app.Disconnect(context.Background(), &s, p)
	if e == nil || !strings.Contains(e.Error(), "server revocation incomplete") || !strings.Contains(e.Error(), "settings conflict") {
		t.Fatal(e)
	}
	if services.stops != 1 || services.removes != 1 || len(store.values) != 0 || !s.Profiles[p.Name].RevocationPending || len(s.Records) != 1 {
		t.Fatal("incomplete local cleanup")
	}
	current, _ := os.ReadFile(path)
	if string(current) != "edited" {
		t.Fatal("conflict overwritten")
	}
}

type recordingRunner struct{ calls [][]string }

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return "running", nil
}
func TestServiceInstallUsesArgumentArraysAndNoSecrets(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		t.Run(platform, func(t *testing.T) {
			d := testDisk(t)
			_ = d.init()
			runner := &recordingRunner{}
			s := UserServices{OS: platform, Home: realTemp(t), UID: "501", Runner: runner, Disk: d}
			if e := s.Install(context.Background(), testProfile(), "/apps/path with spaces/aiprouter", d.Root); e != nil {
				t.Fatal(e)
			}
			if len(runner.calls) == 0 {
				t.Fatal("no service installation")
			}
			for _, call := range runner.calls {
				if strings.Contains(strings.Join(call, " "), "SECRET") {
					t.Fatal("secret in process arguments")
				}
				if call[0] == "sh" || call[0] == "cmd.exe" {
					t.Fatal("shell used")
				}
			}
			if platform != "windows" {
				data, e := os.ReadFile(s.path(testProfile()))
				if e != nil {
					t.Fatal(e)
				}
				if !bytes.Contains(data, []byte("relay")) || !bytes.Contains(data, []byte("--state-dir")) {
					t.Fatal("wrong service command")
				}
			}
		})
	}
}
func TestDSHActiveProfileAndZcodeStore(t *testing.T) {
	for _, id := range []string{"dsh", "zcode"} {
		t.Run(id, func(t *testing.T) {
			r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
			var path string
			var original []byte
			if id == "dsh" {
				path = filepath.Join(r.Home, ".dsh", "profiles", "desktop", "cordis.patch.yml")
				original = []byte("- id: llm-pi-ai\n  config:\n    providers:\n      other:\n        api: keep\n")
			} else {
				path = filepath.Join(r.Home, ".zcode", "v2", "provider_config.json")
				original = []byte(`{"schemaVersion":1,"config":{"providerConfigRules":{"providerRules":[]},"modelConfigRules":{"providerModelRules":[]}}}`)
			}
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			_ = os.WriteFile(path, original, 0600)
			changes, e := PlanSettings(r, emptyState(), id, testProfile(), "secret", []ClientModel{{ID: "v/m", Name: "M", Context: 128000}})
			if e != nil {
				t.Fatal(e)
			}
			if changes[0].Path != path {
				t.Fatal("wrong active target")
			}
			if !bytes.Contains(changes[0].After, []byte("aiprouter")) {
				t.Fatal("provider missing")
			}
		})
	}
}

type failingProtection struct {
	native NativeProtector
	failAt int
	count  int
}

func (p *failingProtection) Protect(path string, dir bool) error {
	if !dir {
		p.count++
		if p.count == p.failAt {
			return errors.New("mock permission failure")
		}
	}
	return p.native.Protect(path, dir)
}
func TestPendingPairRecoveryAfterSecondFileFailure(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	first := filepath.Join(realTemp(t), "providers.json")
	second := filepath.Join(filepath.Dir(first), "models.json")
	old := []byte("original")
	_ = os.WriteFile(first, old, 0600)
	_ = os.WriteFile(second, old, 0600)
	protection := &failingProtection{failAt: 3}
	d.Protection = protection
	changes := []Change{{Path: first, Before: old, After: []byte("new provider"), Existed: true}, {Path: second, Before: old, After: []byte("new catalog"), Existed: true}}
	if d.Apply(&s, "cline", "test", changes, answer(true)) == nil {
		t.Fatal("injected failure ignored")
	}
	if !exists(filepath.Join(d.Root, "pending.json")) {
		t.Fatal("recovery journal missing")
	}
	a := Application{Disk: d, Prompt: answer(true)}
	if e := a.Recover(&s); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{first, second} {
		b, _ := os.ReadFile(p)
		if !bytes.Equal(b, old) {
			t.Fatal("pair rollback incomplete")
		}
	}
	if exists(filepath.Join(d.Root, "pending.json")) {
		t.Fatal("journal not removed")
	}
}
func TestPortChangeFailureLeavesProfileAndSettingsRecoverable(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	p := testProfile()
	s.Profiles[p.Name] = p
	first := filepath.Join(realTemp(t), "first.json")
	second := filepath.Join(filepath.Dir(first), "second.json")
	_ = os.WriteFile(first, []byte("old port"), 0600)
	_ = os.WriteFile(second, []byte("old port"), 0600)
	_ = d.Save(s)
	d.Protection = &failingProtection{failAt: 3}
	plans := map[string][]Change{"pi": {{Path: first, Before: []byte("old port"), After: []byte("new port"), Existed: true}}, "omo": {{Path: second, Before: []byte("old port"), After: []byte("new port"), Existed: true}}}
	next := p
	next.Port++
	if d.ChangePort(&s, next, plans) == nil {
		t.Fatal("injected failure ignored")
	}
	if s.Profiles[p.Name].Port != p.Port {
		t.Fatal("port committed before all settings")
	}
	a := Application{Disk: d, Prompt: answer(true)}
	if e := a.Recover(&s); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{first, second} {
		b, _ := os.ReadFile(path)
		if string(b) != "old port" {
			t.Fatal("partial port change survived rollback")
		}
	}
}
func TestPermissionFailureDoesNotWriteClient(t *testing.T) {
	d := testDisk(t)
	s, _ := d.Load()
	d.Protection = &failingProtection{failAt: 1}
	path := filepath.Join(realTemp(t), "config")
	_ = os.WriteFile(path, []byte("before"), 0600)
	e := d.Apply(&s, "pi", "test", []Change{{Path: path, Before: []byte("before"), After: []byte("secret"), Existed: true}}, answer(true))
	if e == nil {
		t.Fatal("ACL failure ignored")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "before" {
		t.Fatal("unprotected write")
	}
}
func TestLegacyWorkBuddyArrayAndEmptyDSHPatch(t *testing.T) {
	for _, id := range []string{"workbuddy", "dsh"} {
		r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
		path, _ := r.Resolve(id)
		original := []byte("[{\"id\":\"foreign\",\"secret\":\"preserve\"}]\n")
		if id == "dsh" {
			path = filepath.Join(r.Home, ".dsh", "profiles", "desktop", "cordis.patch.yml")
			original = []byte("[]\n")
		}
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		_ = os.WriteFile(path, original, 0600)
		changes, e := PlanSettings(r, emptyState(), id, testProfile(), "key", []ClientModel{{ID: "v/m", Name: "M"}})
		if e != nil {
			t.Fatalf("%s: %v", id, e)
		}
		if id == "workbuddy" && !bytes.Contains(changes[0].After, []byte(`{"id":"foreign","secret":"preserve"}`)) {
			t.Fatal("foreign row changed")
		}
	}
}
func TestEmptyYAMLMapKeepsComments(t *testing.T) {
	source := []byte("# keep\nproviders: {} # also keep\nuser: unchanged\n")
	after, e := Patch(source, YAML, []Fragment{{[]string{"providers", "aiprouter"}, object{"api": "endpoint"}}}, false)
	if e != nil {
		t.Fatal(e)
	}
	for _, part := range []string{"# keep", "# also keep", "user: unchanged"} {
		if !bytes.Contains(after, []byte(part)) {
			t.Fatal("comment or user setting lost")
		}
	}
}
func TestCodexExistingNativeSelectionRestoresExactly(t *testing.T) {
	r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
	path, _ := r.Resolve("codex")
	source := []byte("# native settings\nmodel_provider = 'openai' # keep\nmodel = 'native-model'\n[projects.\"/home/example\"]\ntrust_level = 'trusted'\n")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, source, 0600)
	d := testDisk(t)
	s, _ := d.Load()
	changes, e := PlanSettings(r, s, "codex", testProfile(), "KEY", []ClientModel{{ID: "v/m", Name: "M"}})
	if e != nil {
		t.Fatal(e)
	}
	doc, e := parseDocument(changes[0].After, TOML)
	if e != nil || doc["model_provider"] != "openai" || doc["model"] != "native-model" {
		t.Fatalf("wrong native settings: %v %v", doc, e)
	}
	if e = d.Apply(&s, "codex", "test", changes, answer(true)); e != nil {
		t.Fatal(e)
	}
	if e = d.Restore(&s, "codex"); e != nil {
		t.Fatal(e)
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, source) {
		t.Fatal("native source not restored")
	}
}
func TestDesktopPreservesAndRestoresExistingSelection(t *testing.T) {
	r := Resolver{Home: realTemp(t), OS: "darwin", Env: map[string]string{}}
	path, _ := r.Resolve("claude-desktop")
	meta := filepath.Join(filepath.Dir(path), "_meta.json")
	source := []byte(`{"appliedId":"foreign","entries":[{"id":"foreign","name":"My profile"}],"userSecret":"preserve"}`)
	_ = os.MkdirAll(filepath.Dir(meta), 0700)
	_ = os.WriteFile(meta, source, 0600)
	d := testDisk(t)
	s, _ := d.Load()
	changes, e := PlanSettings(r, s, "claude-desktop", testProfile(), "KEY", []ClientModel{{ID: "v/m", Name: "M"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Apply(&s, "claude-desktop", "test", changes, answer(true)); e != nil {
		t.Fatal(e)
	}
	if e = d.Restore(&s, "claude-desktop"); e != nil {
		t.Fatal(e)
	}
	actual, _ := os.ReadFile(meta)
	if !bytes.Equal(actual, source) || exists(path) {
		t.Fatal("Desktop selection/profile not restored")
	}
}
func TestMutableAsideSelectorRefusesAfterPreview(t *testing.T) {
	r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
	manifest := filepath.Join(r.Home, ".aside", "accounts.json")
	_ = os.MkdirAll(filepath.Dir(manifest), 0700)
	_ = os.WriteFile(manifest, []byte(`{"currentAccountId":0,"accounts":[{"id":0},{"id":1}]}`), 0600)
	d := testDisk(t)
	s, _ := d.Load()
	changes, e := PlanSettings(r, s, "aside", testProfile(), "key", []ClientModel{{ID: "v/m", Name: "M"}})
	if e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(manifest, []byte(`{"currentAccountId":1,"accounts":[{"id":0},{"id":1}]}`), 0600)
	if e = d.Apply(&s, "aside", "test", changes, answer(true)); e == nil {
		t.Fatal("mutated a stale account target")
	}
	if exists(changes[0].Path) {
		t.Fatal("stale account file created")
	}
}
func TestDSHRespectsNativeWriterLock(t *testing.T) {
	r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
	d := testDisk(t)
	s, _ := d.Load()
	changes, e := PlanSettings(r, s, "dsh", testProfile(), "key", []ClientModel{{ID: "v/m", Name: "M"}})
	if e != nil {
		t.Fatal(e)
	}
	lock := changes[0].Path + ".lock"
	_ = os.MkdirAll(filepath.Dir(lock), 0700)
	_ = os.WriteFile(lock, []byte("foreign PID"), 0600)
	if e = d.Apply(&s, "dsh", "test", changes, answer(true)); e == nil {
		t.Fatal("native lock ignored")
	}
	b, _ := os.ReadFile(lock)
	if string(b) != "foreign PID" {
		t.Fatal("contender lock removed")
	}
}
