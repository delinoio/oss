// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestLifecyclePluginsRequireCompleteDisabledObservation(t *testing.T) {
	for _, raw := range []string{
		`{"data":[{"name":"plugins","enabled":true}],"nextCursor":null}`,
		`{"data":[{"name":"plugins","enabled":null}],"nextCursor":null}`,
		`{"data":[{"name":"plugins"}],"nextCursor":null}`,
		`{"data":[{"name":"other","enabled":false}],"nextCursor":null}`,
		`{"data":[{"name":"plugins","enabled":false}],"nextCursor":"more"}`,
		`{"data":[{"name":"plugins","enabled":false},{"name":"plugins","enabled":false}],"nextCursor":null}`,
		`{"data":[{"name":"plugins","enabled":false,"enabled":true}],"nextCursor":null}`,
		`{"data":[{"name":"plugins","enabled":false,"unknown":true}],"nextCursor":null}`,
		`{"data":null,"nextCursor":null}`,
	} {
		if err := validateLifecyclePlugins(json.RawMessage(raw)); domain.SafeError(err).Code != domain.Unsupported {
			t.Fatal("unconfirmed plugin disablement was accepted")
		}
	}
	if err := validateLifecyclePlugins(json.RawMessage(`{"data":[{"name":"plugins","enabled":false}],"nextCursor":null}`)); err != nil {
		t.Fatal(err)
	}
	features := []any{map[string]any{"name": "plugins", "enabled": false}}
	for i := 0; i < 256; i++ {
		features = append(features, map[string]any{"name": fmt.Sprintf("feature-%d", i), "enabled": false})
	}
	raw, _ := json.Marshal(map[string]any{"data": features, "nextCursor": nil})
	if err := validateLifecyclePlugins(raw); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("oversized feature inventory accepted")
	}
}

func TestLifecyclePluginOverrideDoesNotChangeThreadProfile(t *testing.T) {
	config := fixtureConfig(t, "thread-managed-ready")
	config.Mode, config.ManagedAuthentication = ThreadProtocol, true
	sentinel := filepath.Join(config.Home, "plugins-disabled")
	config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_PLUGIN_OVERRIDE_SENTINEL="+sentinel)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(sentinel); !os.IsNotExist(err) {
		t.Fatal("lifecycle override changed native thread features")
	}
}

func TestLifecyclePluginsRejectedBeforeLogin(t *testing.T) {
	for _, protocol := range []ProtocolMode{ProbeProtocol, SubscriptionProtocol} {
		for _, failure := range []string{"plugins-enabled", "plugins-missing"} {
			t.Run(string(protocol)+"/"+failure, func(t *testing.T) {
				mode := failure
				if protocol == SubscriptionProtocol {
					mode = "managed-" + failure
				}
				config := fixtureConfig(t, mode)
				config.Mode, config.ManagedAuthentication = protocol, protocol == SubscriptionProtocol
				sentinel := filepath.Join(config.Home, "login-sent")
				config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_LOGIN_SENTINEL="+sentinel)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				client, err := Open(ctx, config)
				if client != nil {
					_ = client.Close()
					t.Fatal("unverified lifecycle profile returned a client")
				}
				if domain.SafeError(err).Code != domain.Unsupported {
					t.Fatal("unexpected lifecycle rejection")
				}
				if _, err := os.Lstat(sentinel); !os.IsNotExist(err) {
					t.Fatal("login sent before plugin observation")
				}
				if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
					t.Fatal("rejected native owner did not join")
				}
			})
		}
	}
}
