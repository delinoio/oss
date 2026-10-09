// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRetiredModelCommandsHaveNoConnectionOrCredentialSideEffect(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer peer.Close()
	for _, args := range [][]string{{"model"}, {"model", "list"}, {"model", "get", "--id", "legacy"}, {"model", "snapshot"}, {"model", "create", "--input", "-"}, {"model", "edit", "--id", "legacy", "--revision", "1", "--input", "-"}, {"model", "delete", "--id", "legacy", "--revision", "1"}, {"model", "search"}, {"model", "resolve", "--selector", "legacy"}} {
		root := filepath.Join(t.TempDir(), "not-created")
		input := strings.NewReader("credential-sentinel")
		var output, diagnostic strings.Builder
		options := []string{"--data-dir", root, "--server", peer.URL, "--token-stdin"}
		options = append(options, args...)
		if code := Run(context.Background(), options, IO{In: input, Out: &output, Err: &diagnostic}); code == 0 {
			t.Fatal("retired Model catalog accepted")
		}
		if !strings.Contains(output.String(), `"code":"unsupported"`) {
			t.Fatal("retired model did not return typed Unsupported", output.String())
		}
		if calls.Load() != 0 || input.Len() != len("credential-sentinel") || strings.Contains(output.String()+diagnostic.String(), "credential-sentinel") {
			t.Fatal("unknown command acquired authority")
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("unknown command created server/client state", err)
		}
	}
}
