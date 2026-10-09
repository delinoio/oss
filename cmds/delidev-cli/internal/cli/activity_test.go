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

func TestRetiredActivityCommandHasNoConnectionOrServerSideEffect(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer peer.Close()
	for _, args := range [][]string{{"activity"}, {"activity", "list"}, {"activity", "list", "--page-token", "legacy-cursor"}, {"unknown-retired-command"}} {
		root := filepath.Join(t.TempDir(), "not-created")
		input := strings.NewReader("credential-sentinel")
		var output, diagnostic strings.Builder
		options := []string{"--data-dir", root, "--server", peer.URL, "--token-stdin"}
		options = append(options, args...)
		if code := Run(context.Background(), options, IO{In: input, Out: &output, Err: &diagnostic}); code == 0 {
			t.Fatal("retired command accepted")
		}
		if calls.Load() != 0 || input.Len() != len("credential-sentinel") || strings.Contains(output.String()+diagnostic.String(), "credential-sentinel") {
			t.Fatal("unknown command acquired authority")
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("unknown command created server/client state", err)
		}
	}
	var output, diagnostic strings.Builder
	Run(context.Background(), []string{"--help"}, IO{In: strings.NewReader(""), Out: &output, Err: &diagnostic})
	if strings.Contains(strings.ToLower(output.String()+diagnostic.String()), "activity") {
		t.Fatal("help still exposes Activity")
	}
}
