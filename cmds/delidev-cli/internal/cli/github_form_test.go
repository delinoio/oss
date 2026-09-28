package cli

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIGitHubTokenFormBindsScopeAndExactRevision(t *testing.T) {
	for _, bad := range []string{"", "url", "profile", "revision"} {
		t.Run(bad, func(t *testing.T) {
			id := domain.NewID()
			peer := httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.IntegrationServiceGetGitHubTokenFormProcedure, func(_ context.Context, r *connect.Request[pb.GetGitHubTokenFormRequest]) (*connect.Response[pb.GetGitHubTokenFormResponse], error) {
				if r.Msg.ProfileId != string(id) || r.Msg.ExpectedRevision != 9007199254740993 || r.Msg.Access != pb.GitHubTokenAccess_GIT_HUB_TOKEN_ACCESS_PRIVATE_REPOSITORIES {
					t.Error("changed form request")
				}
				u, _ := domain.GitHubTokenFormURL(domain.ClassicPAT, "", domain.GitHubPrivateRepositories)
				v := domain.GitHubTokenForm{ProfileID: id, ProfileRevision: "9007199254740993", TokenKind: domain.ClassicPAT, Access: domain.GitHubPrivateRepositories, URL: u}
				switch bad {
				case "url":
					v.URL += "&redirect_uri=https://example.com"
				case "profile":
					v.ProfileID = domain.NewID()
				case "revision":
					v.ProfileRevision = "9007199254740992"
				}
				raw, _ := json.Marshal(v)
				return connect.NewResponse(&pb.GetGitHubTokenFormResponse{SchemaVersion: 1, DocumentJson: raw}), nil
			}))
			defer peer.Close()
			var out, diagnostic strings.Builder
			code := Run(context.Background(), []string{"--data-dir", filepath.Join(t.TempDir(), "unused"), "--server", peer.URL, "--token-stdin", "integration", "token-form", "--id", string(id), "--revision", "9007199254740993", "--access", "private-repositories"}, IO{In: strings.NewReader("fixture-secret"), Out: &out, Err: &diagnostic})
			if (code == 0) != (bad == "") {
				t.Fatal(code, out.String())
			}
			if bad == "" && (!strings.Contains(out.String(), "broad read/write") || !strings.Contains(out.String(), `"dispatched":false`)) {
				t.Fatal("missing scope/dispatch guidance", out.String())
			}
			if strings.Contains(out.String(), "fixture-secret") {
				t.Fatal("credential escaped")
			}
		})
	}
}

func TestCLIGitHubPresentationStdinIsBoundedValidatedAndDoesNotBootstrap(t *testing.T) {
	url := "https://github.com/owner/repo/pull/1"
	for _, suffix := range []string{"", "\n", "\r\n"} {
		got, err := readGitHubPresentationInput(strings.NewReader(url + suffix))
		if err != nil || got != url {
			t.Fatal("valid stdin rejected", err)
		}
	}
	for _, input := range []string{"", url + "\n\n", url + "?token=fixture-private-value", strings.Repeat("x", 2051)} {
		root := filepath.Join(t.TempDir(), "unused")
		var out, diagnostic strings.Builder
		code := Run(context.Background(), []string{"--data-dir", root, "presentation", "open-github", "--url-stdin"}, IO{In: strings.NewReader(input), Out: &out, Err: &diagnostic})
		if code == 0 || strings.Contains(out.String(), "fixture-private-value") {
			t.Fatal("invalid presentation input escaped")
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("presentation bootstrapped server state", err)
		}
	}
}
