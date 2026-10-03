// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/sshsetup"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"golang.org/x/crypto/ssh"
)

type installationTestLog struct {
	sync.Mutex
	bytes.Buffer
}

func (l *installationTestLog) Write(p []byte) (int, error) {
	l.Lock()
	defer l.Unlock()
	return l.Buffer.Write(p)
}
func (l *installationTestLog) Snapshot() string { l.Lock(); defer l.Unlock(); return l.Buffer.String() }

type fixtureRelease struct {
	source    string
	candidate updates.Verified
}

func (f fixtureRelease) Latest(context.Context, string, time.Time) (updates.Verified, error) {
	return f.candidate, nil
}
func (f fixtureRelease) Download(context.Context, updates.Verified, updates.Component, updates.Target, string) (string, error) {
	return f.source, nil
}
func (f fixtureRelease) Close() {}

type installationSSHPeer struct {
	listener    net.Listener
	home        string
	target      sshsetup.Target
	mu          sync.Mutex
	connections []net.Conn
	wg          sync.WaitGroup
	setupSends  atomic.Int64
	stageSends  atomic.Int64
	commands    atomic.Int64
}

func newInstallationSSHPeer(t *testing.T) *installationSSHPeer {
	t.Helper()
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := ssh.NewSignerFromKey(key)
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	p := &installationSSHPeer{listener: listener, home: t.TempDir(), target: sshsetup.Target{Host: "127.0.0.1", Port: uint16(listener.Addr().(*net.TCPAddr).Port), User: "fixture-user"}}
	config := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, secret []byte) (*ssh.Permissions, error) {
		if string(secret) != "ssh-secret-sentinel" {
			return nil, io.EOF
		}
		return &ssh.Permissions{}, nil
	}}
	config.AddHostKey(signer)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			p.mu.Lock()
			p.connections = append(p.connections, conn)
			p.mu.Unlock()
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				defer conn.Close()
				server, channels, requests, e := ssh.NewServerConn(conn, config)
				if e != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					if incoming.ChannelType() != "session" {
						incoming.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					ch, requests, e := incoming.Accept()
					if e != nil {
						return
					}
					p.wg.Add(1)
					go func() {
						defer p.wg.Done()
						defer ch.Close()
						for r := range requests {
							if r.Type != "exec" {
								r.Reply(false, nil)
								continue
							}
							var input struct{ Command string }
							if ssh.Unmarshal(r.Payload, &input) != nil {
								return
							}
							p.commands.Add(1)
							if strings.Contains(input.Command, "DELIDEV_STAGE_V1") {
								p.stageSends.Add(1)
							}
							if strings.Contains(input.Command, "worker ssh-setup") && !strings.Contains(input.Command, "--inspect-only") {
								p.setupSends.Add(1)
							}
							r.Reply(true, nil)
							cmd := exec.Command("/bin/sh", "-c", input.Command)
							cmd.Env = []string{"HOME=" + p.home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + p.home}
							cmd.Stdin = ch
							cmd.Stdout = ch
							cmd.Stderr = ch.Stderr()
							err := cmd.Run()
							code := uint32(0)
							if err != nil {
								code = 1
							}
							var status [4]byte
							binary.BigEndian.PutUint32(status[:], code)
							ch.SendRequest("exit-status", false, status[:])
							return
						}
					}()
				}
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		p.mu.Lock()
		for _, c := range p.connections {
			c.Close()
		}
		p.mu.Unlock()
		p.wg.Wait()
	})
	return p
}
func TestManualNativeSSHWorkerSetup(t *testing.T) {
	if os.Getenv("DELIDEV_NATIVE_SSH_SETUP") != "1" {
		t.Skip("Opt in to isolated actual SSH/CLI/process setup; no user SSH credentials are accessed.")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("This acceptance fixture uses the actual macOS target probes.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	directory := t.TempDir()
	binaryPath := filepath.Join(directory, "delidev-worker")
	revision := strings.Repeat("a", 40)
	build := exec.CommandContext(ctx, "go", "build", "-ldflags", "-X github.com/delinoio/oss/cmds/delidev-cli/internal/rpc.Version=0.1.0 -X github.com/delinoio/oss/cmds/delidev-cli/internal/rpc.SourceRevision="+revision, "-o", binaryPath, "./cmds/delidev-cli")
	build.Dir = filepath.Join("..", "..", "..", "..")
	if _, e := build.CombinedOutput(); e != nil {
		t.Fatal("Fixture binary build failed", e)
	}
	if e := os.Chmod(binaryPath, 0700); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(binaryPath)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	size := int64(len(raw))
	clear(raw)
	payload := updates.Payload{SchemaVersion: 1, ProtocolVersion: 1, Version: "0.1.0", SourceRevision: revision, PublishedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, c := range []updates.Component{updates.Desktop, updates.Worker} {
		for _, target := range updates.Targets {
			name := updates.ArtifactName(c, target)
			payload.Artifacts = append(payload.Artifacts, updates.Artifact{Component: c, Target: target, Name: name, Size: size, SHA256: hex.EncodeToString(sum[:]), URL: updates.ReleaseURL(payload.Version, name)})
		}
	}
	logs := &installationTestLog{}
	release := fixtureRelease{binaryPath, updates.Verified{Payload: payload}}
	secrets := &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}
	root := filepath.Join(directory, "server")
	ready := make(chan Endpoint, 1)
	done := make(chan error, 1)
	serverCtx, stop := context.WithCancel(ctx)
	go func() {
		done <- Serve(serverCtx, Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(logs, nil)), accountSecrets: secrets, disableCatalogMaintenance: true, releaseFactory: func() (releaseClient, error) { return release, nil }}, func(e Endpoint) { ready <- e })
	}()
	var endpoint Endpoint
	select {
	case endpoint = <-ready:
	case e := <-done:
		t.Fatal("Fixture server failed", e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	t.Cleanup(func() {
		stop()
		if e := <-done; e != nil {
			t.Error(e)
		}
	})
	owner, e := security.LoadIdentity(root)
	if e != nil {
		t.Fatal(e)
	}
	httpClient, transport := rpc.HTTPClient()
	defer transport.CloseIdleConnections()
	client := delidevv1connect.NewInstallationServiceClient(httpClient, endpoint.URL)
	peer := newInstallationSSHPeer(t)
	remoteRoot := filepath.Join(peer.home, ".local", "share", "delidev", "ssh-workers", string(endpoint.ServerID))
	t.Cleanup(func() {
		status, e := worker.Status(remoteRoot)
		if e != nil || status.Lifecycle.Generation == "" {
			return
		}
		if e = worker.RequestStop(remoteRoot, status.Lifecycle.Generation); e != nil {
			t.Error(e)
			return
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			status, e = worker.Status(remoteRoot)
			if e == nil && status.State == worker.StateExited {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Error("Owned fixture Worker exit not observed")
	})
	requestID := string(domain.NewID())
	inspect, e := client.InspectSSHHost(ctx, installationTestRequest(owner, &pb.InspectSSHHostRequest{RequestId: requestID, Host: peer.target.Host, Port: uint32(peer.target.Port), User: peer.target.User}))
	if e != nil {
		t.Fatal(e)
	}
	var observed sshOperation
	if domain.Decode(inspect.Msg.Setup.DocumentJson, &observed) != nil {
		t.Fatal("Malformed setup")
	}
	credential, _ := json.Marshal(sshsetup.Credential{Method: sshsetup.Password, Secret: []byte("ssh-secret-sentinel")})
	startID := string(domain.NewID())
	startRequest := &pb.StartSSHSetupRequest{Mutation: &pb.Mutation{RequestId: startID, Id: inspect.Msg.Setup.Id, ExpectedRevision: inspect.Msg.Setup.Revision}, Credential: append([]byte(nil), credential...), Name: "SSH fixture Runner", ConfirmedFingerprint: observed.Identity.Fingerprint}
	accepted, e := client.StartSSHSetup(ctx, installationTestRequest(owner, startRequest))
	if e != nil {
		t.Fatal(e)
	}
	_ = accepted
	var result sshOperation
	var current *pb.Resource
	for {
		response, e := client.GetSSHSetup(ctx, installationTestRequest(owner, &pb.GetSSHSetupRequest{Id: inspect.Msg.Setup.Id}))
		if e != nil {
			t.Fatal(e)
		}
		current = response.Msg.Setup
		result = sshOperation{}
		if domain.Decode(current.DocumentJson, &result) != nil {
			t.Fatal("Malformed setup progress")
		}
		if result.State == installationSucceeded {
			break
		}
		if result.State == installationUncertain {
			t.Log(logs.Snapshot())
			t.Fatalf("Actual setup unconfirmed: %s; stage=%d; setup=%d; commands=%d", result.ProblemCode, peer.stageSends.Load(), peer.setupSends.Load(), peer.commands.Load())
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	if result.Result == nil || !result.Result.Running || result.Result.ServerID != endpoint.ServerID || result.Result.WorkerVersion != "0.1.0" || peer.stageSends.Load() != 1 || peer.setupSends.Load() != 1 {
		t.Fatal("Original native setup proof missing")
	}
	replayRequest := &pb.StartSSHSetupRequest{Mutation: startRequest.Mutation, Credential: append([]byte(nil), credential...), Name: startRequest.Name, ConfirmedFingerprint: startRequest.ConfirmedFingerprint}
	replay, e := client.StartSSHSetup(ctx, installationTestRequest(owner, replayRequest))
	if e != nil || !replay.Msg.Replayed || replay.Msg.Setup.Id != current.Id || peer.setupSends.Load() != 1 {
		t.Fatal("Exact receipt replay repeated setup", e)
	}
	// A second explicit setup preserves the running original registration and epoch.
	secondID := string(domain.NewID())
	second, err := client.InspectSSHHost(ctx, installationTestRequest(owner, &pb.InspectSSHHostRequest{RequestId: secondID, Host: peer.target.Host, Port: uint32(peer.target.Port), User: peer.target.User}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.StartSSHSetup(ctx, installationTestRequest(owner, &pb.StartSSHSetupRequest{Mutation: &pb.Mutation{Id: secondID, ExpectedRevision: second.Msg.Setup.Revision, RequestId: string(domain.NewID())}, Name: "Repeated SSH setup", Credential: append([]byte(nil), credential...), ConfirmedFingerprint: observed.Identity.Fingerprint}))
	if err != nil {
		t.Fatal(err)
	}
	for {
		response, err := client.GetSSHSetup(ctx, installationTestRequest(owner, &pb.GetSSHSetupRequest{Id: secondID}))
		if err != nil {
			t.Fatal(err)
		}
		var reused sshOperation
		if domain.Decode(response.Msg.Setup.DocumentJson, &reused) != nil {
			t.Fatal("Malformed repeated setup")
		}
		if reused.State == installationSucceeded {
			if reused.Result == nil || !reused.Result.Reused || reused.Result.DeviceID != result.Result.DeviceID || reused.Result.MachineID != result.Result.MachineID || reused.Result.Generation != result.Result.Generation {
				t.Fatal("Repeated setup replaced original ownership")
			}
			break
		}
		if reused.State == installationUncertain {
			t.Fatalf("Repeated setup unconfirmed: %s", reused.ProblemCode)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	if peer.stageSends.Load() != 2 || peer.setupSends.Load() != 2 {
		t.Fatal("Unexpected native send count")
	}
	// Ordinary RPC bytes and SQLite contain neither credential spelling.
	for _, sentinel := range [][]byte{[]byte("ssh-secret-sentinel"), []byte("c3NoLXNlY3JldC1zZW50aW5lbA==")} {
		if bytes.Contains(current.DocumentJson, sentinel) || strings.Contains(logs.Snapshot(), string(sentinel)) {
			t.Fatal("Secret exposed in setup projection")
		}
		for _, name := range []string{"state.sqlite", "state.sqlite-wal"} {
			data, _ := os.ReadFile(filepath.Join(root, name))
			if bytes.Contains(data, sentinel) {
				t.Fatal("Secret persisted in SQLite")
			}
		}
	}
}

func installationTestRequest[T any](owner security.Identity, message *T) *connect.Request[T] {
	req := connect.NewRequest(message)
	req.Header().Set("Authorization", "Bearer "+owner.Token)
	return req
}
