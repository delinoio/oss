package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetiredRepositoryInspectionCommandRejectsWithoutRPC(t *testing.T) {
	calls := 0
	peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer peer.Close()
	var output, diagnostic strings.Builder
	code := Run(context.Background(), []string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", peer.URL, "--token-stdin", "integration", "inspect-repository", "--repository-id", "unused"}, IO{In: strings.NewReader("private-fixture-auth"), Out: &output, Err: &diagnostic})
	if code == 0 || calls != 0 {
		t.Fatalf("retired command: code=%d calls=%d", code, calls)
	}
	output.Reset()
	diagnostic.Reset()
	Run(context.Background(), []string{"--help"}, IO{Out: &output, Err: &diagnostic})
	if strings.Contains(output.String()+diagnostic.String(), "inspect-repository") {
		t.Fatal("retired command remains in help")
	}
}
