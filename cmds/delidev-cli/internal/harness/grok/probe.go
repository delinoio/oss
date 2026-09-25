// Package grok owns the pinned private Grok Build ACP profile. Discovery has
// no authentication, session creation, prompt or execution operation.
package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

const SupportedVersion = domain.GrokProtocolVersion
const maxProbeFrame = 1 << 20
const maxProbeStderr = 64 << 10

type ProbeConfig struct {
	Process process.Config
	Version string
	Home    string
}

type probePhase string

const (
	profilePhase    probePhase = "profile"
	runtimePhase    probePhase = "runtime"
	inspectPhase    probePhase = "inspect"
	launchPhase     probePhase = "launch"
	initializePhase probePhase = "initialize"
	cleanupPhase    probePhase = "cleanup"
)

func incompatible() *domain.Error {
	return domain.Fail(domain.Unsupported, "The installed Grok Build does not match its native protocol profile.", "Select a validated Grok Build version and refresh protocol discovery; no account execution was authorized.")
}

func probeUnavailable() *domain.Error {
	return domain.Fail(domain.Unavailable, "Grok Build did not complete native initialization.", "Inspect the selected Worker installation and refresh protocol discovery.")
}

func probeCleanupRequired() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Grok Build probe cleanup could not be confirmed.", "Retain the private runtime and reconcile its owned process before retrying.")
}

func nativeLaunchError(err error) error {
	problem := domain.SafeError(err)
	if problem.Code == domain.Internal {
		// Cancellation can close the private controller socket during Resume.
		// Its raw OS error includes a local path and is not protocol evidence.
		return probeUnavailable()
	}
	return problem
}

// Probe inspects isolated native configuration, then sends one ACP initialize
// request, never a prompt, login,
// session creation, permission response or model request. Its success proves
// only this installed protocol profile, not any account or execution feature.
func Probe(ctx context.Context, config ProbeConfig) (returned error) {
	phase := profilePhase
	defer func() {
		if config.Process.Logger != nil {
			if returned != nil {
				config.Process.Logger.WarnContext(ctx, "Grok Build native handshake failed", "owner_id", config.Process.OwnerID, "phase", phase, "code", domain.SafeError(returned).Code)
			} else {
				config.Process.Logger.InfoContext(ctx, "Grok Build native handshake completed", "owner_id", config.Process.OwnerID)
			}
		}
	}()
	if config.Version != SupportedVersion {
		return incompatible()
	}
	phase = runtimePhase
	env, err := probeEnvironment(config)
	if err != nil {
		return err
	}
	config.Process.Env = env
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	phase = inspectPhase
	if err := inspect(bounded, config.Process); err != nil {
		return err
	}
	config.Process.Args = []string{"--no-auto-update", "agent", "stdio"}
	requestID := domain.NewID()
	wire := &probeWire{id: requestID, cwd: config.Process.Cwd, ready: make(chan struct{}, 1), cancel: cancel}
	config.Process.Stdout = &probeFrames{wire: wire}
	config.Process.Stderr = &probeStderr{wire: wire}
	phase = launchPhase
	if config.Process.Logger != nil {
		config.Process.Logger.InfoContext(ctx, "Grok Build native handshake started", "owner_id", config.Process.OwnerID, "profile_version", SupportedVersion)
	}
	h, err := process.Start(bounded, config.Process)
	if err != nil {
		return nativeLaunchError(err)
	}
	var writeDone chan struct{}
	defer func() {
		cancel()
		cleanup := h.Close()
		_ = h.Wait()
		if writeDone != nil {
			<-writeDone
		}
		if cleanup != nil {
			phase, returned = cleanupPhase, probeCleanupRequired()
			return
		}
		// The stdout drain can detect another/partial frame after the first
		// response woke us. Cleanup joins that drain before accepting success.
		if returned == nil {
			if problem := wire.status(); problem != nil {
				returned = problem
			} else if frames := config.Process.Stdout.(*probeFrames); len(frames.buffer) != 0 {
				returned = incompatible()
			}
		}
	}()
	if err := h.Resume(); err != nil {
		return nativeLaunchError(err)
	}
	phase = initializePhase
	request, _ := json.Marshal(struct {
		JSONRPC string           `json:"jsonrpc"`
		ID      domain.ID        `json:"id"`
		Method  string           `json:"method"`
		Params  initializeParams `json:"params"`
	}{JSONRPC: "2.0", ID: requestID, Method: "initialize", Params: initializeParams{
		ProtocolVersion: 1, ClientCapabilities: struct{}{}, ClientInfo: clientInfo{Name: "delidev", Title: "DeliDev", Version: "0.1.0"},
	}})
	writeDone = make(chan struct{})
	var writeError error
	go func() {
		raw := append(request, '\n')
		n, err := h.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
		writeError = err
		close(writeDone)
	}()
	// Closing the owned scope releases a blocked writer. Join it after cleanup
	// on every exit; a timeout never leaves a background stdin writer behind.
	select {
	case <-writeDone:
		if writeError != nil {
			if problem := wire.status(); problem != nil {
				return problem
			}
			return probeUnavailable()
		}
	case <-bounded.Done():
		if problem := wire.status(); problem != nil {
			return problem
		}
		return domain.SafeError(bounded.Err())
	case <-h.Done():
		if problem := wire.status(); problem != nil {
			return problem
		}
		return probeUnavailable()
	}
	select {
	case <-wire.ready:
	case <-bounded.Done():
	case <-h.Done():
	}
	if problem := wire.status(); problem != nil {
		return problem
	}
	if err := bounded.Err(); err != nil {
		return domain.SafeError(err)
	}
	if !wire.initialized() {
		return probeUnavailable()
	}
	return nil
}

type probeWire struct {
	mu        sync.Mutex
	id        domain.ID
	cwd       string
	ready     chan struct{}
	cancel    context.CancelFunc
	seen      bool
	inventory bool
	problem   *domain.Error
}

func (w *probeWire) status() *domain.Error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.problem
}

func (w *probeWire) initialized() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seen && w.inventory
}

func (w *probeWire) fail(problem *domain.Error) {
	w.mu.Lock()
	if w.problem == nil {
		w.problem = problem
	}
	w.mu.Unlock()
	w.cancel()
}

func (w *probeWire) receive(raw []byte) {
	w.mu.Lock()
	var err error
	switch {
	case !w.seen:
		err = validateInitialize(raw, w.id, w.cwd)
		if err == nil {
			w.seen = true
		}
	case !w.inventory:
		err = validateMCPInventory(raw)
		if err == nil {
			w.inventory = true
		}
	default:
		err = incompatible()
	}
	ready := err == nil && w.seen && w.inventory
	w.mu.Unlock()
	if err != nil {
		w.fail(domain.SafeError(err))
	} else if ready {
		w.ready <- struct{}{}
	}
}

type probeFrames struct {
	wire    *probeWire
	buffer  []byte
	stopped bool
}

func (w *probeFrames) Write(data []byte) (int, error) {
	length := len(data)
	for len(data) > 0 && !w.stopped {
		end := bytes.IndexByte(data, '\n')
		size := len(data)
		if end >= 0 {
			size = end
		}
		if len(w.buffer)+size > maxProbeFrame {
			w.stopped, w.buffer = true, nil
			w.wire.fail(domain.Fail(domain.ResourceExhausted, "Grok Build initialization exceeded its output bound.", "Use a supported native protocol profile and refresh discovery."))
			break
		}
		w.buffer = append(w.buffer, data[:size]...)
		data = data[size:]
		if end < 0 {
			break
		}
		data = data[1:]
		w.wire.receive(w.buffer)
		w.buffer = nil
		w.stopped = w.wire.status() != nil
	}
	// Draining continues after rejection until owned process cleanup finishes.
	return length, nil
}

type probeStderr struct {
	wire  *probeWire
	count int
}

func (w *probeStderr) Write(data []byte) (int, error) {
	if len(data) > maxProbeStderr-w.count {
		w.wire.fail(domain.Fail(domain.ResourceExhausted, "Grok Build initialization exceeded its diagnostic bound.", "Inspect the selected installation and refresh protocol discovery."))
	}
	w.count = min(maxProbeStderr, w.count+min(maxProbeStderr, len(data)))
	return len(data), nil
}

// Reject relative/empty lookup entries instead of giving a probe the current
// directory's executables. This does not inherit shell or credential context.
func probePath(value string) bool {
	if value == "" || strings.ContainsRune(value, 0) {
		return false
	}
	for _, entry := range filepath.SplitList(value) {
		if !filepath.IsAbs(entry) {
			return false
		}
	}
	return true
}
