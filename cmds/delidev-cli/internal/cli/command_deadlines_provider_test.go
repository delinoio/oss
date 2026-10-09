// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestCLIUnaryDeadlineRealProviderBodyAndReceipt(t *testing.T) {
	for _, discover := range []bool{false, true} {
		for _, timeout := range []bool{false, true} {
			t.Run(fmt.Sprintf("discover=%t/timeout=%t", discover, timeout), func(t *testing.T) {
				t.Parallel()
				var requests atomic.Int32
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" || r.URL.Path != "/v1/models" {
						t.Error("foreign provider request")
						http.NotFound(w, r)
						return
					}
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					delay := 16 * time.Second
					if timeout {
						delay = 25 * time.Second
					}
					timer := time.NewTimer(delay)
					defer timer.Stop()
					select {
					case <-r.Context().Done():
						return
					case <-timer.C:
					}
					_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model"}]}`)
				}))
				defer provider.Close()
				root := filepath.Join(t.TempDir(), "state")
				ctx, cancel := context.WithCancel(context.Background())
				ready := make(chan struct{})
				done := make(chan error, 1)
				go func() {
					done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
				}()
				defer func() {
					cancel()
					if err := <-done; err != nil {
						t.Error(err)
					}
				}()
				select {
				case <-ready:
				case <-time.After(10 * time.Second):
					t.Fatal("isolated server readiness timeout")
				}
				raw := fmt.Sprintf(`{"name":"fixture","endpoint":%q,"protocol":"openai-chat","authentication":"keyless","discovery":%t}`, provider.URL+"/v1", discover)
				code, value := cliRun(t, root, []string{"provider", "create", "--input", "-"}, raw)
				if code != 0 {
					t.Fatal("provider creation", value)
				}
				providerID := value["result"].(map[string]any)["resource"].(map[string]any)["id"].(string)
				body, _ := json.Marshal(domain.Account{Alias: "Fixture account", ProviderID: domain.ID(providerID), Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected})
				code, value = cliRun(t, root, []string{"account", "create", "--input", "-"}, string(body))
				if code != 0 {
					t.Fatal("account creation", value)
				}
				accountID := value["result"].(map[string]any)["resource"].(map[string]any)["id"].(string)
				code, value = cliRun(t, root, []string{"account", "connect", "--id", accountID, "--revision", "1", "--keyless"}, "")
				if code != 0 {
					t.Fatal("keyless connection", value)
				}
				revision := value["result"].(map[string]any)["account"].(map[string]any)["revision"].(float64)
				args := []string{"account", "validate", "--id", accountID, "--revision", strconv.FormatUint(uint64(revision), 10)}
				if discover {
					args = []string{"provider", "discover", "--account-id", accountID, "--revision", strconv.FormatUint(uint64(revision), 10)}
				}
				original := string(domain.NewID())
				args = append(args, "--request-id", original)
				started := time.Now()
				code, value = cliRun(t, root, args, "")
				elapsed := time.Since(started)
				if value["request_id"] != original || requests.Load() != 1 {
					t.Fatal("original inspection replaced/repeated", value, requests.Load())
				}
				if timeout {
					failure, _ := value["error"].(map[string]any)
					if code == 0 || failure["code"] == "server_unavailable" || elapsed < 19*time.Second || elapsed > 25*time.Second {
						t.Fatal("upstream20second timeout was abandoned early", elapsed, code, value)
					}
				} else if code != 0 || elapsed < 16*time.Second {
					t.Fatal("valid immediate-header delayed body abandoned", elapsed, code, value)
				}
				// Receipt replay must preserve the original result without another provider
				// request, even though its original pre-inspection revision is now stale.
				replayCode, replay := cliRun(t, root, args, "")
				if replayCode != code || replay["request_id"] != original || replay["result"].(map[string]any)["replayed"] != true || requests.Load() != 1 {
					t.Fatal("original inspection receipt repeated upstream work", replayCode, replay, requests.Load())
				}
			})
		}
	}
}
