//go:build !windows

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/async-commit-hook/internal/core"
)

func verifyLocalUI(t *testing.T, address string) {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || strings.Contains(address, "pair=") {
		t.Fatal(address, err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !bytes.Contains(body, []byte(`<div id="root">`)) {
		t.Fatal(response.StatusCode, string(body), err)
	}
	for _, match := range regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllSubmatch(body, -1) {
		asset, err := client.Get(parsed.Scheme + "://" + parsed.Host + string(match[1]))
		if err != nil {
			t.Fatal(err)
		}
		asset.Body.Close()
		if asset.StatusCode != 200 {
			t.Fatal(asset.StatusCode)
		}
	}
}

func TestBinaryServesLocalUIAndControlsWithoutPairing(t *testing.T) {
	// TestMain builds the sole executable into a temporary directory. Neither
	// the daemon nor HTTP handlers can rely on repository-relative runtime assets.
	config, repo := setup(t, "daemon")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "--quiet", "-m", "local ui")
	status, _ := invoke(t, config, repo, "status")
	b, _ := json.Marshal(status.Result)
	var page core.Page
	if err := json.Unmarshal(b, &page); err != nil || len(page.Runs) != 1 {
		t.Fatal(string(b), err)
	}
	id := page.Runs[0].ID
	var address string
	for i := 0; i < 2; i++ {
		result, code := invoke(t, config, repo, "ui", "--run", id)
		if code != 0 {
			t.Fatal(result)
		}
		current := result.Result.(map[string]any)["url"].(string)
		if i > 0 && current != address {
			t.Fatal("daemon reuse changed URL")
		}
		address = current
		verifyLocalUI(t, address)
	}
	parsed, _ := url.Parse(address)
	if parsed.Fragment != "run="+id {
		t.Fatal(address)
	}
	origin := parsed.Scheme + "://" + parsed.Host
	client := &http.Client{Timeout: 5 * time.Second}
	rpc := func(method, body string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest("POST", origin+"/async_commit_hook.v1.LocalService/"+method, strings.NewReader(body))
		req.Header.Set("Origin", origin)
		req.Header.Set("X-Ach-Api-Version", "1")
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		b, _ := io.ReadAll(response.Body)
		if response.StatusCode != 200 {
			t.Fatal(method, response.StatusCode, string(b))
		}
		var value map[string]any
		if err := json.Unmarshal(b, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	rpc("ListRepositories", "{}")
	rpc("GetRun", `{"runId":"`+id+`"}`)
	rpc("Cancel", `{"runId":"`+id+`"}`)
	if result, code := invoke(t, config, repo, "wait", "--run", id, "--timeout", "15"); code != 0 {
		t.Fatal(result)
	}
	rpc("Acknowledge", `{"runId":"`+id+`"}`)
	if err := os.WriteFile(filepath.Join(filepath.Dir(config), "release-check"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	rerun := rpc("Rerun", `{"runId":"`+id+`"}`)["runId"].(string)
	if result, code := invoke(t, config, repo, "wait", "--run", rerun, "--timeout", "15"); code != 0 {
		t.Fatal(result)
	}
	if result, code := invoke(t, config, repo, "browser", "list"); code != 2 || result.Error == nil || result.Error.Code != "browser-management-removed" {
		t.Fatal(result, code)
	}
}
