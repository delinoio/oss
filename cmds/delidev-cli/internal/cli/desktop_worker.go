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
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func desktopWorkerAuthority(ctx context.Context, o options, expected string, clientID domain.ID) error {
	if o.server != "" || o.tokenStdin {
		return usage()
	}
	identity, err := security.LoadIdentity(o.dataDir)
	if err != nil {
		return err
	}
	endpoint, err := localEndpoint(o)
	if err != nil {
		return err
	}
	saved, err := worker.LoadCredential(filepath.Join(o.dataDir, "desktop-client"))
	if err != nil {
		return err
	}
	if clientID.Validate() != nil ||
		domain.OwnershipBlocks(domain.OwnershipDevice, domain.ID(saved.DeviceID), saved.DeviceID != clientID) ||
		endpoint.URL != expected ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(endpoint.ServerID), endpoint.ServerID != identity.ServerID) ||
		domain.OwnershipBlocks(domain.OwnershipActor, "", saved.Type != domain.ClientDevice) ||
		!localEndpointMatches(o, saved.ServerID, saved.Endpoint, expected) ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(saved.ServerID), saved.ServerID != identity.ServerID) {
		return recoveryRequired()
	}
	paired, err := connectClient(options{dataDir: filepath.Join(o.dataDir, "desktop-client"), desktop: o.desktop}, nil)
	if err != nil {
		return err
	}
	defer paired.transport.CloseIdleConnections()
	status, err := paired.system.GetStatus(ctx, request(paired, &pb.GetStatusRequest{}))
	if err != nil {
		return rpc.ClientError(err)
	}
	if status.Msg.ServerId != string(saved.ServerID) || status.Msg.ProtocolVersion != rpc.ProtocolVersion || status.Msg.Stopping {
		return recoveryRequired()
	}
	return nil
}

// Recovery authenticates the retained registration without issuing a grant.
// A revoked Worker must remain diagnostic rather than cycling generations.
func desktopWorkerRegistration(ctx context.Context, o options, root, expected string) error {
	identity, err := security.LoadIdentity(o.dataDir)
	if err != nil {
		return err
	}
	saved, err := worker.LoadCredential(root)
	if err != nil {
		return err
	}
	if domain.OwnershipBlocks(domain.OwnershipActor, "", saved.Type != domain.WorkerDevice) ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(saved.ServerID), saved.ServerID != identity.ServerID) ||
		!localEndpointMatches(o, saved.ServerID, saved.Endpoint, expected) {
		return recoveryRequired()
	}
	if o.desktop != nil {
		owner, local, err := desktopruntime.LocalRoot(root, saved.ServerID, saved.Endpoint)
		if err != nil {
			return err
		}
		if !local || owner != o.dataDir {
			return recoveryRequired()
		}
	}
	paired, err := connectClient(options{dataDir: o.dataDir, server: saved.Endpoint, tokenStdin: true, desktop: o.desktop}, strings.NewReader(saved.Token))
	if err != nil {
		return err
	}
	defer paired.transport.CloseIdleConnections()
	status, err := paired.system.GetStatus(ctx, request(paired, &pb.GetStatusRequest{}))
	if err != nil {
		return rpc.ClientError(err)
	}
	if status.Msg.ServerId != string(saved.ServerID) || status.Msg.ProtocolVersion != rpc.ProtocolVersion || status.Msg.Stopping {
		return recoveryRequired()
	}
	if o.desktop != nil {
		return desktopruntime.Follow(root, saved.ServerID)
	}
	return nil
}

// Short native controllers do not inherit the resident host's in-memory target.
// Load its private locator and retain the generation for this admission. Never
// use ordinary discovery or the immutable pairing address as a fallback.
func desktopWorkerTarget(ctx context.Context, o options) (context.Context, options, error) {
	if o.server != "" || o.tokenStdin {
		return ctx, o, usage()
	}
	identity, err := security.LoadIdentity(o.dataDir)
	if err != nil {
		return ctx, o, err
	}
	target, err := desktopruntime.Load(o.dataDir, identity.ServerID)
	if err != nil {
		return ctx, o, err
	}
	o.desktop = &target
	return desktopruntime.WithTarget(ctx, &target), o, nil
}

// Preparation is an intentional native bootstrap, not a renderer/read operation.
// Its selected private path never enters renderer metadata. The selected original
// process independently revalidates the signed selection below.
func desktopWorkerExecutable(ctx context.Context, o options, args []string) (result any, failure error) {
	phase := "arguments"
	defer func() {
		if failure != nil {
			slog.Warn("desktop_worker_preparation", "phase", phase, "code", domain.SafeError(failure).Code)
		}
	}()
	fs := flags("worker desktop-prepare")
	mode := fs.String("mode", "", "launch, retry or ensure")
	clientID := fs.String("client-id", "", "original desktop client identity")
	if err := parse(fs, args); err != nil {
		return nil, err
	}
	if (*mode != "launch" && *mode != "retry" && *mode != "ensure") || domain.ID(*clientID).Validate() != nil {
		return nil, usage()
	}
	bounded, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	phase = "runtime-target"
	bounded, o, err := desktopWorkerTarget(bounded, o)
	if err != nil {
		return nil, err
	}
	phase = "client-authority"
	if err := desktopWorkerAuthority(bounded, o, o.desktop.Endpoint, domain.ID(*clientID)); err != nil {
		return nil, err
	}
	phase = "local-pairing"
	if *mode != "ensure" {
		if _, err := pairLocalDeviceAt(bounded, o, filepath.Join(o.dataDir, "worker"), domain.WorkerDevice, false, o.desktop.Endpoint); err != nil {
			return nil, err
		}
	}
	phase = "worker-registration"
	if err := desktopWorkerRegistration(bounded, o, filepath.Join(o.dataDir, "worker"), o.desktop.Endpoint); err != nil {
		return nil, err
	}
	phase = "executable-selection"
	executable, err := os.Executable()
	if err != nil {
		return nil, domain.SafeError(err)
	}
	selected, err := worker.DesktopExecutable(filepath.Join(o.dataDir, "worker"), executable)
	if err != nil {
		return nil, err
	}
	return map[string]any{"executable": selected}, nil
}

func runDesktopWorkerHostCommand(ctx context.Context, o options, args []string, streams IO) int {
	// Preserve an admitted Worker after desktop crashes, including a lost first
	// reply. EOF and broken standard pipes confer no cancellation authority.
	pipeSignal := make(chan os.Signal, 1)
	signal.Notify(pipeSignal, syscall.SIGPIPE)
	defer signal.Stop(pipeSignal)
	ctx, o, err := desktopWorkerTarget(ctx, o)
	if err != nil {
		slog.Warn("desktop_worker_admission", "phase", "runtime-target", "code", domain.SafeError(err).Code)
		return emitResult(streams, o, nil, err)
	}
	value, err := desktopWorkerHost(ctx, o, args, streams, o.desktop.Endpoint)
	return emitResult(streams, o, value, err)
}

type desktopStopRequest struct {
	Version int    `json:"version"`
	Action  string `json:"action"`
}

// EOF is a crash signal, not a Stop authorization. Only the exact bounded
// control frame can close the admitted Worker's original generation.
func readDesktopStop(input io.Reader, stop chan<- struct{}) {
	reader := bufio.NewReader(input)
	const limit = 64 << 10
	for {
		var line []byte
		for {
			part, err := reader.ReadSlice('\n')
			if len(line)+len(part) > limit {
				return
			}
			line = append(line, part...)
			if err == bufio.ErrBufferFull {
				continue
			}
			var request desktopStopRequest
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.DisallowUnknownFields()
			var trailing any
			if decoder.Decode(&request) == nil && decoder.Decode(&trailing) == io.EOF && request.Version == 1 && request.Action == "stop" {
				close(stop)
				return
			}
			if err != nil {
				return
			}
			break
		}
	}
}

func desktopWorkerHost(ctx context.Context, o options, args []string, streams IO, expected string) (any, error) {
	fs := flags("worker desktop-host")
	mode := fs.String("mode", "", "launch, retry or ensure")
	clientID := fs.String("client-id", "", "original desktop client identity")
	exited := fs.String("exited-generation", "", "original native-observed exited child")
	if err := parse(fs, args); err != nil {
		return nil, err
	}
	if (*mode != "launch" && *mode != "retry" && *mode != "ensure") || domain.ID(*clientID).Validate() != nil || (*exited != "" && (*mode != "ensure" || domain.ID(*exited).Validate() != nil)) {
		return nil, usage()
	}
	stop := make(chan struct{})
	go readDesktopStop(streams.In, stop)
	defer func() {
		if input, ok := streams.In.(io.Closer); ok {
			input.Close()
		}
	}()
	running, cancel := context.WithCancel(ctx)
	defer cancel()
	finished, observed := make(chan struct{}), make(chan struct{})
	var original domain.ID
	var originalGate sync.Mutex
	root := filepath.Join(o.dataDir, "worker")
	go func() {
		defer close(observed)
		select {
		case <-finished:
			select {
			case <-stop:
			default:
				return
			}
		case <-stop:
		}
		originalGate.Lock()
		generation := original
		if generation != "" {
			if err := worker.RequestStop(root, generation); err != nil {
				slog.Warn("desktop_worker_shutdown", "phase", "suppression-unconfirmed", "code", domain.SafeError(err).Code)
			}
		}
		cancel()
		originalGate.Unlock()
	}()
	defer func() { close(finished); <-observed }()
	admission, endAdmission := context.WithTimeout(running, 35*time.Second)
	defer endAdmission()
	if err := desktopWorkerAuthority(admission, o, expected, domain.ID(*clientID)); err != nil {
		return nil, err
	}
	// Pairing reuses the exact journal; revoked/foreign/damaged credentials fail.
	// Supervision cannot create a new registration after initial launch.
	if _, err := worker.LoadCredential(root); errors.Is(err, os.ErrNotExist) && (*mode == "launch" || *mode == "retry") {
		if _, err := pairLocalDeviceAt(admission, o, root, domain.WorkerDevice, false, expected); err != nil {
			return nil, err
		}
	}
	if err := desktopWorkerRegistration(admission, o, root, expected); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, domain.SafeError(err)
	}
	selected, err := worker.DesktopExecutable(root, executable)
	if err != nil {
		return nil, err
	}
	if selected != executable {
		return nil, workerUpdateFailure()
	}
	service, err := userservice.AdmitKindLaunch(admission, root, userservice.Worker)
	if err != nil {
		return nil, err
	}
	startup, err := security.TryLock(filepath.Join(root, "worker-startup.lock"))
	if err != nil {
		service.Close()
		return nil, err
	}
	var release sync.Once
	unlock := func() { release.Do(func() { startup.Close(); service.Close() }) }
	defer unlock()
	installed, _, err := service.Managed()
	if err != nil {
		return nil, err
	}
	status, err := worker.Status(root)
	if err != nil {
		return nil, err
	}
	if installed {
		if status.ControllerActive && status.State == worker.StateRunning {
			return map[string]any{"started": false, "worker": status}, nil
		}
		return map[string]any{"state": "service-managed", "started": false, "worker": status}, nil
	}
	if status.State == worker.StateUncertain && !status.ControllerActive && domain.ID(*exited) != status.Lifecycle.Generation {
		return nil, recoveryRequired()
	}
	// Admission may have waited behind service/update control. Recheck signed
	// selection and retained authorization inside the final startup gate.
	selected, err = worker.DesktopExecutable(root, executable)
	if err != nil {
		return nil, err
	}
	if selected != executable {
		return nil, workerUpdateFailure()
	}
	if err := desktopWorkerAuthority(admission, o, expected, domain.ID(*clientID)); err != nil {
		return nil, err
	}
	if err := desktopWorkerRegistration(admission, o, root, expected); err != nil {
		return nil, err
	}
	originalGate.Lock()
	if err := admission.Err(); err != nil {
		originalGate.Unlock()
		return nil, domain.SafeError(err)
	}
	status, launch, err := worker.PrepareDesktopStart(root, *mode == "launch" || (*mode == "retry" && status.Lifecycle.Version == 0), domain.ID(*exited))
	if err != nil || !launch {
		originalGate.Unlock()
		return map[string]any{"started": false, "worker": status}, err
	}
	original = status.Lifecycle.Generation
	originalGate.Unlock()
	log, err := serviceLog(root, userservice.Worker)
	if err != nil {
		// The native owner still retains this child; its failed admission cannot
		// silently discard or reissue the reserved generation.
		return nil, err
	}
	defer log.Close()
	logger := slog.New(slog.NewJSONHandler(log, nil))
	// Admission pins its original host, while the surviving Local Worker uses
	// its existing proved locator transport to reconnect after a desktop restart.
	err = worker.Run(desktopruntime.WithTarget(running, nil), worker.Config{Root: root, StartupID: original, Logger: logger, Admitted: func(intent worker.Lifecycle) {
		unlock()
		// Publish ownership before readiness; server lease expiry can delay the
		// first attachment while this same Worker safely reconnects.
		status.Lifecycle, status.State, status.ControllerActive = intent, worker.StateStarting, true
		if err := json.NewEncoder(streams.Out).Encode(envelope{Version: 1, Result: map[string]any{"started": true, "generation": original, "worker": status}}); err != nil {
			logger.Warn("desktop_worker_control", "phase", "admission-delivery-lost")
		}
	}})
	var update *worker.UpdateHandoff
	if errors.As(err, &update) {
		// The explicit updater owns the replacement. Never adopt its detached
		// generation as this desktop's original child.
		return handoffWorkerUpdate(ctx, o, root, update.ID)
	}
	if err != nil && running.Err() != nil {
		err = nil
	}
	return map[string]any{"state": "exited", "generation": original}, err
}
