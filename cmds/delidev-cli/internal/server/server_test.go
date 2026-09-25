package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func runTestServer(t *testing.T, root string) (Endpoint, security.Identity, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan Endpoint, 1)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{DataDir: root, Listen: "127.0.0.1:0", AllowedOrigins: []string{"tauri://localhost"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, func(endpoint Endpoint) { ready <- endpoint })
	}()
	t.Cleanup(cancel)
	select {
	case endpoint := <-ready:
		identity, err := security.LoadIdentity(root)
		if err != nil {
			t.Fatal(err)
		}
		return endpoint, identity, cancel, done
	case err := <-done:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("server readiness timed out")
	}
	return Endpoint{}, security.Identity{}, cancel, done
}
func ownerRequest[T any](identity security.Identity, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+identity.Token)
	return request
}
func saveProvider(t *testing.T, ctx context.Context, identity security.Identity, client delidevv1connect.ConfigurationServiceClient, requestID domain.ID) *pb.SaveConfigurationResponse {
	t.Helper()
	raw, _ := json.Marshal(domain.Provider{Name: "local", Endpoint: "http://127.0.0.1:11434/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth})
	response, err := client.SaveConfiguration(ctx, ownerRequest(identity, &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(requestID)}, Kind: pb.EntityKind_ENTITY_KIND_PROVIDER, SchemaVersion: 1, DocumentJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg
}
func TestRealConnectAuthenticationOriginsAndRedaction(t *testing.T) {
	endpoint, identity, stop, done := runTestServer(t, filepath.Join(t.TempDir(), "state"))
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	client := delidevv1connect.NewSystemServiceClient(http.DefaultClient, endpoint.URL)
	if _, err := client.GetStatus(context.Background(), connect.NewRequest(&pb.GetStatusRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous allowed: %v", err)
	}
	for _, origin := range []string{"https://evil.example", "null", "http://tauri.localhost"} {
		request := ownerRequest(identity, &pb.GetStatusRequest{})
		request.Header().Set("Origin", origin)
		if _, err := client.GetStatus(context.Background(), request); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("origin %s accepted: %v", origin, err)
		}
	}
	request := ownerRequest(identity, &pb.GetStatusRequest{})
	request.Header().Set("Origin", "tauri://localhost")
	response, err := client.GetStatus(context.Background(), request)
	if err != nil || response.Msg.ServerId != string(identity.ServerID) {
		t.Fatalf("trusted client failed: %+v %v", response, err)
	}
	if response.Header().Get("X-Delidev-Correlation-Id") == "" {
		t.Fatal("missing correlation")
	}
	raw, _ := json.Marshal(response.Msg)
	if bytes.Contains(raw, []byte(identity.Token)) {
		t.Fatal("status leaked owner token")
	}
	hostile, err := http.NewRequest(http.MethodPost, endpoint.URL+delidevv1connect.SystemServiceGetStatusProcedure, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	hostile.Host = "evil.example:46310"
	hostile.Header.Set("Content-Type", "application/json")
	hostile.Header.Set("Authorization", "Bearer "+identity.Token)
	result, err := http.DefaultClient.Do(hostile)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusForbidden {
		t.Fatalf("DNS rebinding accepted: %d", result.StatusCode)
	}
}
func TestConnectSnapshotReplayRestartAndBackupDeduplication(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	endpoint, identity, stop, done := runTestServer(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, endpoint.URL)
	configuration := delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, endpoint.URL)
	snapshot, err := resources.GetSnapshot(ctx, ownerRequest(identity, &pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_PROVIDER, PageSize: 10}}))
	if err != nil {
		t.Fatal(err)
	}
	requestID := domain.NewID()
	saved := saveProvider(t, ctx, identity, configuration, requestID)
	stream, err := resources.WatchEvents(ctx, ownerRequest(identity, &pb.WatchEventsRequest{Cursor: snapshot.Msg.Cursor}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() {
		t.Fatal("event not replayed", stream.Err())
	}
	event := stream.Msg()
	if event.EntityId != saved.Resource.Id {
		t.Fatalf("wrong event: %+v", event)
	}
	stream.Close()
	replayed := saveProvider(t, ctx, identity, configuration, requestID)
	if !replayed.Replayed || replayed.Resource.Id != saved.Resource.Id {
		t.Fatal("mutation retry duplicated provider")
	}
	system := delidevv1connect.NewSystemServiceClient(http.DefaultClient, endpoint.URL)
	backupID := domain.NewID()
	backup, err := system.CreateBackup(ctx, ownerRequest(identity, &pb.CreateBackupRequest{RequestId: string(backupID)}))
	if err != nil {
		t.Fatal(err)
	}
	again, err := system.CreateBackup(ctx, ownerRequest(identity, &pb.CreateBackupRequest{RequestId: string(backupID)}))
	if err != nil || again.Msg.Id != backup.Msg.Id || !again.Msg.Replayed {
		t.Fatalf("backup retry duplicated: %+v %v", again, err)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	endpoint, identity2, stop, done := runTestServer(t, root)
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	if identity2 != identity {
		t.Fatal("server identity changed on restart")
	}
	configuration = delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, endpoint.URL)
	replayed = saveProvider(t, ctx, identity2, configuration, requestID)
	if !replayed.Replayed || replayed.Resource.Id != saved.Resource.Id {
		t.Fatal("restart lost receipt")
	}
}
func TestRemoteListenerRequiresExplicitTLS(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:46310", "[::]:46310", "192.0.2.1:46310"} {
		if err := ValidateConfig(Config{Listen: listen}); domain.SafeError(err).Code != domain.PermissionDenied {
			t.Fatalf("remote plaintext accepted: %s %v", listen, err)
		}
	}
	for _, listen := range []string{"127.0.0.1:0", "[::1]:0"} {
		if err := ValidateConfig(Config{Listen: listen}); err != nil {
			t.Fatal(err)
		}
	}
	for _, origin := range []string{"*", "https://example.org/path", "https://user:secret@example.org"} {
		if err := ValidateConfig(Config{Listen: "127.0.0.1:0", AllowedOrigins: []string{origin}}); err == nil {
			t.Fatal("invalid origin accepted")
		}
	}
}
