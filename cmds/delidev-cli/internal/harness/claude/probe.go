// Package claude owns Claude Code's private stream-json protocol. Discovery
// deliberately has no user-input or execution operation.
package claude

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

const SupportedVersion = domain.ClaudeProtocolVersion
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
	launchPhase     probePhase = "launch"
	initializePhase probePhase = "initialize"
	cleanupPhase    probePhase = "cleanup"
)

func incompatible() *domain.Error {
	return domain.Fail(domain.Unsupported, "The installed Claude Code does not match its native protocol profile.", "Select a validated Claude Code version and refresh protocol discovery; no account execution was authorized.")
}

func probeUnavailable() *domain.Error {
	return domain.Fail(domain.Unavailable, "Claude Code did not complete native initialization.", "Inspect the selected Worker installation and refresh protocol discovery.")
}

func probeCleanupRequired() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Claude Code probe cleanup could not be confirmed.", "Retain the private runtime and reconcile its owned process before retrying.")
}

// Probe sends exactly one initialize control request, never a prompt, login,
// session creation, permission response or model request. Its success proves
// only this installed protocol profile, not any account or execution feature.
func Probe(ctx context.Context, config ProbeConfig) (returned error) {
	phase := profilePhase
	defer func() {
		if config.Process.Logger != nil {
			if returned != nil {
				config.Process.Logger.WarnContext(ctx, "Claude Code native handshake failed", "owner_id", config.Process.OwnerID, "phase", phase, "code", domain.SafeError(returned).Code)
			} else {
				config.Process.Logger.InfoContext(ctx, "Claude Code native handshake completed", "owner_id", config.Process.OwnerID)
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
	// Bare mode skips OAuth/keychain and automatic project/user extensions.
	// These fixed flags are probe policy, never editable session settings.
	config.Process.Args = []string{"--bare", "--print", "--input-format=stream-json", "--output-format=stream-json", "--verbose", "--setting-sources=", "--strict-mcp-config", `--mcp-config={"mcpServers":{}}`, "--no-session-persistence", "--tools=", "--permission-mode=dontAsk", "--no-chrome", "--disable-slash-commands"}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	requestID := domain.NewID()
	wire := &probeWire{id: requestID, ready: make(chan struct{}, 1), cancel: cancel}
	config.Process.Stdout = &probeFrames{wire: wire}
	config.Process.Stderr = &probeStderr{wire: wire}
	phase = launchPhase
	if config.Process.Logger != nil {
		config.Process.Logger.InfoContext(ctx, "Claude Code native handshake started", "owner_id", config.Process.OwnerID, "profile_version", SupportedVersion)
	}
	h, err := process.Start(bounded, config.Process)
	if err != nil {
		return err
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
		return err
	}
	phase = initializePhase
	request, _ := json.Marshal(struct {
		Type      string    `json:"type"`
		RequestID domain.ID `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
			Hooks   any    `json:"hooks"`
		} `json:"request"`
	}{Type: "control_request", RequestID: requestID, Request: struct {
		Subtype string `json:"subtype"`
		Hooks   any    `json:"hooks"`
	}{Subtype: "initialize"}})
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
	mu      sync.Mutex
	id      domain.ID
	ready   chan struct{}
	cancel  context.CancelFunc
	seen    bool
	problem *domain.Error
}

func (w *probeWire) status() *domain.Error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.problem
}

func (w *probeWire) initialized() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seen
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
	if err := validateInitialize(raw, w.id); err != nil {
		w.fail(domain.SafeError(err))
		return
	}
	w.mu.Lock()
	if w.seen {
		w.mu.Unlock()
		w.fail(incompatible())
		return
	}
	w.seen = true
	w.mu.Unlock()
	w.ready <- struct{}{}
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
			w.wire.fail(domain.Fail(domain.ResourceExhausted, "Claude Code initialization exceeded its output bound.", "Use a supported native protocol profile and refresh discovery."))
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
		w.wire.fail(domain.Fail(domain.ResourceExhausted, "Claude Code initialization exceeded its diagnostic bound.", "Inspect the selected installation and refresh protocol discovery."))
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
