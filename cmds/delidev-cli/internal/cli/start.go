package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func startDetached(ctx context.Context, o options, config server.Config, streams IO) (any, error) {
	return ensureDetached(ctx, o, config, streams, false)
}

type startupMode uint8

const (
	explicitStartup startupMode = iota
	intentRecovery
	desktopLaunch
	desktopRetry
)

func ensureDetached(ctx context.Context, o options, config server.Config, streams IO, automatic bool) (any, error) {
	mode := explicitStartup
	if automatic {
		mode = intentRecovery
	}
	return startDetachedMode(ctx, o, config, streams, mode)
}

func startDetachedMode(ctx context.Context, o options, config server.Config, streams IO, mode startupMode) (any, error) {
	automatic := mode == intentRecovery
	if _, err := worker.LoadCredential(o.dataDir); err == nil {
		return nil, domain.Fail(domain.PermissionDenied, "Server startup requires an owner scope, not a paired device scope.", "Run the lifecycle command on the server machine with its original data directory.")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := server.ValidateConfig(config); err != nil {
		return nil, err
	}
	tlsConfig, err := startupTLS(config)
	if err != nil {
		return nil, err
	}
	if err := security.PrivateDir(o.dataDir); err != nil {
		return nil, domain.SafeError(err)
	}
	// Service management takes its control lock before touching product locks.
	// Hold the same admission through intent publication and native spawn; a
	// snapshot-only check would race installation or removal.
	if mode == desktopLaunch || mode == desktopRetry {
		admission, err := userservice.LockStartupAdmission(ctx, o.dataDir, userservice.Server)
		if err != nil {
			return nil, err
		}
		defer admission.Close()
	}
	lock, err := startupControllerLock(ctx, o.dataDir, mode)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	intentLock, err := server.LockLifecycle(o.dataDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if intentLock != nil {
			intentLock.Close()
		}
	}()
	intent, err := server.ReadLifecycle(o.dataDir)
	if err != nil {
		return nil, err
	}
	if mode == desktopRetry && intent.Version != 0 && intent.State == server.DesiredStopped {
		return map[string]any{"state": "stopped"}, nil
	}
	serviceManaged, serviceStopped := false, false
	if automatic || (mode == desktopLaunch || mode == desktopRetry) {
		serviceManaged, serviceStopped, err = userservice.ManagedIntent(o.dataDir, userservice.Server)
		if err != nil {
			return nil, err
		}
		if automatic && intent.State != server.DesiredRunning {
			return map[string]any{"state": "stopped"}, nil
		}
		if automatic && !intent.Matches(config) {
			return nil, domain.Fail(domain.Unsupported, "Automatic startup configuration differs from the original server.", "Use the original controller configuration or explicitly start the desired server after reviewing its state.")
		}
	}
	probe := func() (any, error) {
		c, err := startupClient(o, streams.In, tlsConfig)
		if err != nil {
			return nil, err
		}
		defer c.transport.CloseIdleConnections()
		check, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		status, err := c.system.GetStatus(check, request(c, &pb.GetStatusRequest{}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		if status.Msg.ProtocolVersion != rpc.ProtocolVersion || status.Msg.Version != rpc.Version {
			return nil, domain.Fail(domain.Unsupported, "A different server version already owns this scope.", "Use its compatible CLI or explicitly stop it after reviewing active sessions.")
		}
		if (mode == desktopLaunch || mode == desktopRetry) && !strings.HasSuffix(config.Listen, ":0") {
			scheme := "http://"
			if config.TLSCertificate != "" {
				scheme = "https://"
			}
			if status.Msg.Listener != scheme+config.Listen {
				return nil, domain.Fail(domain.Unsupported, "A different listener owns the original server scope.", "Preserve its sessions and use its original compatible client or explicitly stop it before changing the listener.")
			}
		}
		if status.Msg.Stopping || intent.State == server.DesiredStopped {
			return nil, domain.Fail(domain.Conflict, "The server is stopping.", "Wait for confirmed shutdown before explicitly starting again.")
		}
		return map[string]any{"reused": true, "status": status.Msg}, nil
	}
	if status, err := probe(); err == nil {
		// Adopt legacy foreground servers only after authenticated compatibility.
		if intent.Version == 0 {
			if _, err := server.WriteRunning(o.dataDir, config); err != nil {
				return nil, err
			}
		}
		return status, nil
	} else if code := domain.SafeError(err).Code; code != domain.ServerUnavailable && code != domain.Unavailable {
		return nil, err
	}
	if (mode == desktopLaunch || mode == desktopRetry) && serviceManaged {
		if config.Logger != nil {
			config.Logger.InfoContext(ctx, "desktop_launch_blocked", "reason", "native_service_owned")
		}
		return map[string]any{"state": "service-managed"}, nil
	}
	if automatic && serviceManaged {
		if serviceStopped {
			return map[string]any{"state": "stopped"}, nil
		}
		return nil, domain.Fail(domain.RecoveryRequired, "The registered native service has not confirmed readiness.", "Inspect user service status on this computer; automatic controllers cannot launch a competing server process.")
	}
	// Endpoint removal precedes the original store lock release during final
	// shutdown. Join that ownership boundary before publishing a new intent or
	// spawning a replacement; an absent endpoint is not proof of cleanup.
	ownership, err := waitStartupOwnership(ctx, o.dataDir, config)
	if err != nil {
		return nil, err
	}
	defer func() {
		if ownership != nil {
			ownership.Close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	if !automatic {
		intent, err = server.WriteRunning(o.dataDir, config)
		if err != nil {
			return nil, err
		}
	}
	if err := ownership.Close(); err != nil {
		return nil, domain.SafeError(err)
	}
	ownership = nil
	if err := intentLock.Close(); err != nil {
		return nil, domain.SafeError(err)
	}
	intentLock = nil
	executable, err := os.Executable()
	if err != nil {
		return nil, domain.SafeError(err)
	}
	args := []string{"--data-dir", o.dataDir, "server", "run", "--listen", config.Listen, "--startup-id", string(intent.Generation)}
	if config.TLSCertificate != "" {
		args = append(args, "--tls-cert", config.TLSCertificate, "--tls-key", config.TLSKey)
	}
	if len(config.AllowedOrigins) > 0 {
		value := ""
		for i, origin := range config.AllowedOrigins {
			if i > 0 {
				value += ","
			}
			value += origin
		}
		args = append(args, "--allowed-origins", value)
	}
	path := filepath.Join(o.dataDir, "server.log")
	if _, err := os.Lstat(path); err == nil {
		if err := security.RegularPrivate(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, domain.SafeError(err)
	}
	log, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	defer log.Close()
	cmd := exec.Command(executable, args...)
	cmd.Stdin = nil
	cmd.Stdout = log
	cmd.Stderr = log
	detach(cmd)
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	if err := cmd.Start(); err != nil {
		return nil, domain.Fail(domain.Unavailable, "The server process could not start.", "Inspect the executable and data directory permissions.")
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, domain.Fail(domain.RecoveryRequired, "Startup was interrupted after creating the server process.", "Inspect `delidev server status` before retrying; the server may still become ready.")
		case <-timeout.C:
			return nil, domain.Fail(domain.RecoveryRequired, "The server has not reported readiness yet.", "Inspect server status and the private server log before retrying startup.")
		case <-exited:
			if current, err := server.ReadLifecycle(o.dataDir); err == nil && current.State == server.DesiredStopped {
				return map[string]any{"state": "stopped"}, nil
			}
			return nil, domain.Fail(domain.Unavailable, "The server exited before becoming ready.", "Inspect the private server log for a typed startup failure.")
		case <-ticker.C:
			if result, err := probe(); err == nil {
				return map[string]any{"started": true, "server": result}, nil
			} else if domain.SafeError(err).Code == domain.Unsupported {
				return nil, err
			}
		}
	}
}

// Startup already holds the controller and lifecycle locks. A still-owned
// database may be finishing shutdown or may be an unavailable live server;
// bounded waiting grants neither termination nor configuration replacement.
func waitStartupOwnership(ctx context.Context, root string, config server.Config) (*security.Lock, error) {
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	waiting := false
	for {
		if err := deadline.Err(); err != nil {
			if ctx.Err() != nil {
				return nil, domain.SafeError(ctx.Err())
			}
			return nil, domain.Fail(domain.Conflict, "The original server still owns this data scope.", "Wait for confirmed shutdown or inspect the original server before retrying startup.")
		}
		lock, err := security.TryLock(filepath.Join(root, "server.lock"))
		if err == nil {
			if deadline.Err() != nil {
				lock.Close()
				return nil, domain.SafeError(deadline.Err())
			}
			return lock, nil
		}
		if domain.SafeError(err).Code != domain.Conflict {
			return nil, domain.SafeError(err)
		}
		if !waiting && config.Logger != nil {
			config.Logger.InfoContext(ctx, "server_start_waiting_for_original_ownership")
		}
		waiting = true
		select {
		case <-deadline.Done():
		case <-ticker.C:
		}
	}
}

// A second desktop joins the original startup rather than publishing a busy
// launch outcome. Ordinary explicit CLI start retains its fail-fast admission.
func startupControllerLock(ctx context.Context, root string, mode startupMode) (*security.Lock, error) {
	if mode != desktopLaunch && mode != desktopRetry {
		return security.TryLock(filepath.Join(root, "startup.lock"))
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, domain.SafeError(err)
		}
		lock, err := security.TryLock(filepath.Join(root, "startup.lock"))
		if err == nil {
			return lock, nil
		}
		if domain.SafeError(err).Code != domain.Conflict {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, domain.SafeError(ctx.Err())
		case <-ticker.C:
		}
	}
}

// Keep server admission and the retained initial client pairing in one bounded
// Go-owned operation. Two fresh hosts cannot race the client recovery/pair gate.
func joinDesktopBootstrap(ctx context.Context, root string) (*security.Lock, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, domain.SafeError(err)
		}
		gate, err := security.TryLock(filepath.Join(root, "desktop-bootstrap.lock"))
		if err == nil {
			return gate, nil
		}
		if domain.SafeError(err).Code != domain.Conflict {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, domain.SafeError(ctx.Err())
		case <-ticker.C:
		}
	}
}
