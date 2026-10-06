// SPDX-License-Identifier: Apache-2.0
package sshsetup

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"golang.org/x/crypto/ssh"
)

type peerStall uint8

const (
	noStall peerStall = iota
	channelOpenStall
	execReplyStall
	commandExitStall
)

type testPeer struct {
	listener    net.Listener
	target      Target
	identity    Identity
	auth        atomic.Int64
	commands    atomic.Int64
	mu          sync.Mutex
	connections []net.Conn
	wg          sync.WaitGroup
	stalled     chan struct{}
	settled     chan struct{}
}

func newPeer(t *testing.T, key ed25519.PrivateKey, output string) *testPeer {
	t.Helper()
	return newStallingPeer(t, key, output, noStall)
}

func newStallingPeer(t *testing.T, key ed25519.PrivateKey, output string, stall peerStall) *testPeer {
	t.Helper()
	signer, e := ssh.NewSignerFromKey(key)
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := listener.Addr().(*net.TCPAddr)
	p := &testPeer{listener: listener, target: Target{"127.0.0.1", uint16(addr.Port), "fixture-user"}, identity: Identity{ssh.FingerprintSHA256(signer.PublicKey()), signer.PublicKey().Type()}}
	p.stalled = make(chan struct{}, 1)
	p.settled = make(chan struct{}, 1)
	config := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, secret []byte) (*ssh.Permissions, error) {
		p.auth.Add(1)
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
				var handlers sync.WaitGroup
				defer func() {
					server.Close()
					handlers.Wait()
					p.settled <- struct{}{}
				}()
				handlers.Add(1)
				go func() {
					defer handlers.Done()
					ssh.DiscardRequests(requests)
				}()
				for incoming := range channels {
					if incoming.ChannelType() != "session" {
						incoming.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					if stall == channelOpenStall {
						p.stalled <- struct{}{}
						continue
					}
					ch, requests, e := incoming.Accept()
					if e != nil {
						return
					}
					handlers.Add(1)
					go func() {
						defer handlers.Done()
						defer ch.Close()
						for r := range requests {
							if r.Type != "exec" {
								r.Reply(false, nil)
								continue
							}
							var request struct{ Command string }
							if ssh.Unmarshal(r.Payload, &request) != nil {
								return
							}
							p.commands.Add(1)
							if stall == execReplyStall {
								p.stalled <- struct{}{}
								for range requests {
								}
								return
							}
							r.Reply(true, nil)
							if output == "wait" || stall == commandExitStall {
								p.stalled <- struct{}{}
								for range requests {
								}
								return
							}
							if strings.Contains(request.Command, "/etc/os-release") && output == "Linux\nx86_64\n" {
								io.WriteString(ch, "ubuntu\n22.04\n")
							} else {
								io.WriteString(ch, output)
							}
							var status [4]byte
							binary.BigEndian.PutUint32(status[:], 0)
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
func fixtureKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return key
}
func TestActualSSHObservesBeforeAuthenticationAndPinsExactKey(t *testing.T) {
	peer := newPeer(t, fixtureKey(t), "Linux\nx86_64\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observed, e := Observe(ctx, peer.target)
	if e != nil || observed != peer.identity || peer.auth.Load() != 0 || peer.commands.Load() != 0 {
		t.Fatal("Observation gained authority", e)
	}
	credential := Credential{Method: Password, Secret: []byte("ssh-secret-sentinel")}
	connection, e := Connect(ctx, peer.target, observed, credential)
	if e != nil {
		t.Fatal(e)
	}
	target, e := connection.Inspect(ctx)
	connection.Close()
	if e != nil || target != "linux-amd64" || peer.auth.Load() != 1 || peer.commands.Load() != 2 {
		t.Fatal("Closed inspection failed", e)
	}
	changed := newPeer(t, fixtureKey(t), "Linux\nx86_64\n")
	if c, e := Connect(ctx, changed.target, observed, credential); e == nil {
		c.Close()
		t.Fatal("Changed host key accepted")
	}
	if changed.auth.Load() != 0 || changed.commands.Load() != 0 {
		t.Fatal("Changed host received authentication or commands")
	}
	credential.Clear()
	if len(credential.Secret) != 0 || len(credential.Passphrase) != 0 {
		t.Fatal("Credential retained")
	}
}
func TestActualSSHCommandCancellationAndOutputBounds(t *testing.T) {
	for _, value := range []string{"wait", strings.Repeat("x", OutputLimit+1)} {
		t.Run(value[:4], func(t *testing.T) {
			peer := newPeer(t, fixtureKey(t), value)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, e := Connect(ctx, peer.target, peer.identity, Credential{Method: Password, Secret: []byte("ssh-secret-sentinel")})
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			readCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
			defer stop()
			if _, e := c.Inspect(readCtx); e == nil {
				t.Fatal("Unavailable inspection accepted")
			}
		})
	}
}

func TestActualSSHCommandStallsRespectCancellation(t *testing.T) {
	for _, stall := range []struct {
		name string
		mode peerStall
	}{
		{"channel-open", channelOpenStall},
		{"exec-reply", execReplyStall},
		{"command-exit", commandExitStall},
	} {
		for _, deadline := range []bool{true, false} {
			name := "parent-cancel"
			if deadline {
				name = "child-deadline"
			}
			t.Run(stall.name+"/"+name, func(t *testing.T) {
				peer := newStallingPeer(t, fixtureKey(t), "", stall.mode)
				parent, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				c, err := Connect(parent, peer.target, peer.identity, Credential{Method: Password, Secret: []byte("ssh-secret-sentinel")})
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				ctx := parent
				if deadline {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(parent, 50*time.Millisecond)
					defer stop()
				}
				done := make(chan error, 1)
				go func() {
					_, err := c.Inspect(ctx)
					done <- err
				}()
				select {
				case <-peer.stalled:
				case <-time.After(time.Second):
					t.Fatal("SSH peer did not reach the stalled phase")
				}
				if !deadline {
					cancel()
				}
				select {
				case err := <-done:
					want := domain.Canceled
					if deadline {
						want = domain.Unavailable
						if parent.Err() != nil {
							t.Fatal("Command waited for its connection parent")
						}
					}
					var problem *domain.Error
					if !errors.As(err, &problem) || problem.Code != want {
						t.Fatalf("Unexpected command failure: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("Command did not settle at cancellation")
				}
				// The peer withholds channel replies and closes. Only transport
				// shutdown can join every authenticated peer handler here.
				select {
				case <-peer.settled:
				case <-time.After(time.Second):
					t.Fatal("Command left its authenticated transport running")
				}
				wantCommands := int64(1)
				if stall.mode == channelOpenStall {
					wantCommands = 0
				}
				if peer.auth.Load() != 1 || peer.commands.Load() != wantCommands {
					t.Fatal("Stalled command gained extra authentication or exec authority")
				}
			})
		}
	}
}

type heldCloseConn struct {
	net.Conn
	closed  chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *heldCloseConn) Close() error {
	c.once.Do(func() {
		c.Conn.Close()
		close(c.closed)
		<-c.release
	})
	return nil
}

func TestActualSSHCommandJoinsCancellationBeforeReturn(t *testing.T) {
	peer := newStallingPeer(t, fixtureKey(t), "", channelOpenStall)
	parent, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := Connect(parent, peer.target, peer.identity, Credential{Method: Password, Secret: []byte("ssh-secret-sentinel")})
	if err != nil {
		t.Fatal(err)
	}
	held := &heldCloseConn{Conn: c.conn, closed: make(chan struct{}), release: make(chan struct{})}
	c.conn = held
	defer c.Close()
	var release sync.Once
	defer release.Do(func() { close(held.release) })
	ctx, stop := context.WithTimeout(parent, 50*time.Millisecond)
	defer stop()
	done := make(chan error, 1)
	go func() {
		_, err := c.Inspect(ctx)
		done <- err
	}()
	select {
	case <-held.closed:
	case <-time.After(time.Second):
		t.Fatal("Child deadline did not close the owned transport")
	}
	select {
	case <-done:
		t.Fatal("Command returned before its cancellation callback joined")
	case <-time.After(20 * time.Millisecond):
	}
	release.Do(func() { close(held.release) })
	select {
	case err := <-done:
		if domain.SafeError(err).Code != domain.Unavailable || parent.Err() != nil {
			t.Fatal("Joined command lost its child timeout classification", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Command did not return after its cancellation callback joined")
	}
}

func TestActualSSHSetupEffectsRetainRecoveryAfterDeadline(t *testing.T) {
	data := []byte("fixture-worker-bytes")
	digest := sha256.Sum256(data)
	artifact := updates.Artifact{
		Component: updates.Worker,
		Target:    "linux-amd64",
		Name:      updates.ArtifactName(updates.Worker, "linux-amd64"),
		Size:      int64(len(data)),
		SHA256:    hex.EncodeToString(digest[:]),
	}
	artifact.URL = updates.ReleaseURL("0.1.0", artifact.Name)
	server, operation := domain.NewID(), domain.NewID()
	document := SetupDocument{
		Version: 1, OperationID: operation, ServerID: server,
		ReleaseVersion: "0.1.0", SourceRevision: strings.Repeat("a", 40), Artifact: artifact,
		Grant: worker.PairingCode{
			Version: 1, PairingID: domain.NewID(), ServerID: server,
			Endpoint: "http://127.0.0.1:46307",
			Code:     base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
		},
		Name: "Fixture Runner Device",
	}
	if err := document.Validate(); err != nil {
		t.Fatal("Invalid setup fixture", err)
	}
	source := filepath.Join(t.TempDir(), "worker")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, setup := range []bool{false, true} {
		name := "stage"
		if setup {
			name = "setup"
		}
		t.Run(name, func(t *testing.T) {
			peer := newStallingPeer(t, fixtureKey(t), "", execReplyStall)
			parent, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c, err := Connect(parent, peer.target, peer.identity, Credential{Method: Password, Secret: []byte("ssh-secret-sentinel")})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ctx, stop := context.WithTimeout(parent, 50*time.Millisecond)
			defer stop()
			if setup {
				_, err = c.Setup(ctx, document, false)
			} else {
				err = c.Stage(ctx, server, operation, artifact, source)
			}
			if domain.SafeError(err).Code != domain.RecoveryRequired || parent.Err() != nil {
				t.Fatal("Potential remote effect lost its recovery requirement or child deadline", err)
			}
			select {
			case <-peer.settled:
			case <-time.After(time.Second):
				t.Fatal("Potential remote effect left the transport running")
			}
			if peer.commands.Load() != 1 {
				t.Fatal("Potential remote effect was not sent exactly once")
			}
		})
	}
}

func TestRejectsArbitraryTargetAndAuthenticationInput(t *testing.T) {
	for _, host := range []string{"", "$(id)", "host;rm", "host\nother", "host/path", "[::1]", "0.0.0.0", "ff02::1"} {
		if (Target{host, 22, "user"}).Validate() == nil {
			t.Fatalf("Invalid host accepted %q", host)
		}
	}
	for _, user := range []string{"user`id`", "user\nother", "user;cmd"} {
		if (Target{"localhost", 22, user}).Validate() == nil {
			t.Fatal("Invalid user accepted")
		}
	}
	for _, c := range []Credential{{}, {Method: "agent", Secret: []byte("sentinel")}, {Method: PrivateKey, Secret: []byte("invalid")}, {Method: Password, Secret: []byte("value"), Passphrase: []byte("other")}} {
		if _, e := c.auth(); e == nil {
			t.Fatal("Invalid auth accepted")
		}
	}
}
