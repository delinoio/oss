package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func workerSubscriptionBundle(rotation string) []byte {
	claims, _ := json.Marshal(map[string]any{"email": "fixture@example.invalid", "nonce": rotation, "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "worker-account", "chatgpt_user_id": "worker-user", "chatgpt_plan_type": "plus"}})
	token := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(claims) + ".synthetic-signature"
	at := time.Now().UTC().Add(-time.Minute)
	if rotation != "first" {
		at = time.Now().UTC()
	}
	raw, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "tokens": map[string]string{"id_token": token, "access_token": token, "refresh_token": "synthetic-worker-refresh-" + rotation, "account_id": "worker-account"}, "last_refresh": at})
	return raw
}

type managedWorkerRPC struct {
	delidevv1connect.SubscriptionServiceClient
	t                      *testing.T
	root, executable, mode string
	lease                  domain.ID
	bundle                 []byte
	finish                 *pb.FinishSubscriptionRequest
	finishBundle           []byte
	progress               bool
}

func (f *managedWorkerRPC) TakeSubscription(_ context.Context, req *connect.Request[pb.TakeSubscriptionRequest]) (*connect.Response[pb.TakeSubscriptionResponse], error) {
	f.lease = domain.ID(req.Msg.Mutation.RequestId)
	if _, err := security.ReadPrivate(filepath.Join(f.root, "managed-auth", string(f.lease)+".json"), 4096); err != nil {
		f.t.Fatal("protected delivery preceded durable claim", err)
	}
	installation, _ := json.Marshal(domain.Installation{Harness: domain.Codex, Version: domain.CodexProtocolVersion, ResolvedPath: f.executable})
	generation := ""
	if len(f.bundle) > 0 {
		generation = string(domain.NewID())
	}
	return connect.NewResponse(&pb.TakeSubscriptionResponse{LeaseId: string(f.lease), GenerationId: generation, Bundle: bytes.Clone(f.bundle), LeaseRevision: 3, InstallationJson: installation}), nil
}

func (f *managedWorkerRPC) PublishSubscriptionProgress(_ context.Context, req *connect.Request[pb.PublishSubscriptionProgressRequest]) (*connect.Response[pb.PublishSubscriptionProgressResponse], error) {
	if req.Msg.Url == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}
	f.progress = true
	return connect.NewResponse(&pb.PublishSubscriptionProgressResponse{Canceled: f.mode == "cancel"}), nil
}

func (f *managedWorkerRPC) FinishSubscription(_ context.Context, req *connect.Request[pb.FinishSubscriptionRequest]) (*connect.Response[pb.FinishSubscriptionResponse], error) {
	if !req.Msg.CleanupConfirmed {
		f.t.Error("controlled process/home cleanup was not confirmed")
	}
	if _, err := os.Lstat(filepath.Join(f.root, "managed-auth", string(f.lease))); !os.IsNotExist(err) {
		f.t.Error("completion preceded owned home removal", err)
	}
	f.finish = &pb.FinishSubscriptionRequest{Mutation: req.Msg.Mutation, GenerationId: req.Msg.GenerationId, CleanupConfirmed: req.Msg.CleanupConfirmed, RefreshConfirmed: req.Msg.RefreshConfirmed, Succeeded: req.Msg.Succeeded}
	f.finishBundle = bytes.Clone(req.Msg.Bundle)
	if f.mode == "lost-upload" {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	return connect.NewResponse(&pb.FinishSubscriptionResponse{}), nil
}

func TestManagedExecutionAuthenticationCleanup(t *testing.T) {
	for _, mode := range []string{"clean", "copied-token", "symlink", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "codex")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			bundle := workerSubscriptionBundle("first")
			defer clear(bundle)
			if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
				t.Fatal(err)
			}
			if mode == "copied-token" {
				b, _, _ := subscription.Parse(bundle)
				if err := os.WriteFile(filepath.Join(home, "history.jsonl"), []byte(b.Tokens.Refresh), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "symlink" {
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(home, "history")); err != nil {
					t.Skip("symlinks unavailable")
				}
			}
			if mode == "oversized" {
				file, err := os.Create(filepath.Join(home, "history"))
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate((16 << 20) + 1); err != nil {
					t.Fatal(err)
				}
				file.Close()
			}
			err := cleanupExecutionAuthentication(home, bundle, bundle)
			if (err == nil) != (mode == "clean") {
				t.Fatalf("cleanup %s: %v", mode, err)
			}
		})
	}
}

func assertManagedWorkerFilesRedacted(t *testing.T, root string, bundles ...[]byte) {
	t.Helper()
	var needles [][]byte
	for _, raw := range bundles {
		b, _, err := subscription.Parse(raw)
		if err != nil {
			continue
		}
		for _, value := range []string{b.Tokens.ID, b.Tokens.Access, b.Tokens.Refresh} {
			needles = append(needles, []byte(value))
		}
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, needle := range needles {
			if bytes.Contains(raw, needle) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(needle))) {
				t.Errorf("protected token retained in %s", entry.Name())
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
