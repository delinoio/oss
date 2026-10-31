// SPDX-License-Identifier: Apache-2.0
// Package session binds durable cache leases to a native process supervisor.
package session

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/cachecow/internal/lease"
	"github.com/delinoio/oss/cmds/cachecow/internal/lifecycle"
	"github.com/delinoio/oss/cmds/cachecow/internal/process"
	"github.com/delinoio/oss/cmds/cachecow/internal/state"
	"github.com/google/uuid"
)

const Env = "CCOW_SESSION"

var errAuth = errors.New("nested session authentication failed; use a live managed parent session")

type Action string

const (
	Prepare   Action = "prepare"
	Run       Action = "run"
	Clean     Action = "clean"
	ToolClean Action = "tool-clean"
)

type Plan struct {
	SchemaVersion int              `json:"schemaVersion"`
	Root          string           `json:"root"`
	Action        Action           `json:"action"`
	Resources     lease.Resources  `json:"resources"`
	Pairs         []lifecycle.Pair `json:"pairs"`
	Record        string           `json:"record"`
	Replace       bool             `json:"replace"`
	AllowCopy     bool             `json:"allowCopy"`
	Claims        json.RawMessage  `json:"claims"`
}
type Response struct {
	Ticket string           `json:"ticket,omitempty"`
	Result lifecycle.Result `json:"result"`
	Error  string           `json:"error,omitempty"`
}
type request struct {
	Cancel   bool
	Ticket   string
	ExitCode int
	Token    string
	Plan     Plan
}

// Validate must independently check discovered claims and protected paths using
// filesystem reads only. Supervisor callbacks cannot create subprocesses.
type Validate func(Plan) error

type RecordHooks struct {
	Compatible func(Plan, Plan) bool
	Before     func(Plan) error
	After      func(Plan, int) error
}

type nestedRecord struct {
	pid  int
	plan Plan
	done bool
	code int
}
type server struct {
	tickets   map[string]*nestedRecord
	records   RecordHooks
	initial   Plan
	pending   map[string]Plan
	ambiguous map[string]bool
	scope     process.Scope
	registry  *lease.Registry
	session   *lease.Session
	listener  net.Listener
	cleanup   func()
	token     string
	validate  Validate
	root      string
	wg        sync.WaitGroup
	mu        sync.Mutex
}

func Hooks(validate Validate, recordHooks ...RecordHooks) process.SupervisorHooks {
	var records RecordHooks
	if len(recordHooks) > 0 {
		records = recordHooks[0]
	}
	var s *server
	return process.SupervisorHooks{
		Ready: func(scope process.Scope) error { return lease.EnsurePrivateDirectory(scope.Directory) },
		BeforeStart: func(scope process.Scope, c *process.Command) error {
			var p Plan
			if e := state.Read(filepath.Join(scope.Directory, "operation.json"), &p); e != nil {
				return e
			}
			fail := func(e error) error {
				_ = state.Write(filepath.Join(scope.Directory, "result.json"), Response{Error: e.Error()})
				return e
			}
			if p.SchemaVersion != 1 || filepath.Dir(filepath.Dir(scope.Directory)) != p.Root {
				return fail(errors.New("invalid supervisor state root"))
			}
			if e := validate(p); e != nil {
				return fail(e)
			}
			open := lease.OpenWait
			if p.Action == Clean {
				open = lease.Open
			}
			r, e := open(scope.Context, filepath.Join(p.Root, "leases"))
			if e != nil {
				return fail(e)
			}
			session, e := r.NewSession(scope.Directory)
			if e != nil {
				r.Close()
				return fail(e)
			}
			s = &server{scope: scope, registry: r, session: session, validate: validate, root: p.Root, records: records, initial: p, pending: map[string]Plan{}, ambiguous: map[string]bool{}, tickets: map[string]*nestedRecord{}}
			result, e := s.apply(scope.Context, p)
			if e != nil {
				return fail(e)
			}
			if e = state.Write(filepath.Join(scope.Directory, "result.json"), Response{Result: result}); e != nil {
				return e
			}
			if c.Noop {
				return nil
			}
			l, endpoint, cleanup, e := listen()
			if e != nil {
				return fail(e)
			}
			s.listener = l
			s.cleanup = cleanup
			var token [32]byte
			if _, e = rand.Read(token[:]); e != nil {
				return fail(e)
			}
			s.token = hex.EncodeToString(token[:])
			c.Env = withEnv(c.Env, Env, endpoint+"|"+s.token)
			s.wg.Add(1)
			go s.serve()
			return nil
		},
		Quiesce: func(scope process.Scope) error {
			if s != nil && s.listener != nil {
				s.listener.Close()
				s.wg.Wait()
				s.cleanup()
			}
			if s != nil {
				for _, v := range s.tickets {
					if !v.done || v.code != 0 {
						s.ambiguous[v.plan.Record] = true
					}
				}
				if s.records.After != nil {
					for key, p := range s.pending {
						if !s.ambiguous[key] {
							if e := s.records.After(p, scope.ExitCode); e != nil {
								return e
							}
						}
					}
				}
			}
			return nil
		},
		Drained: func(scope process.Scope) error {
			if s == nil {
				return nil
			}
			defer s.registry.Close()

			return s.session.Finish(context.Background(), func(_ context.Context, r lease.Record) error {
				if r.Ownership != scope.Directory {
					return lease.ErrUnverified
				}
				return process.Reconcile(scope)
			})
		},
	}
}
func (s *server) apply(ctx context.Context, p Plan) (result lifecycle.Result, resultErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if resultErr == nil {
			if old, ok := s.pending[p.Record]; ok && s.records.Compatible != nil && !s.records.Compatible(old, p) {
				s.ambiguous[p.Record] = true
			}
			s.pending[p.Record] = p
		}
	}()
	if p.SchemaVersion != 1 || p.Root != s.root {
		return lifecycle.Result{}, errors.New("invalid nested state root")
	}
	if e := s.validate(p); e != nil {
		return lifecycle.Result{}, e
	}
	g, e := s.session.Acquire(ctx, p.Resources, p.Action != Clean)
	if e != nil {
		return lifecycle.Result{}, e
	}
	if e := s.validate(p); e != nil {
		return lifecycle.Result{}, e
	}
	if g.Borrowed {
		if p.Action == Clean || p.Replace {
			return lifecycle.Result{}, errors.New("cannot clean or replace an active parent cache")
		}
		if s.records.Before != nil {
			if e := s.records.Before(p); e != nil {
				return lifecycle.Result{}, e
			}
		}
		return lifecycle.Result{Reused: true}, nil
	}
	// A partly borrowed resource set must not replace an outer cache either.
	newPath := map[string]bool{}
	for _, v := range g.NewPaths {
		newPath[v] = true
	}
	for _, source := range p.Resources.Sources {
		if !newPath[source] {
			return lifecycle.Result{}, errors.New("nested source cache is already writable by the parent scope; prepare the derived cache before starting the outer build")
		}
	}
	for _, v := range p.Resources.Destinations {
		if !newPath[v] {
			return lifecycle.Result{}, lease.ErrNestedConflict
		}
	}
	if s.records.Before != nil {
		if e := s.records.Before(p); e != nil {
			return lifecycle.Result{}, e
		}
	}
	if e = lifecycle.Recover(p.Record, p.Pairs); e != nil {
		return lifecycle.Result{}, e
	}
	switch p.Action {
	case Clean:
		for _, v := range p.Resources.Destinations {
			if _, e = lifecycle.Exists(v); e != nil {
				return lifecycle.Result{}, e
			}
			if e = os.RemoveAll(v); e != nil {
				return lifecycle.Result{}, e
			}
		}
		return lifecycle.Result{}, nil
	case ToolClean:
		return lifecycle.Result{Reused: true}, nil
	case Run, Prepare:
		result, e := lifecycle.Prepare(ctx, p.Record, p.Pairs, p.Replace, p.AllowCopy)
		if e != nil {
			return result, e
		}
		var reads []string
		for _, source := range p.Resources.Sources {
			if !newPath[source] {
				continue
			}
			destination := false
			for _, dst := range p.Resources.Destinations {
				if dst == source {
					destination = true
				}
			}
			if !destination {
				reads = append(reads, source)
			}
		}
		if e = s.session.ReleasePreparedSources(reads, g); e != nil {
			return result, e
		}
		return result, nil
	default:
		return lifecycle.Result{}, errors.New("unknown session action")
	}
}
func (s *server) serve() {
	defer s.wg.Done()
	for {
		c, e := s.listener.Accept()
		if e != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(30 * time.Second))
			pid, e := peer(c)
			if e != nil || !process.Contains(s.scope, pid) {
				json.NewEncoder(c).Encode(Response{Error: errAuth.Error()})
				return
			}
			var r request
			d := json.NewDecoder(io.LimitReader(c, 1024*1024))
			d.DisallowUnknownFields()
			if e = d.Decode(&r); e != nil {
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Token), []byte(s.token)) != 1 {
				json.NewEncoder(c).Encode(Response{Error: errAuth.Error()})
				return
			}
			_ = c.SetDeadline(time.Time{})
			out := Response{}
			if r.Ticket != "" {
				s.mu.Lock()
				ticket, ok := s.tickets[r.Ticket]
				if !ok || ticket.pid != pid || ticket.done {
					out.Error = errAuth.Error()
				} else if r.Cancel {
					if e := cancelOwner(s.scope.OwnerPID); e != nil {
						out.Error = e.Error()
					}
				} else {
					ticket.done = true
					ticket.code = r.ExitCode
				}
				s.mu.Unlock()
			} else {
				result, e := s.apply(s.scope.Context, r.Plan)
				out.Result = result
				if e != nil {
					out.Error = e.Error()
				} else {
					id, e := uuid.NewV7()
					if e != nil {
						out.Error = e.Error()
					} else {
						out.Ticket = id.String()
						s.mu.Lock()
						s.tickets[out.Ticket] = &nestedRecord{pid: pid, plan: r.Plan}
						s.mu.Unlock()
					}
				}
			}

			json.NewEncoder(c).Encode(out)
		}()
	}
}
func Nested(ctx context.Context, capability string, p Plan) (Response, error) {
	return exchange(ctx, capability, request{Plan: p})
}

// Cancel asks the authenticated native owner to drain the entire session.
// Shared outer protection remains held until native whole-tree completion.
func Cancel(ctx context.Context, capability, ticket string) error {
	if ticket == "" {
		return errAuth
	}
	_, e := exchange(ctx, capability, request{Ticket: ticket, Cancel: true})
	return e
}
func Complete(ctx context.Context, capability, ticket string, code int) error {
	_, e := exchange(ctx, capability, request{Ticket: ticket, ExitCode: code})
	return e
}
func exchange(ctx context.Context, capability string, r request) (Response, error) {
	endpoint, token, ok := strings.Cut(capability, "|")
	if !ok || len(token) != 64 {
		return Response{}, errAuth
	}
	r.Token = token
	c, e := dial(endpoint)
	if e != nil {
		return Response{}, fmt.Errorf("%w: %v", errAuth, e)
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if e = json.NewEncoder(c).Encode(r); e != nil {
		return Response{}, fmt.Errorf("%w: %v", errAuth, e)
	}
	var out Response
	if e = json.NewDecoder(c).Decode(&out); e != nil {
		return out, e
	}
	if out.Error != "" {
		return out, errors.New(out.Error)
	}
	return out, nil
}

func Execute(ctx context.Context, p Plan, c process.Command, streams process.Streams) (process.Result, lifecycle.Result, error) {
	var prepared lifecycle.Result
	r, e := process.Run(ctx, filepath.Join(p.Root, "processes"), c, streams, process.Hooks{
		Owned: func(scope process.Scope) error {
			return state.Write(filepath.Join(scope.Directory, "operation.json"), p)
		},
		Drained: func(process.Scope) error { return nil },
	})
	var out Response
	if r.Scope.Directory != "" {
		if readErr := state.Read(filepath.Join(r.Scope.Directory, "result.json"), &out); readErr == nil {
			prepared = out.Result
			if out.Error != "" {
				e = errors.New(out.Error)
			}
		}
	}
	return r, prepared, e
}
func withEnv(env []string, key, value string) []string {
	out := []string{}
	for _, v := range env {
		if !strings.HasPrefix(v, key+"=") {
			out = append(out, v)
		}
	}
	return append(out, key+"="+value)
}
