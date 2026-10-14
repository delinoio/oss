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
	versionPhase          probePhase = "version"
	profilePhase          probePhase = "profile"
	runtimePhase          probePhase = "runtime"
	inspectPhase          probePhase = "inspect"
	runtimeInspectPhase   probePhase = "runtime-inspect"
	workspaceInspectPhase probePhase = "workspace-inspect"
	authenticatePhase     probePhase = "authenticate"
	launchPhase           probePhase = "launch"
	initializePhase       probePhase = "initialize"
	cleanupPhase          probePhase = "cleanup"
)

// diagnosticPhase maps an internal probe phase to the closed wire-visible
// diagnostic phase. Unknown phases fall back to profile validation.
func (p probePhase) diagnosticPhase() domain.GrokPhase {
	switch p {
	case versionPhase:
		return domain.GrokVersion
	case runtimePhase, launchPhase, initializePhase:
		return domain.GrokRuntime
	case authenticatePhase:
		return domain.GrokLogin
	case cleanupPhase:
		return domain.GrokCleanup
	default:
		// profile, inspect, runtime-inspect and workspace-inspect validate the
		// native configuration profile.
		return domain.GrokProfile
	}
}

func incompatible() *domain.Error {
	return domain.Fail(domain.Unsupported, "The installed Grok Build does not match its native protocol profile.", "Select a validated Grok Build version and refresh protocol discovery; no account execution was authorized.")
}

// inspectFailurePhase classifies a failed configuration inspection. Its owned
// process runs to completion inside inspect, so a recovery-required result
// means that process was not confirmed stopped: that is a cleanup failure, not
// a profile validation failure.
func inspectFailurePhase(err error) probePhase {
	if domain.SafeError(err).Code == domain.RecoveryRequired {
		return cleanupPhase
	}
	return inspectPhase
}

func probeUnavailable() *domain.Error {
	return domain.Fail(domain.Unavailable, "Grok Build did not complete native initialization.", "Inspect the selected Worker installation and refresh protocol discovery.")
}

// closeProbeHandle closes the owned native process scope. Tests replace it to
// inject an unconfirmed cleanup after the real scope is closed.
var closeProbeHandle = func(h *process.Handle) error { return h.Close() }

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
	phase := versionPhase
	// cleanupFailure records unconfirmed owned-process cleanup. It never replaces
	// an earlier probe failure; see domain.WithGrokCleanupFailure.
	var cleanupFailure *domain.Error
	defer func() {
		// The first native failure keeps its original phase and the installed
		// version; no native text enters the diagnostic. An unconfirmed cleanup
		// alone is reported as the cleanup phase, and after an earlier failure it
		// only upgrades the transport error to recovery_required.
		returned = domain.WithGrokCleanupFailure(config.Version, phase.diagnosticPhase(), returned, cleanupFailure)
		if config.Process.Logger != nil {
			if returned != nil {
				config.Process.Logger.WarnContext(ctx, "Grok Build native handshake failed", "owner_id", config.Process.OwnerID, "phase", phase, "diagnostic_phase", domain.GrokErrorDiagnostic(returned).Phase, "diagnostic_code", domain.GrokErrorDiagnostic(returned).Code, "code", domain.SafeError(returned).Code, "cleanup_unconfirmed", cleanupFailure != nil, "installed_version", config.Version, "supported_version", domain.GrokSubscriptionBaselineVersion)
			} else {
				config.Process.Logger.InfoContext(ctx, "Grok Build native handshake completed", "owner_id", config.Process.OwnerID)
			}
		}
	}()
	if !domain.HarnessVersionAllowed(domain.GrokBuild, config.Version) {
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
		phase = inspectFailurePhase(err)
		return err
	}
	config.Process.Args = []string{"--no-auto-update", "agent", "stdio"}
	requestID := domain.NewID()
	wire := &probeWire{id: requestID, cwd: config.Process.Cwd, ready: make(chan struct{}, 1), cancel: cancel}
	config.Process.Stdout = &probeFrames{wire: wire}
	config.Process.Stderr = &probeStderr{wire: wire}
	phase = launchPhase
	if config.Process.Logger != nil {
		config.Process.Logger.InfoContext(ctx, "Grok Build native handshake started", "owner_id", config.Process.OwnerID, "installed_version", config.Version, "baseline_version", SupportedVersion)
	}
	h, err := process.Start(bounded, config.Process)
	if err != nil {
		return nativeLaunchError(err)
	}
	var writeDone chan struct{}
	defer func() {
		cancel()
		cleanup := closeProbeHandle(h)
		_ = h.Wait()
		if writeDone != nil {
			<-writeDone
		}
		if cleanup != nil {
			cleanupFailure = probeCleanupRequired()
		}
		// The stdout drain can detect another/partial frame after the first
		// response woke us. Cleanup joins that drain before accepting success.
		// A late protocol failure precedes the cleanup outcome, so it is the first
		// failure; the frame buffer is only read once the drain is joined.
		if returned == nil {
			if problem := wire.status(); problem != nil {
				returned = problem
			} else if frames := config.Process.Stdout.(*probeFrames); cleanup == nil && len(frames.buffer) != 0 {
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
