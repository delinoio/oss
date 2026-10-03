// SPDX-License-Identifier: Apache-2.0
package sshsetup

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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
}

func newPeer(t *testing.T, key ed25519.PrivateKey, output string) *testPeer {
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
							var request struct{ Command string }
							if ssh.Unmarshal(r.Payload, &request) != nil {
								return
							}
							p.commands.Add(1)
							r.Reply(true, nil)
							if output == "wait" {
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
