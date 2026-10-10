// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"strings"
	"testing"
)

func nativeAppsScopeFixture() domain.NativeAppScope {
	return domain.NativeAppScope{SessionID: domain.NewID(), MachineID: domain.NewID(), AccountID: domain.NewID(), ConnectionID: domain.NewID(), ConfigurationDigest: strings.Repeat("a", 64)}
}
func TestSessionNativeAppsInventorySeparatesDiscoveryInstallationAndCallability(t *testing.T) {
	scope := nativeAppsScopeFixture()
	thread := domain.NewID()
	calls := []string{}
	call := func(_ context.Context, _ domain.ID, method string, params any) (nativewire.Response, error) {
		calls = append(calls, method)
		raw, _ := json.Marshal(params)
		if !strings.Contains(string(raw), string(thread)) || (method != "app/read" && !strings.Contains(string(raw), "true")) {
			t.Fatal("original thread or explicit refresh lost")
		}
		var result string
		switch method {
		case "app/list":
			result = `{"data":[{"id":"available","name":"Available App","isAccessible":true,"isEnabled":true,"installUrl":"https://private-inventory.example/secret"},{"id":"callable","name":"Callable App","isAccessible":true,"isEnabled":true}],"nextCursor":null}`
		case "app/read":
			result = `{"apps":[{"id":"available","name":"Available App"},{"id":"callable","name":"Callable App"}],"missingAppIds":[]}`
		case "app/installed":
			result = `{"apps":[{"id":"callable","runtimeName":"Untrusted runtime name","enabled":true,"callable":true}]}`
		default:
			t.Fatal("unexpected native effect", method)
		}
		return nativewire.Response{Result: json.RawMessage(result)}, nil
	}
	snapshot, err := readAppsInventory(context.Background(), scope, thread, true, call)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Discovered) != 2 || len(snapshot.Installed) != 1 {
		t.Fatal("discovery collapsed into installed authority")
	}
	raw, _ := json.Marshal(snapshot)
	if strings.Contains(string(raw), "private-inventory") || strings.Contains(string(raw), "Untrusted runtime name") {
		t.Fatal("private metadata escaped")
	}
	selection := domain.SessionNativeAppSelection{Scope: scope, InventoryID: snapshot.InventoryID, Revision: 1, AppIDs: []string{"available", "callable"}}
	if domain.AdmitNativeAppCall(selection, selection, snapshot, "available") == nil || domain.AdmitNativeAppCall(selection, selection, snapshot, "callable") != nil {
		t.Fatal("available app borrowed callable authority")
	}
	if len(calls) != 3 || calls[0] != "app/list" || calls[1] != "app/read" || calls[2] != "app/installed" {
		t.Fatal("unexpected read/effect order")
	}
}
func TestSessionNativeAppsInventoryRejectsUnknownPartialAndUncertainNativeResults(t *testing.T) {
	for _, scenario := range []string{"missing-list", "duplicate-list", "unknown-list", "cursor-loop", "missing-installed", "unknown-installed", "contradictory-installed", "refresh-error", "foreign-id", "missing-read", "foreign-read", "duplicate-read", "uncertain-read", "display-tools"} {
		t.Run(scenario, func(t *testing.T) {
			pages := 0
			call := func(_ context.Context, _ domain.ID, method string, _ any) (nativewire.Response, error) {
				raw := `{"data":[{"id":"original","name":"Original App","isAccessible":true}],"nextCursor":null}`
				if method == "app/list" {
					pages++
					switch scenario {
					case "missing-list":
						raw = `{"nextCursor":null}`
					case "duplicate-list":
						raw = `{"data":[{"id":"original","name":"Original App"},{"id":"original","name":"Original App"}],"nextCursor":null}`
					case "unknown-list":
						raw = `{"data":[{"id":"original","name":"Original App","newAuthority":true}],"nextCursor":null}`
					case "cursor-loop":
						raw = `{"data":[{"id":"original","name":"Original App"}],"nextCursor":"same"}`
					case "foreign-id":
						raw = `{"data":[{"id":" original ","name":"Original App"}],"nextCursor":null}`
					}
				} else if method == "app/read" {
					raw = `{"apps":[{"id":"original","name":"Original App"}],"missingAppIds":[]}`
					switch scenario {
					case "missing-read":
						raw = `{"apps":[],"missingAppIds":[]}`
					case "foreign-read":
						raw = `{"apps":[{"id":"foreign","name":"Foreign App"}],"missingAppIds":[]}`
					case "duplicate-read":
						raw = `{"apps":[{"id":"original","name":"Original App"}],"missingAppIds":["original"]}`
					case "uncertain-read":
						code := int64(-32603)
						return nativewire.Response{ErrorCode: &code}, nil
					case "display-tools":
						raw = `{"apps":[{"id":"original","name":"Original App","toolSummaries":[{"name":"callable"}]}],"missingAppIds":[]}`
					}
				} else {
					raw = `{"apps":[{"id":"original","enabled":true,"callable":true}]}`
					switch scenario {
					case "missing-installed":
						raw = `{"apps":[{"id":"original","enabled":true}]}`
					case "unknown-installed":
						raw = `{"apps":[{"id":"original","enabled":true,"callable":true,"newAuthority":true}]}`
					case "contradictory-installed":
						raw = `{"apps":[{"id":"original","enabled":false,"callable":true}]}`
					case "refresh-error":
						code := int64(-32603)
						return nativewire.Response{ErrorCode: &code}, nil
					}
				}
				return nativewire.Response{Result: json.RawMessage(raw)}, nil
			}
			value, err := readAppsInventory(context.Background(), nativeAppsScopeFixture(), domain.NewID(), true, call)
			if err == nil || value.InventoryID != "" {
				t.Fatal("partial, ambiguous or failed native inventory published")
			}
			if pages > 2 {
				t.Fatal("unbounded repeated pagination")
			}
		})
	}
}
