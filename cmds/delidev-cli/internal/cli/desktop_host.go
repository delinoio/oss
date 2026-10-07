// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

const desktopFrameLimit = 256 << 10
const desktopReplyLimit = 128 << 10
const desktopShutdownTimeout = 35 * time.Second

type desktopOperation string

const (
	desktopLaunch   desktopOperation = "runtime.launch"
	desktopRetry    desktopOperation = "runtime.retry"
	desktopEnsure   desktopOperation = "runtime.ensure"
	desktopCancel   desktopOperation = "runtime.cancel"
	desktopShutdown desktopOperation = "runtime.shutdown"
	desktopFence    desktopOperation = "runtime.fence"
)

type desktopRequest struct {
	Version   int              `json:"version"`
	ID        domain.ID        `json:"id"`
	Operation desktopOperation `json:"operation"`
	Arguments []string         `json:"arguments,omitempty"`
	Input     []byte           `json:"input,omitempty"`
	Scope     string           `json:"scope,omitempty"`
	RequestID domain.ID        `json:"request_id,omitempty"`
	CancelID  domain.ID        `json:"cancel_id,omitempty"`
	TimeoutMS uint64           `json:"timeout_ms,omitempty"`
}
type desktopReply struct {
	Version int           `json:"version"`
	ID      domain.ID     `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *domain.Error `json:"error,omitempty"`
}
type desktopHostState struct {
	mu         sync.Mutex
	target     desktopruntime.Target
	config     server.Config
	options    options
	listener   net.Listener
	done       chan struct{}
	stop       context.CancelFunc
	startup    domain.ID
	closed     bool
	log        *slog.Logger
	diagnostic io.Writer
}

func runDesktopHostCommand(ctx context.Context, o options, args []string, streams IO) int {
	// A closed parent output pipe cancels and joins the host rather than exiting
	// immediately on SIGPIPE before owned native state is drained.
	brokenPipe := make(chan os.Signal, 1)
	signal.Notify(brokenPipe, syscall.SIGPIPE)
	defer signal.Stop(brokenPipe)
	fs := flags("server desktop-host")
	version := fs.Int("control-version", 0, "private desktop control version")
	listen := fs.String("listen", "127.0.0.1:0", "app-owned loopback listener")
	origins := fs.String("allowed-origins", "", "exact trusted desktop origins")
	parent := fs.Int("parent-pid", os.Getppid(), "original native parent")
	if parse(fs, args) != nil || *version != 2 || o.server != "" || o.tokenStdin || *parent != os.Getppid() {
		return emitDesktopFailure(streams, usage())
	}
	hostname, _, err := net.SplitHostPort(*listen)
	if err != nil || hostname != "127.0.0.1" {
		return emitDesktopFailure(streams, usage())
	}
	if err := security.PrivateDir(o.dataDir); err != nil {
		return emitDesktopFailure(streams, err)
	}
	lease, err := security.TryLock(filepath.Join(o.dataDir, "desktop-host.lock"))
	if err != nil {
		return emitDesktopFailure(streams, err)
	}
	defer lease.Close()
	sessionLease, err := security.TryLock(filepath.Join(o.dataDir, "desktop-session.lock"))
	if err != nil {
		return emitDesktopFailure(streams, err)
	}
	defer sessionLease.Close()
	listener, err := net.Listen("tcp4", *listen)
	if err != nil {
		return emitDesktopFailure(streams, domain.Fail(domain.Conflict, "The desktop listener is occupied.", "Preserve the existing process and inspect the original connection."))
	}
	key, err := randomDesktopKey()
	if err != nil {
		listener.Close()
		return emitDesktopFailure(streams, err)
	}
	config := server.Config{DataDir: o.dataDir, Listen: listener.Addr().String(), AllowedOrigins: strings.Split(*origins, ",")}
	if *origins == "" {
		config.AllowedOrigins = nil
	}
	if err := server.ValidateConfig(config); err != nil {
		listener.Close()
		return emitDesktopFailure(streams, err)
	}
	path := filepath.Join(o.dataDir, "server.log")
	if _, err := os.Lstat(path); err == nil {
		if err := security.RegularPrivate(path); err != nil {
			listener.Close()
			return emitDesktopFailure(streams, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		listener.Close()
		return emitDesktopFailure(streams, domain.SafeError(err))
	}
	log, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		listener.Close()
		return emitDesktopFailure(streams, domain.SafeError(err))
	}
	defer log.Close()
	h := &desktopHostState{target: desktopruntime.Target{Version: 2, Endpoint: "http://" + config.Listen, Generation: domain.NewID(), Key: key, Root: o.dataDir}, config: config, options: o, listener: listener, log: slog.New(slog.NewJSONHandler(log, nil)), diagnostic: streams.Err}
	lifetime, cancel := context.WithCancel(ctx)
	defer cancel()
	stopParent, err := watchDesktopParent(*parent, cancel)
	if err != nil {
		listener.Close()
		return emitDesktopFailure(streams, domain.SafeError(err))
	}
	defer stopParent()
	var writes sync.Mutex
	send := func(id domain.ID, result any, err error) {
		frame := desktopReply{Version: 2, ID: id, Result: result}
		if id != "" {
			phase := "completed"
			code := ""
			if err != nil {
				phase = "failed"
				code = string(domain.SafeError(err).Code)
			}
			h.log.Info("desktop_request", "request_id", id, "phase", phase, "code", code)
		}
		if err != nil {
			frame.Result = nil
			frame.Error = domain.SafeError(err)
		}
		raw, encodeErr := json.Marshal(frame)
		if encodeErr != nil || len(raw) > desktopReplyLimit {
			raw, _ = json.Marshal(desktopReply{Version: 2, ID: id, Error: domain.SafeError(domain.Fail(domain.ResourceExhausted, "The desktop reply exceeds its bound.", "Inspect the original operation before retrying."))})
		}
		writes.Lock()
		_, err = streams.Out.Write(append(raw, '\n'))
		writes.Unlock()
		if err != nil {
			cancel()
		}
	}
	send("", map[string]any{"endpoint": h.target.Endpoint, "generation": h.target.Generation, "key": key}, nil)
	// Only this native parent owns the pipe. EOF, malformed control and failed
	// delivery retire the host without authority over any discovered server.
	requests := make(chan desktopRequest, 32)
	go func() {
		defer cancel()
		reader := bufio.NewReader(streams.In)
		for {
			line, err := readDesktopFrame(reader)
			if err != nil {
				return
			}
			var r desktopRequest
			if domain.Decode(line, &r) != nil || r.Version != 2 || r.ID.Validate() != nil {
				return
			}
			select {
			case requests <- r:
			case <-lifetime.Done():
				return
			}
		}
	}()
	var tasks sync.WaitGroup
	var pendingMu sync.Mutex
	pending := map[domain.ID]context.CancelFunc{}
	mutations := make(chan struct{}, 1)
	reads := make(chan struct{}, 4)
	fenced := false
	explicitShutdown := false
loop:
	for {
		select {
		case <-lifetime.Done():
			break loop
		case <-brokenPipe:
			// A closed parent output pipe is an abnormal desktop loss. Join the
			// owned server, but do not turn transport loss into durable Stop.
			cancel()
			break loop
		case r := <-requests:
			if _, known := desktopCommands[r.Operation]; !known && r.Operation != desktopLaunch && r.Operation != desktopRetry && r.Operation != desktopEnsure && r.Operation != desktopShutdown && r.Operation != desktopCancel && r.Operation != desktopFence {
				// Reject untrusted operation text before structured diagnostics.
				clear(r.Input)
				send(r.ID, nil, usage())
				continue
			}
			if (r.Operation == desktopShutdown || r.Operation == desktopCancel || r.Operation == desktopFence) && (len(r.Arguments) > 0 || len(r.Input) > 0 || r.Scope != "" || r.RequestID != "" || ((r.Operation == desktopShutdown || r.Operation == desktopFence) && r.CancelID != "") || (r.Operation == desktopCancel && r.CancelID.Validate() != nil)) {
				cancel()
				break loop
			}
			if r.Operation == desktopFence {
				fenced = true
				pendingMu.Lock()
				for _, stop := range pending {
					stop()
				}
				pendingMu.Unlock()
				continue
			}
			if fenced && r.Operation != desktopShutdown && r.Operation != desktopCancel && !desktopReadOnly(r.Operation) {
				send(r.ID, nil, context.Canceled)
				continue
			}
			if r.Operation == desktopShutdown {
				explicitShutdown = true
				cancel()
				break loop
			}
			if r.Operation == desktopCancel {
				pendingMu.Lock()
				stop := pending[r.CancelID]
				pendingMu.Unlock()
				if stop != nil {
					stop()
				}
				continue
			}
			h.log.Info("desktop_request", "request_id", r.ID, "operation", r.Operation, "phase", "admission")
			pendingMu.Lock()
			if len(pending) >= 32 || pending[r.ID] != nil {
				pendingMu.Unlock()
				send(r.ID, nil, domain.Fail(domain.Conflict, "Desktop admission is busy.", "Retain the original request identity."))
				continue
			}
			timeout := 40 * time.Second
			if r.TimeoutMS > 0 && r.TimeoutMS <= 660000 {
				timeout = time.Duration(r.TimeoutMS) * time.Millisecond
			}
			operation, stop := context.WithTimeout(lifetime, timeout)
			pending[r.ID] = stop
			pendingMu.Unlock()
			tasks.Add(1)
			go func() {
				defer tasks.Done()
				defer stop()
				defer func() { pendingMu.Lock(); delete(pending, r.ID); pendingMu.Unlock(); clear(r.Input) }()
				gate := mutations
				if desktopReadOnly(r.Operation) {
					gate = reads
				}
				select {
				case gate <- struct{}{}:
				case <-operation.Done():
					send(r.ID, nil, operation.Err())
					return
				}
				defer func() { <-gate }()
				result, err := h.execute(operation, r)
				send(r.ID, result, err)
			}()
		}
	}
	force := time.AfterFunc(desktopShutdownTimeout, func() {
		h.log.Warn("desktop_host_shutdown", "phase", "host-force-requested", "native_cleanup", "unconfirmed")
		os.Exit(1)
	})
	h.shutdown(explicitShutdown)
	tasks.Wait()
	force.Stop()
	h.log.Info("desktop_host_shutdown", "phase", "host-joined")
	return 0
}
func readDesktopFrame(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > desktopFrameLimit {
			return nil, io.ErrShortBuffer
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, err
	}
}
func (h *desktopHostState) start(ctx context.Context, op desktopOperation) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, joinedStartupTimeout)
	defer cancel()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, context.Canceled
	}
	if h.done != nil {
		select {
		case <-h.done:
			h.done = nil
			h.stop = nil
		default:
			done, stop := h.done, h.stop
			h.mu.Unlock()
			intent, err := server.ReadLifecycle(h.options.dataDir)
			if err != nil {
				return nil, err
			}
			if intent.State == server.DesiredStopped {
				if op != desktopLaunch {
					return map[string]any{"state": "stopped"}, nil
				}
				stop()
				select {
				case <-done:
					return h.start(ctx, op)
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return h.execute(ctx, desktopRequest{Operation: "server.desktop-status"})
		}
	}
	config := h.config
	config.Logger = h.log
	config.Listener = h.listener
	h.listener = nil
	target := h.target
	config.Desktop = &target
	done := make(chan struct{})
	run, stop := context.WithCancel(context.Background())
	h.done = done
	h.stop = stop
	h.mu.Unlock()
	ready := make(chan any, 1)
	failed := make(chan error, 1)
	mode := startupDesktopLaunch
	if op == desktopRetry {
		mode = startupDesktopRetry
	}
	if op == desktopEnsure {
		mode = startupEnsure
	}
	go func() {
		defer close(done)
		defer stop()
		defer func() {
			if config.Listener != nil {
				config.Listener.Close()
			}
		}()
		admission, cancel := context.WithTimeout(run, joinedStartupTimeout)
		defer cancel()
		result, err := startupWithHost(admission, options{dataDir: h.options.dataDir, desktop: &target}, config, IO{In: bytes.NewReader(nil), Out: io.Discard, Err: io.Discard}, mode, func(admission context.Context, c server.Config, intent server.Lifecycle, release func() error) (any, error) {
			c.StartupID = intent.Generation
			h.mu.Lock()
			h.startup = intent.Generation
			h.mu.Unlock()
			abort := context.AfterFunc(admission, stop)
			defer abort()
			err := server.Serve(run, c, func(endpoint server.Endpoint) {
				abort()
				if err := release(); err != nil {
					stop()
					failed <- err
					return
				}
				target.ServerID = endpoint.ServerID
				h.mu.Lock()
				h.target = target
				h.mu.Unlock()
				ready <- desktopStarted(target, intent.Generation)
			})
			return nil, err
		})
		if result != nil {
			ready <- result
		} else {
			failed <- err
		}
	}()
	select {
	case result := <-ready:
		return result, nil
	case err := <-failed:
		return nil, err
	case <-ctx.Done():
		stop()
		return nil, ctx.Err()
	}
}
func desktopStarted(t desktopruntime.Target, generation domain.ID) any {
	return map[string]any{"started": true, "generation": generation, "server": map[string]any{"status": map[string]any{"version": "0.1.0", "protocol_version": 1, "listener": t.Endpoint, "server_id": t.ServerID}}}
}
func (h *desktopHostState) shutdown(suppressRestart bool) {
	h.mu.Lock()
	h.closed = true
	stop, done, listener, generation := h.stop, h.done, h.listener, h.startup
	h.listener = nil
	h.mu.Unlock()
	if listener != nil {
		listener.Close()
	}
	if stop != nil {
		stop()
		<-done
		if suppressRestart {
			h.mu.Lock()
			finalGeneration := h.startup
			h.mu.Unlock()
			for _, candidate := range []domain.ID{generation, finalGeneration} {
				if candidate == "" {
					continue
				}
				if err := server.SuppressDesktopRestart(h.options.dataDir, candidate); err != nil {
					h.log.Warn("desktop_host_shutdown", "phase", "suppression-unconfirmed", "code", domain.SafeError(err).Code)
				}
			}
		}
	}
}

func emitDesktopFailure(streams IO, err error) int {
	_ = json.NewEncoder(streams.Out).Encode(desktopReply{Version: 2, Error: domain.SafeError(err)})
	return 1
}
