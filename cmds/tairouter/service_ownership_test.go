// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ownershipRunner struct {
	platform string
	owner    UserServices
	profile  Profile
	calls    [][]string
	taskXML  string
	queryErr error
}

func (r *ownershipRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	if r.platform == "windows" && len(args) > 0 && args[0] == "/Create" {
		data, err := os.ReadFile(args[len(args)-1])
		if err != nil {
			return "", err
		}
		r.taskXML = string(data)
	}
	if (r.platform == "linux" && len(args) > 1 && args[1] == "show") ||
		(r.platform == "darwin" && len(args) > 0 && args[0] == "print") ||
		(r.platform == "windows" && len(args) > 0 && args[0] == "/Query") {
		if r.queryErr != nil {
			return "", r.queryErr
		}
		switch r.platform {
		case "linux":
			return "FragmentPath=" + r.owner.path(r.profile) + "\nDropInPaths=\nNeedDaemonReload=no\nLoadState=loaded\n", nil
		case "darwin":
			return "path = " + r.owner.path(r.profile) + "\narguments = {\n\t/apps/tairouter\n\trelay\n\t--profile\n\t" + r.profile.Name + "\n\t--state-dir\n\t" + r.owner.Disk.Root + "\n}\n", nil
		case "windows":
			return r.taskXML, nil
		}
	}
	return "ok", nil
}

func (r *ownershipRunner) destructiveCalls() int {
	count := 0
	for _, call := range r.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, " stop ") || strings.Contains(joined, " disable ") || strings.Contains(joined, " bootout ") || strings.Contains(joined, " /End ") || strings.Contains(joined, " /Delete ") {
			count++
		}
	}
	return count
}

func setupOwnedService(t *testing.T, platform string) (UserServices, UserServices, *ownershipRunner, Profile) {
	t.Helper()
	home := realTemp(t)
	a, b := testDisk(t), testDisk(t)
	a.Root = filepath.Join(home, "state A")
	b.Root = a.Root + "-other"
	p := testProfile()
	r := &ownershipRunner{platform: platform, profile: p}
	owner := UserServices{OS: platform, Home: home, UID: "501", Runner: r, Disk: a}
	other := UserServices{OS: platform, Home: home, UID: "501", Runner: r, Disk: b}
	r.owner = owner
	if err := owner.Install(context.Background(), p, "/apps/tairouter", a.Root); err != nil {
		t.Fatal(err)
	}
	return owner, other, r, p
}

func TestServiceTeardownRequiresStateRootOwnership(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		t.Run(platform, func(t *testing.T) {
			owner, other, runner, p := setupOwnedService(t, platform)
			var original []byte
			if platform != "windows" {
				original, _ = os.ReadFile(owner.path(p))
				if err := other.Install(context.Background(), p, "/apps/tairouter", other.Disk.Root); err == nil {
					t.Fatal("foreign install replaced existing definition")
				}
			}
			before := runner.destructiveCalls()
			for _, operation := range []func(context.Context, Profile) error{other.Stop, other.Remove} {
				if err := operation(context.Background(), p); err == nil || !strings.Contains(err.Error(), "ownership") {
					t.Fatalf("foreign teardown: %v", err)
				}
			}
			if runner.destructiveCalls() != before {
				t.Fatal("foreign service received destructive command")
			}
			if platform != "windows" {
				after, _ := os.ReadFile(owner.path(p))
				if string(after) != string(original) {
					t.Fatal("foreign service definition changed")
				}
			}
			if err := owner.Stop(context.Background(), p); err != nil {
				t.Fatal("owner cannot stop matching service:", err)
			}
			if err := owner.Remove(context.Background(), p); err != nil {
				t.Fatal("owner cannot remove matching service:", err)
			}
			if runner.destructiveCalls() == before {
				t.Fatal("owner did not receive teardown commands")
			}
			if platform != "windows" {
				if _, err := os.Stat(owner.path(p)); !os.IsNotExist(err) {
					t.Fatal("owned definition remains:", err)
				}
			}
		})
	}
}

func TestServiceTeardownRejectsUnverifiableOwnership(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		t.Run(platform, func(t *testing.T) {
			owner, _, runner, p := setupOwnedService(t, platform)
			before := runner.destructiveCalls()
			runner.queryErr = errors.New("permission denied: secret details")
			if err := owner.Remove(context.Background(), p); err == nil || strings.Contains(err.Error(), "secret details") {
				t.Fatalf("query failure leaked or ignored: %v", err)
			}
			runner.queryErr = nil
			if platform == "windows" {
				runner.taskXML = "<Task><Actions><Exec><Command>other</Command></Exec></Actions></Task>"
			} else {
				if err := os.WriteFile(owner.path(p), []byte("malformed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := owner.Remove(context.Background(), p); err == nil {
				t.Fatal("malformed definition authorized removal")
			}
			if platform == "windows" {
				runner.taskXML = `<Task><Actions><Exec><Command>/apps/tairouter</Command><Arguments>relay --profile ` + p.Name + ` --state-dir ` + windowsQuote(owner.Disk.Root) + `</Arguments></Exec><ComHandler/></Actions></Task>`
				if err := owner.Remove(context.Background(), p); err == nil {
					t.Fatal("additional task action authorized removal")
				}
			}
			if platform != "windows" {
				if err := os.Remove(owner.path(p)); err != nil {
					t.Fatal(err)
				}
				if err := owner.Stop(context.Background(), p); err == nil {
					t.Fatal("missing definition authorized stop")
				}
			}
			if runner.destructiveCalls() != before {
				t.Fatal("unverifiable job received destructive command")
			}
		})
	}
}

func TestDisconnectPreservesForeignServiceAndCleansSelectedProfile(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(map[bool]string{false: "online", true: "offline"}[offline], func(t *testing.T) {
			owner, other, runner, p := setupOwnedService(t, "linux")
			p.KeyID, p.Organization = "key-id", "org"
			state, err := other.Disk.Load()
			if err != nil {
				t.Fatal(err)
			}
			state.Profiles[p.Name] = p
			settings := filepath.Join(realTemp(t), "settings.json")
			if err := os.WriteFile(settings, []byte("applied"), 0600); err != nil {
				t.Fatal(err)
			}
			state.Records["pi"] = Record{Profile: p.Name, Client: "pi", Files: []Backup{{Path: settings, Before: []byte("original"), After: []byte("applied"), Existed: true}}}
			store := &memoryStore{values: map[string]string{credentialID(p): "secret"}}
			api := &mockAPI{}
			if offline {
				api.revokeErr = errors.New("offline")
			}
			app := Application{Disk: other.Disk, Store: store, Services: other, API: func(string) (ClientAPI, error) { return api, nil }, Prompt: answer(true), Out: io.Discard}
			before := runner.destructiveCalls()
			err = app.Disconnect(context.Background(), &state, p)
			if err == nil || !strings.Contains(err.Error(), "relay stop blocked by an ownership conflict") || !strings.Contains(err.Error(), "login service removal blocked by an ownership conflict") {
				t.Fatalf("ownership conflict not reported: %v", err)
			}
			if runner.destructiveCalls() != before || len(store.values) != 0 {
				t.Fatal("foreign service changed or selected credential retained")
			}
			data, _ := os.ReadFile(settings)
			if string(data) != "original" {
				t.Fatal("selected settings not restored")
			}
			_, present := state.Profiles[p.Name]
			if offline != present || (offline && !state.Profiles[p.Name].RevocationPending) {
				t.Fatal("incorrect profile revocation state")
			}
			if _, err := os.Stat(owner.path(p)); err != nil {
				t.Fatal("owner definition removed:", err)
			}
		})
	}
}
