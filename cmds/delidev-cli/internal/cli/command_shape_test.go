// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type commandShapeUnreadableInput struct{ reads int }

func (r *commandShapeUnreadableInput) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("command admission must not read input")
}

func TestUnknownNestedCommandsRejectBeforeAuthority(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer peer.Close()
	commands := []string{
		"usage bogus", "usage pricing bogus", "usage pricing", "usage pricing get bogus",
		"project bogus", "project prompt-history bogus", "agent bogus", "provider bogus",
		"account oauth bogus", "account oauth", "session terminal bogus", "session budget bogus",
		"session files bogus", "session forward bogus", "session pr bogus", "session review bogus",
		"session sidechat bogus", "github pr remediation bogus", "github pr problems bogus",
		"github issue bogus", "network profile bogus", "network profile", "network bogus",
		"server service bogus", "worker service bogus", "worker network bogus", "worker bogus",
		"connection bogus", "device bogus", "update native-bogus", "browser-storage bogus",
		"integration inspect-repository", "schedule bogus", "inbox bogus", "queue bogus",
		"backup bogus", "notification bogus", "storage bogus", "configuration bogus",
		"service-control bogus", "doctor bogus", "events bogus", "search bogus", "presentation bogus",
		"machine ssh", "machine ssh bogus", "machine ssh get bogus",
	}
	for _, command := range commands {
		for _, remote := range []bool{false, true} {
			t.Run(command+map[bool]string{false: "/no-endpoint", true: "/explicit-endpoint"}[remote], func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "uncreated")
				args := []string{"--data-dir", root, "--token-stdin"}
				if remote {
					args = append(args, "--server", peer.URL)
				}
				args = append(args, strings.Fields(command)...)
				input := &commandShapeUnreadableInput{}
				var out, diagnostic bytes.Buffer
				code := Run(context.Background(), args, IO{In: input, Out: &out, Err: &diagnostic})
				var result struct {
					Version int `json:"version"`
					Error   struct {
						Code    domain.Code `json:"code"`
						Message string      `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil || code != 2 || result.Version != 1 || result.Error.Code != domain.InvalidArgument || result.Error.Message != "Invalid command or arguments." {
					t.Fatalf("syntax did not win: exit=%d output=%s diagnostic=%s error=%v", code, out.String(), diagnostic.String(), err)
				}
				if input.reads != 0 || calls.Load() != 0 {
					t.Fatalf("invalid command acquired authority: reads=%d requests=%d", input.reads, calls.Load())
				}
				if _, err := os.Stat(root); !os.IsNotExist(err) {
					t.Fatalf("invalid command created state: %v", err)
				}
			})
		}
	}
}

func TestUnknownNestedCommandPrecedesLocalCredentialSelection(t *testing.T) {
	root := t.TempDir()
	// If connection setup selects this local scope, its malformed credential
	// metadata produces a different error before authentication input is read.
	credential := filepath.Join(root, "device.json")
	const original = "invalid credential metadata"
	if err := os.WriteFile(credential, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	code, result := cliRun(t, root, []string{"usage", "pricing", "bogus"}, "")
	if code != 2 || result["error"].(map[string]any)["message"] != "Invalid command or arguments." {
		t.Fatalf("local credentials selected before syntax: %d %+v", code, result)
	}
	raw, err := os.ReadFile(credential)
	if err != nil || string(raw) != original {
		t.Fatal("invalid command changed existing state", err)
	}
}

func TestCommandShapeRetainsSpecializedAndGenericPaths(t *testing.T) {
	for _, command := range []string{
		"usage summary", "usage pricing get", "usage pricing version", "usage pricing set", "usage pricing mode", "usage pricing refresh",
		"account oauth complete", "account refresh-all-quotas", "account connect", "account list", "provider create", "provider inventory",
		"project prompt-history clear", "project save", "agent routing", "settings defaults", "settings get",
		"session sidechat", "session sidechat send", "session fork", "session terminal reattach", "session forward reconcile", "session files review-context", "session budget remove", "session review submit", "session get", "session snapshot",
		"github issue search", "github pr statuses", "github pr remediation capabilities", "github pr remediation fix", "github pr problems dismiss",
		"network profile save", "network select", "network export-bundle", "worker network import", "worker ssh-setup", "worker desktop-prepare",
		"worker service install", "server service remove", "server start", "server run", "server ensure", "server desktop-host", "server status", "server stop",
		"service-run", "service-control status", "machine ssh inspect", "machine ssh start", "machine ssh get", "machine ssh cancel", "machine ssh reconcile", "connection worker-network-prepare", "device recover-local", "device pair-local", "integration token-form",
		"queue edit", "queue snapshot", "inbox mark-unread", "inbox snapshot", "schedule occurrence", "schedule snapshot", "snapshot list", "snapshot get",
		"storage operation", "backup restore-status", "update native-inspect", "notification preferences", "model native-discover", "search", "events", "doctor",
	} {
		if err := validateCommandShape(strings.Fields(command)); err != nil {
			t.Errorf("existing path %q was rejected: %v", command, err)
		}
	}
	for _, command := range []string{"session save", "queue create", "schedule save", "inbox delete", "integration save", "network list", "update list", "project routing", "provider routing"} {
		if err := validateCommandShape(strings.Fields(command)); err == nil {
			t.Errorf("unreachable generic path %q was admitted", command)
		}
	}
	for command, paths := range commandPaths {
		for _, path := range paths {
			args := append([]string{command}, strings.Fields(path)...)
			if err := validateCommandShape(args); err != nil {
				t.Errorf("registered path %v was rejected: %v", args, err)
			}
			if err := validateCommandShape(append(args, "unknown-child")); err == nil {
				t.Errorf("unknown child of %v was admitted", args)
			}
		}
	}
}

func TestCommandShapePreservesFlagValues(t *testing.T) {
	for _, args := range [][]string{
		{"project", "create", "--input", "file with spaces.json"},
		{"session", "create", "--prompt", "not a subcommand"},
		{"usage", "pricing", "get", "--native-id", "nested native model name"},
		{"server", "start", "--listen=127.0.0.1:0"},
		{"worker", "network", "import", "--input", "-"},
	} {
		if err := validateCommandShape(args); err != nil {
			t.Errorf("flag values interpreted as command words: %v: %v", args, err)
		}
	}
}

func TestCommandShapeKeepsAuthenticatedReads(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.Handle(delidevv1connect.ResourceServiceListResourcesProcedure, connect.NewUnaryHandler(delidevv1connect.ResourceServiceListResourcesProcedure, func(_ context.Context, req *connect.Request[pb.ListResourcesRequest]) (*connect.Response[pb.ListResourcesResponse], error) {
		calls.Add(1)
		if req.Header().Get("Authorization") != "Bearer fixture-only" || req.Msg.Filter.Kind != pb.EntityKind_ENTITY_KIND_PROJECT {
			t.Error("authenticated read lost its credential or resource selection")
		}
		return connect.NewResponse(&pb.ListResourcesResponse{}), nil
	}))
	peer := httptest.NewServer(mux)
	defer peer.Close()
	root := filepath.Join(t.TempDir(), "uncreated")
	code, result := cliRun(t, root, []string{"--server", peer.URL, "--token-stdin", "project", "list"}, "fixture-only\n")
	if code != 0 || calls.Load() != 1 || result["version"] != float64(1) {
		t.Fatalf("valid authenticated command was rejected: %d %+v requests=%d", code, result, calls.Load())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("authenticated read created local state", err)
	}
}

func TestCommandShapeKeepsExplicitStartupDispatch(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uncreated")
	input := &commandShapeUnreadableInput{}
	var out bytes.Buffer
	code := Run(context.Background(), []string{"--data-dir", root, "server", "start", "--listen", "invalid-listener"}, IO{In: input, Out: &out, Err: io.Discard})
	if code == 0 || strings.Contains(out.String(), "Invalid command or arguments.") || input.reads != 0 {
		t.Fatalf("explicit startup did not reach its own validation: %d %s reads=%d", code, out.String(), input.reads)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("invalid startup listener created state", err)
	}
}
