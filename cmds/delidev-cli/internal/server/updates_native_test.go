// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"github.com/gowebpki/jcs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManualNativeSignedWorkerReplacement(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_WORKER_UPDATE") != "1" {
		t.Skip("Opt in to temporary actual replacement binaries and test signing authority.")
	}
	for _, mode := range []string{"success", "failed-start-rollback", "lost-claim-response", "lost-success-report"} {
		t.Run(mode, func(t *testing.T) { testNativeWorkerReplacement(t, mode) })
	}
}
func testNativeWorkerReplacement(t *testing.T, mode string) {
	rollback := mode == "failed-start-rollback"
	if os.Getenv("DELIDEV_NATIVE_WORKER_UPDATE") != "1" {
		t.Skip("Opt in to isolated signed test-root overlay, actual binaries and detached generations.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	directory := t.TempDir()
	repo, e := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if e != nil {
		t.Fatal(e)
	}
	var data []byte
	var mu sync.RWMutex
	download := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Write(data)
	}))
	defer download.Close()
	public, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	root := updates.Root{SchemaVersion: 1, KeyID: "delidev-release-root-v1", PublicKey: base64.StdEncoding.EncodeToString(public), ProductionReady: true}
	raw, _ := json.Marshal(root)
	replacementRoot := filepath.Join(directory, "trust-root.json")
	os.WriteFile(replacementRoot, raw, 0600)
	source, e := os.ReadFile(filepath.Join(repo, "cmds/delidev-cli/internal/updates/download.go"))
	if e != nil {
		t.Fatal(e)
	}
	patched := strings.Replace(string(source), "return &Client{v, newHTTPClient()}, nil", "return &Client{v, &http.Client{Transport:updateFixtureTransport{},Timeout:time.Minute}}, nil", 1)
	patched += `\n`
	patched = strings.TrimSuffix(patched, `\n`) + fmt.Sprintf("\ntype updateFixtureTransport struct{}\nfunc (updateFixtureTransport) RoundTrip(r *http.Request)(*http.Response,error){copy:=r.Clone(r.Context());u,_:=url.Parse(%q);copy.URL=&url.URL{Scheme:u.Scheme,Host:u.Host,Path:r.URL.Path};return http.DefaultTransport.RoundTrip(copy)}\n", download.URL)
	replacementGo := filepath.Join(directory, "download.go")
	os.WriteFile(replacementGo, []byte(patched), 0600)
	replacements := map[string]string{filepath.Join(repo, "cmds/delidev-cli/internal/updates/trust-root.json"): replacementRoot, filepath.Join(repo, "cmds/delidev-cli/internal/updates/download.go"): replacementGo}
	if mode != "success" {
		file := filepath.Join(repo, "cmds/delidev-cli/internal/worker/updates.go")
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		patched := string(raw)
		if rollback {
			patched = strings.Replace(patched, "func settleUpdateOnAttach(ctx context.Context, root string, credential Credential, instance domain.ID) error {", "func settleUpdateOnAttach(ctx context.Context, root string, credential Credential, instance domain.ID) error { if rpc.Version==\"0.2.0\" {return updateFailure()}", 1)
		}
		if mode == "lost-claim-response" {
			patched = strings.Replace(patched, "if claimed.Msg.Update == nil", `if problem==nil { marker:=filepath.Join(config.Root,"test-claim-response-loss");if _,e:=os.Stat(marker);os.IsNotExist(e){os.WriteFile(marker,[]byte("lost"),0600);return updateFailure()} }; if claimed.Msg.Update == nil`, 1)
		}
		if mode == "lost-success-report" {
			patched = strings.Replace(patched, "j.Phase = UpdateComplete\n\treturn WriteUpdateJournal", `if j.Outcome==pb.WorkerUpdateOutcome_WORKER_UPDATE_OUTCOME_SUCCEEDED {marker:=filepath.Join(root,"test-success-report-loss");if _,e:=os.Stat(marker);os.IsNotExist(e){os.WriteFile(marker,[]byte("lost"),0600);return updateFailure()}}; j.Phase = UpdateComplete`+"\n\treturn WriteUpdateJournal", 1)
		}
		path := filepath.Join(directory, "worker-updates.go")
		os.WriteFile(path, []byte(patched), 0600)
		replacements[file] = path
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": replacements})
	overlayPath := filepath.Join(directory, "overlay.json")
	os.WriteFile(overlayPath, overlay, 0600)
	build := func(version string) string {
		path := filepath.Join(directory, "worker-"+version)
		cmd := exec.CommandContext(ctx, "go", "build", "-overlay", overlayPath, "-ldflags", "-X github.com/delinoio/oss/cmds/delidev-cli/internal/rpc.Version="+version+" -X github.com/delinoio/oss/cmds/delidev-cli/internal/rpc.SourceRevision="+strings.Repeat("a", 40), "-o", path, "./cmds/delidev-cli")
		cmd.Dir = repo
		if output, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("Test-overlay binary build failed: %v\n%s", e, output)
		}
		os.Chmod(path, 0700)
		return path
	}
	oldPath, newPath := build(rpc.Version), build("0.2.0")
	mu.Lock()
	data, e = os.ReadFile(newPath)
	mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(data)
	target, e := updates.SelectTarget(runtime.GOOS, runtime.GOARCH)
	if e != nil {
		t.Fatal(e)
	}
	payload := updates.Payload{SchemaVersion: 1, ProtocolVersion: 1, Version: "0.2.0", SourceRevision: strings.Repeat("a", 40), PublishedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, c := range []updates.Component{updates.Desktop, updates.Worker} {
		for _, target := range updates.Targets {
			name := updates.ArtifactName(c, target)
			payload.Artifacts = append(payload.Artifacts, updates.Artifact{Component: c, Target: target, Name: name, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), URL: updates.ReleaseURL(payload.Version, name)})
		}
	}
	payloadRaw, _ := json.Marshal(payload)
	canon, e := jcs.Transform(payloadRaw)
	if e != nil {
		t.Fatal(e)
	}
	manifest, _ := json.Marshal(updates.Manifest{KeyID: root.KeyID, Payload: canon, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte(updates.SignatureDomain), canon...)))})
	manifestHash := sha256.Sum256(manifest)
	candidate := updates.Verified{Payload: payload, Canonical: manifest, ManifestSHA256: hex.EncodeToString(manifestHash[:])}
	verifyFixture := func(raw []byte, current string, now time.Time) (updates.Verified, error) {
		if !bytes.Equal(raw, manifest) || current != rpc.Version {
			return updates.Verified{}, installationFailure(domain.PermissionDenied)
		}
		return candidate, nil
	}
	serverRoot := filepath.Join(directory, "server")
	logs := &installationTestLog{}
	ready := make(chan Endpoint, 1)
	done := make(chan error, 1)
	serverCtx, stopServer := context.WithCancel(ctx)
	go func() {
		done <- Serve(serverCtx, Config{DataDir: serverRoot, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(logs, nil)), disableCatalogMaintenance: true, accountSecrets: &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}, releaseFactory: func() (releaseClient, error) { return fixtureRelease{candidate: candidate}, nil }, releaseVerifier: verifyFixture}, func(e Endpoint) { ready <- e })
	}()
	var endpoint Endpoint
	select {
	case endpoint = <-ready:
	case e := <-done:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer func() {
		stopServer()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	owner, e := security.LoadIdentity(serverRoot)
	if e != nil {
		t.Fatal(e)
	}
	h, tr := rpc.HTTPClient()
	defer tr.CloseIdleConnections()
	dc := delidevv1connect.NewDeviceServiceClient(h, endpoint.URL)
	uc := delidevv1connect.NewInstallationServiceClient(h, endpoint.URL)
	code, _ := worker.RandomToken()
	hash := sha256.Sum256([]byte(code))
	grant, e := dc.CreatePairing(ctx, installationTestRequest(owner, &pb.CreatePairingRequest{RequestId: string(domain.NewID()), Type: pb.DeviceType_DEVICE_TYPE_WORKER, Name: "Native update fixture", CodeDigest: hash[:]}))
	if e != nil {
		t.Fatal(e)
	}
	workerRoot := filepath.Join(directory, "worker")
	credential, e := worker.Pair(ctx, workerRoot, worker.PairingCode{Version: 1, PairingID: domain.ID(grant.Msg.Pairing.Id), ServerID: endpoint.ServerID, Endpoint: endpoint.URL, Code: code}, domain.WorkerDevice, "Native update fixture")
	if e != nil {
		t.Fatal(e)
	}
	run := func(path string, args ...string) error {
		cmd := exec.CommandContext(ctx, path, append([]string{"--json", "--data-dir", serverRoot}, args...)...)
		cmd.Env = append(os.Environ(), "DELIDEV_EXECUTION_TOKEN_SENTINEL=not-in-child")
		cmd.Stdout = &bytes.Buffer{}
		cmd.Stderr = &bytes.Buffer{}
		return cmd.Run()
	}
	if e = run(oldPath, "worker", "start", "--worker-dir", workerRoot, "--detach"); e != nil {
		t.Fatal("Original Worker start", e)
	}
	old, e := worker.Status(workerRoot)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		status, e := worker.Status(workerRoot)
		if e == nil && status.Lifecycle.Generation != "" {
			if e = worker.RequestStop(workerRoot, status.Lifecycle.Generation); e != nil {
				t.Error(e)
			}
			limit := time.Now().Add(15 * time.Second)
			for time.Now().Before(limit) {
				s, e := worker.Status(workerRoot)
				if e == nil && s.State == worker.StateExited {
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Error("Fixture generation did not exit")
		}
	}()
	machine, e := storeMachineForUpdate(ctx, endpoint, owner, credential.MachineID)
	if e != nil {
		t.Fatal(e)
	}
	var targetEnum pb.UpdateTarget
	for i, v := range updates.Targets {
		if target == v {
			targetEnum = pb.UpdateTarget(i + 1)
		}
	}
	observed, e := uc.CheckUpdate(ctx, installationTestRequest(owner, &pb.CheckUpdateRequest{RequestId: string(domain.NewID()), Component: pb.UpdateComponent_UPDATE_COMPONENT_WORKER, Target: targetEnum, CurrentVersion: rpc.Version, MachineId: string(credential.MachineID), ExpectedMachineRevision: machine.Revision}))
	if e != nil {
		t.Fatal(e)
	}
	accepted, e := uc.RequestWorkerUpdate(ctx, installationTestRequest(owner, &pb.RequestWorkerUpdateRequest{Mutation: &pb.Mutation{Id: observed.Msg.Update.Id, ExpectedRevision: observed.Msg.Update.Revision, RequestId: string(domain.NewID())}}))
	if e != nil {
		t.Fatal(e)
	}
	restarted := false
	for {
		if strings.HasPrefix(mode, "lost-") && !restarted {
			status, problem := worker.Status(workerRoot)
			marker := "test-claim-response-loss"
			if mode == "lost-success-report" {
				marker = "test-success-report-loss"
			}
			_, markerError := os.Stat(filepath.Join(workerRoot, marker))
			if markerError == nil && problem == nil && status.State == worker.StateExited && status.Lifecycle.Generation != "" {
				// Wait for the original owned helper to join after the injected
				// response loss; an earlier idle exit is still handoff-owned.
				time.Sleep(time.Second)
				prior := status.Lifecycle.Generation
				_ = run(oldPath, "worker", "start", "--worker-dir", workerRoot, "--detach")
				now, problem := worker.Status(workerRoot)
				restarted = problem == nil && now.Lifecycle.Generation != prior
			}
		}
		current, e := uc.GetUpdate(ctx, installationTestRequest(owner, &pb.GetUpdateRequest{Id: accepted.Msg.Update.Id}))
		if e != nil {
			t.Fatal(e)
		}
		var op updates.Operation
		if domain.Decode(current.Msg.Update.DocumentJson, &op) != nil {
			t.Fatal("Malformed update")
		}
		if (!rollback && op.State == updates.Succeeded) || (rollback && op.State == updates.Failed) {
			j, problem := worker.ReadUpdateJournal(workerRoot, domain.ID(accepted.Msg.Update.Id))
			status, statusError := worker.Status(workerRoot)
			if problem == nil && j.Phase == worker.UpdateComplete && statusError == nil && status.State == worker.StateRunning {
				break
			}
		}
		if op.State == updates.Failed || op.State == updates.Uncertain {
			t.Fatalf("Original update outcome %s; %s", op.State, logs.Snapshot())
		}
		select {
		case <-ctx.Done():
			raw, _ := os.ReadFile(filepath.Join(workerRoot, "worker.log"))
			t.Fatalf("Native replacement timeout: %s\n%s", logs.Snapshot(), raw)
		case <-time.After(100 * time.Millisecond):
		}
	}
	now, e := worker.Status(workerRoot)
	if e != nil || now.State != worker.StateRunning || now.Lifecycle.Generation == old.Lifecycle.Generation {
		t.Fatal("No distinct positively ready replacement", e)
	}
	after, e := worker.LoadCredential(workerRoot)
	if e != nil || after != credential {
		t.Fatal("Replacement changed registration", e)
	}
	journal, e := worker.ReadUpdateJournal(workerRoot, domain.ID(accepted.Msg.Update.Id))
	if e != nil || journal.Phase != worker.UpdateComplete || (!rollback && mode != "lost-success-report" && journal.NewGeneration != now.Lifecycle.Generation) || (rollback && journal.RollbackGeneration != now.Lifecycle.Generation) || journal.PreviousPath == "" {
		t.Fatal("Original native outcome missing", e)
	}
	expectedVersion := "0.2.0"
	if rollback {
		expectedVersion = rpc.Version
	}
	machine, e = storeMachineForUpdate(ctx, endpoint, owner, credential.MachineID)
	if e != nil {
		t.Fatal(e)
	}
	var actual domain.Machine
	if domain.Decode(machine.DocumentJson, &actual) != nil || actual.Version != expectedVersion {
		t.Fatal("Wrong retained version", e)
	}
	if _, e = os.Stat(journal.PreviousPath); e != nil {
		t.Fatal("Previous working binary lost", e)
	}
	// An older bundled CLI must ask the verified installed controller to admit
	// its own version, and must reuse a live original generation without spawn.
	if e = run(oldPath, "worker", "start", "--worker-dir", workerRoot, "--detach"); e != nil {
		t.Fatal("Stale CLI could not reuse the installed Worker", e)
	}
	reused, e := worker.Status(workerRoot)
	if e != nil || reused.Lifecycle.Generation != now.Lifecycle.Generation || reused.Lifecycle.WorkerVersion != expectedVersion {
		t.Fatal("Stale CLI reserved a different native generation/version", e)
	}
	if e = run(newPath, "worker", "update-replace", "--worker-dir", workerRoot, "--operation-id", accepted.Msg.Update.Id); e == nil {
		t.Fatal("Completed installation was resent")
	}
	stable, _ := worker.Status(workerRoot)
	if stable.Lifecycle.Generation != now.Lifecycle.Generation {
		t.Fatal("Replay replaced running generation")
	}
}
func storeMachineForUpdate(ctx context.Context, endpoint Endpoint, owner security.Identity, id domain.ID) (*pb.Resource, error) {
	h, tr := rpc.HTTPClient()
	defer tr.CloseIdleConnections()
	r, e := delidevv1connect.NewResourceServiceClient(h, endpoint.URL).GetResource(ctx, installationTestRequest(owner, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: string(id)}))
	if e != nil {
		return nil, e
	}
	return r.Msg.Resource, nil
}
