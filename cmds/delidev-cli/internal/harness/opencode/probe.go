// Package opencode owns DeliDev's private authenticated OpenCode HTTP profile.
// Discovery never attaches to a pre-existing native server or creates sessions.
package opencode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

const SupportedVersion = domain.OpenCodeProtocolVersion
const maxOutput = 64 << 10

type ProbeConfig struct {
	Process process.Config
	Version string
	Home    string
}

type probePhase string

const (
	profilePhase  probePhase = "profile"
	runtimePhase  probePhase = "runtime"
	launchPhase   probePhase = "launch"
	listenerPhase probePhase = "listener"
	authPhase     probePhase = "authentication"
	healthPhase   probePhase = "health"
	configPhase   probePhase = "configuration"
	schemaPhase   probePhase = "schema"
	cleanupPhase  probePhase = "cleanup"
)

func incompatible() *domain.Error {
	return domain.Fail(domain.Unsupported, "The installed OpenCode does not match its native protocol profile.", "Select a validated native OpenCode version and refresh discovery; no execution was authorized.")
}
func unavailable() *domain.Error {
	return domain.Fail(domain.Unavailable, "OpenCode native protocol initialization did not complete.", "Inspect the selected Worker installation and refresh protocol discovery.")
}
func cleanupRequired() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "OpenCode probe cleanup could not be confirmed.", "Retain the private runtime and reconcile its owned process before retrying.")
}
func launchError(err error) error {
	problem := domain.SafeError(err)
	if problem.Code == domain.Internal {
		return unavailable()
	}
	return problem
}

// Probe owns one freshly authenticated loopback server. Only global health,
// configuration and static schema reads are allowed; project/session endpoints
// initialize native execution state and belong to a separate execution adapter.
func Probe(ctx context.Context, config ProbeConfig) (returned error) {
	phase := profilePhase
	defer func() {
		if config.Process.Logger == nil {
			return
		}
		if returned != nil {
			config.Process.Logger.WarnContext(ctx, "OpenCode native handshake failed", "owner_id", config.Process.OwnerID, "phase", phase, "code", domain.SafeError(returned).Code)
		} else {
			config.Process.Logger.InfoContext(ctx, "OpenCode native handshake completed", "owner_id", config.Process.OwnerID, "profile_version", SupportedVersion)
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
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return unavailable()
	}
	password := base64.RawURLEncoding.EncodeToString(secret)
	config.Process.Env = append(env, "OPENCODE_SERVER_USERNAME=delidev", "OPENCODE_SERVER_PASSWORD="+password)
	// Reserve an ephemeral port until the owned native process is prepared.
	// The native listener must bind this exact port or fail; no fallback/adoption
	// is permitted. HTTP starts only after that process's SDK startup record.
	reservation, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return unavailable()
	}
	defer reservation.Close()
	port := strconv.Itoa(reservation.Addr().(*net.TCPAddr).Port)
	address := net.JoinHostPort("127.0.0.1", port)
	origin := "http://" + address
	config.Process.Args = []string{"serve", "--hostname=127.0.0.1", "--port=" + port, "--mdns=false"}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output := &startupOutput{expected: "opencode server listening on " + origin, ready: make(chan struct{}), cancel: cancel}
	config.Process.Stdout = output
	config.Process.Stderr = &diagnosticOutput{output: output}
	phase = launchPhase
	h, err := process.Start(bounded, config.Process)
	if err != nil {
		return launchError(err)
	}
	defer func() {
		cancel()
		cleanup := h.Close()
		_ = h.Wait()
		if cleanup != nil {
			phase, returned = cleanupPhase, cleanupRequired()
			return
		}
		// Closing joins both output drains. Late extra output invalidates an
		// otherwise successful HTTP handshake rather than being discarded.
		if returned == nil {
			if problem := output.status(); problem != nil {
				returned = problem
			} else if len(output.buffer) != 0 {
				returned = incompatible()
			}
		}
	}()
	if err := reservation.Close(); err != nil {
		return unavailable()
	}
	if err := h.Resume(); err != nil {
		return launchError(err)
	}
	phase = listenerPhase
	select {
	case <-output.ready:
	case <-bounded.Done():
	case <-h.Done():
	}
	if problem := output.status(); problem != nil {
		return problem
	}
	select {
	case <-output.ready:
	default:
		return unavailable()
	}
	client, transport := probeHTTPClient(address)
	defer transport.CloseIdleConnections()
	read := func(path, credential string, status int) ([]byte, error) {
		select {
		case <-h.Done():
			return nil, unavailable()
		default:
		}
		if problem := output.status(); problem != nil {
			return nil, problem
		}
		return readHTTP(bounded, client, origin, path, credential, status)
	}
	phase = authPhase
	if _, err := read("/global/health", "", 401); err != nil {
		return err
	}
	if _, err := read("/global/health", "incorrect-"+password, 401); err != nil {
		return err
	}
	phase = healthPhase
	health, err := read("/global/health", password, 200)
	if err != nil {
		return err
	}
	if validateHealth(health) != nil {
		return incompatible()
	}
	phase = configPhase
	global, err := read("/global/config", password, 200)
	if err != nil {
		return err
	}
	if validateEmptyConfig(global) != nil {
		return incompatible()
	}
	phase = schemaPhase
	schema, err := read("/doc", password, 200)
	if err != nil {
		return err
	}
	if validateSchema(schema) != nil {
		return incompatible()
	}
	select {
	case <-h.Done():
		return unavailable()
	default:
	}
	return nil
}

// This exact readiness line is the official SDK's owned-server startup
// contract, not a TTY transcript or a source of business/session events.
type startupOutput struct {
	mu       sync.Mutex
	expected string
	buffer   []byte
	count    int
	seen     bool
	problem  *domain.Error
	ready    chan struct{}
	cancel   context.CancelFunc
}

func (w *startupOutput) status() *domain.Error { w.mu.Lock(); defer w.mu.Unlock(); return w.problem }
func (w *startupOutput) fail(problem *domain.Error) {
	w.mu.Lock()
	if w.problem == nil {
		w.problem = problem
	}
	w.mu.Unlock()
	w.cancel()
}
func (w *startupOutput) Write(data []byte) (int, error) {
	length := len(data)
	if len(data) > maxOutput-w.count {
		w.fail(domain.Fail(domain.ResourceExhausted, "OpenCode startup output exceeded its bound.", "Inspect the selected installation and refresh discovery."))
	}
	w.count = min(maxOutput, w.count+min(maxOutput, len(data)))
	for len(data) > 0 && w.status() == nil {
		end := bytes.IndexByte(data, '\n')
		size := len(data)
		if end >= 0 {
			size = end
		}
		if len(w.buffer)+size > 1024 {
			w.fail(incompatible())
			break
		}
		w.buffer = append(w.buffer, data[:size]...)
		data = data[size:]
		if end < 0 {
			break
		}
		data = data[1:]
		if w.seen || string(w.buffer) != w.expected {
			w.fail(incompatible())
			break
		}
		w.seen = true
		w.buffer = nil
		close(w.ready)
	}
	return length, nil
}

type diagnosticOutput struct {
	output *startupOutput
	count  int
}

func (w *diagnosticOutput) Write(data []byte) (int, error) {
	if len(data) > maxOutput-w.count {
		w.output.fail(domain.Fail(domain.ResourceExhausted, "OpenCode initialization exceeded its diagnostic bound.", "Inspect the selected installation and refresh discovery."))
	}
	w.count = min(maxOutput, w.count+min(maxOutput, len(data)))
	return len(data), nil
}

var _ io.Writer = (*startupOutput)(nil)
