// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"context"
	"database/sql"
	observability "github.com/delinoio/oss/servers/aiprouter/internal/observability"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/store"
)

// The proxy disconnects only this isolated fixture, never the shared PostgreSQL server.
func TestRoutingReleaseQueueRecoversDatabaseOutage(t *testing.T) {
	dsn := os.Getenv("AIPROUTER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("real PostgreSQL fixture required")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	upstream := u.Host
	if u.Port() == "" {
		upstream = net.JoinHostPort(u.Hostname(), "5432")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var mu sync.Mutex
	offline := false
	failPricingRead := false
	connections := map[net.Conn]bool{}
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if offline {
				mu.Unlock()
				client.Close()
				continue
			}
			connections[client] = true
			mu.Unlock()
			go func() {
				defer client.Close()
				defer func() { mu.Lock(); delete(connections, client); mu.Unlock() }()
				backend, err := net.DialTimeout("tcp", upstream, time.Second)
				if err != nil {
					return
				}
				defer backend.Close()
				done := make(chan struct{})
				go func() {
					defer close(done)
					defer backend.Close()
					buffer := make([]byte, 8192)
					var recent []byte
					for {
						n, readErr := client.Read(buffer)
						if n > 0 {
							recent = append(recent, buffer[:n]...)
							mu.Lock()
							fail := failPricingRead && bytes.Contains(recent, []byte("SELECT rpm,concurrency,usage_days"))
							if fail {
								offline = true
								failPricingRead = false
							}
							mu.Unlock()
							if fail {
								client.Close()
								break
							}
							if len(recent) > 512 {
								recent = recent[len(recent)-512:]
							}
							if _, err := backend.Write(buffer[:n]); err != nil {
								break
							}
						}
						if readErr != nil {
							break
						}
					}
				}()
				io.Copy(client, backend)
				client.Close()
				<-done
			}()
		}
	}()
	u.Host = listener.Addr().String()
	keys, err := core.NewEnvelope("test", map[string][]byte{"test": make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	schema := "release_" + strings.ReplaceAll(core.ID(), "-", "")
	s, err := store.Open(context.Background(), u.String(), store.Options{Schema: schema, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer func() {
		db, e := sql.Open("pgx", dsn)
		if e == nil {
			defer db.Close()
			db.Exec("DROP SCHEMA IF EXISTS " + schema + "_temp CASCADE")
			db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterAdmissionInstance(ctx); err != nil {
		t.Fatal(err)
	}
	user, _ := testIdentity(t, s, "release-outage")
	org, err := s.CreateOrganization(ctx, user.ID, "Release fixture")
	if err != nil {
		t.Fatal(err)
	}
	sc, _, err := s.Membership(ctx, org.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := s.IssueKey(ctx, sc, "release", nil)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.Admit(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	q := newAdmissionReleaseQueue(ctx, s, observability.New(slog.New(slog.NewJSONHandler(io.Discard, nil))))
	defer q.Close()
	mu.Lock()
	offline = true
	for conn := range connections {
		conn.Close()
	}
	mu.Unlock()
	cleaned := make(chan struct{})
	q.Submit(lease, func(context.Context) { close(cleaned) })
	short, stop := context.WithTimeout(ctx, 150*time.Millisecond)
	if err = q.Wait(short); err == nil {
		stop()
		t.Fatal("release acknowledged during database outage")
	}
	stop()
	select {
	case <-cleaned:
		t.Fatal("cleanup ran before durable acknowledgement")
	default:
	}
	mu.Lock()
	offline = false
	mu.Unlock()
	if err = q.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("release cleanup missing")
	}
	instances, err := s.ListAdmissionInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		if instance.ID == s.InstanceID() && instance.ActiveAdmissions != 0 {
			t.Fatal("completed admission still consumes capacity")
		}
	}
	next, err := s.Admit(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Release(ctx, next); err != nil {
		t.Fatal(err)
	}
	// Fail the pricing SELECT after the admission transaction commits. Its
	// failed setup must retain the same asynchronous release obligation.
	mu.Lock()
	failPricingRead = true
	mu.Unlock()
	adapter := &inferenceAdapter{s: s, telemetry: q.telemetry, releaseCompleted: q.Submit}
	_, release, err := adapter.Admit(ctx, key)
	if err == nil || release != nil {
		t.Fatal("pricing outage did not fail setup")
	}
	mu.Lock()
	cut := offline
	mu.Unlock()
	if !cut {
		t.Fatal("pricing read outage was not exercised")
	}
	short, stop = context.WithTimeout(ctx, 150*time.Millisecond)
	if err = q.Wait(short); err == nil {
		stop()
		t.Fatal("setup failure release lost during outage")
	}
	stop()
	mu.Lock()
	offline = false
	mu.Unlock()
	if err = q.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	instances, err = s.ListAdmissionInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		if instance.ID == s.InstanceID() && instance.ActiveAdmissions != 0 {
			t.Fatal("setup failure leaked admission capacity")
		}
	}

}
