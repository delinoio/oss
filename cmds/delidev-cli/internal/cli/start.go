package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	return detachedStartup(ctx, o, config, streams, startupExplicit)
}

type startupMode uint8

const (
	startupExplicit startupMode = iota
	startupEnsure
	startupDesktopLaunch
	startupDesktopRetry
	startupObservation
)

func ensureDetached(ctx context.Context, o options, config server.Config, streams IO, automatic bool) (any, error) {
	mode := startupExplicit
	if automatic {
		mode = startupEnsure
	}
	return detachedStartup(ctx, o, config, streams, mode)
}

func detachedStartup(ctx context.Context, o options, config server.Config, streams IO, mode startupMode) (any, error) {
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
	privateDir := security.PrivateDir
	if mode == startupObservation {
		privateDir = security.CheckPrivateDir
	}
	if err := privateDir(o.dataDir); err != nil {
		return nil, domain.SafeError(err)
	}
	// Lock order is service admission, startup controller, lifecycle, then store.
	// Service control owns the same admission lock through its native write.
	var admission *userservice.LaunchAdmission
	if mode == startupDesktopLaunch || mode == startupDesktopRetry || mode == startupEnsure {
		admission, err = userservice.AdmitLaunch(ctx, o.dataDir)
		if err != nil {
			return nil, err
		}
		defer admission.Close()
	}
	lock, err := security.TryLock(filepath.Join(o.dataDir, "startup.lock"))
	if (mode == startupDesktopLaunch || mode == startupDesktopRetry || mode == startupObservation) && err != nil && domain.SafeError(err).Code == domain.Conflict {
		lock, err = waitStartupController(ctx, o.dataDir)
	}
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
	serviceManaged, serviceStopped := false, false
	if admission != nil {
		serviceManaged, serviceStopped, err = admission.Managed()
		if err != nil {
			return nil, err
		}
	}
	if (mode == startupDesktopLaunch || mode == startupDesktopRetry) && intent.Version != 0 && !intent.Matches(config) {
		return nil, domain.Fail(domain.Unsupported, "The retained server configuration is incompatible with desktop launch.", "Preserve the original server and inspect connection diagnostics.")
	}
	if mode == startupDesktopRetry && intent.State == server.DesiredStopped {
		return map[string]any{"state": "stopped"}, nil
	}
	if mode == startupEnsure || mode == startupObservation {
		if intent.State != server.DesiredRunning {
			return map[string]any{"state": "stopped"}, nil
		}
		if !intent.Matches(config) {
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
		if status.Msg.Stopping || intent.State == server.DesiredStopped {
			return nil, domain.Fail(domain.Conflict, "The server is stopping.", "Wait for confirmed shutdown before explicitly starting again.")
		}
		return map[string]any{"reused": true, "status": status.Msg}, nil
	}
	if status, err := probe(); err == nil {
		// Adopt legacy foreground servers only after authenticated compatibility.
		if intent.Version == 0 && mode != startupObservation && !serviceManaged {
			if _, err := server.WriteRunning(o.dataDir, config); err != nil {
				return nil, err
			}
		}
		return status, nil
	} else if code := domain.SafeError(err).Code; code != domain.ServerUnavailable && code != domain.Unavailable {
		// A fresh launch is intentional Start even while the previous Stop is
		// completing. Only that mode joins cleanup below; ordinary Start keeps
		// its live-stopping conflict and Retry never reopens stopped intent.
		if mode != startupDesktopLaunch || code != domain.Conflict {
			return nil, err
		}
	}
	if mode == startupObservation {
		return nil, domain.Fail(domain.ServerUnavailable, "DeliDev is not connected.", "Inspect connection diagnostics before an explicit retry.")
	}
	if admission != nil && serviceManaged {
		if mode == startupDesktopLaunch || mode == startupDesktopRetry {
			return map[string]any{"state": "service-managed"}, nil
		}
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
	if admission != nil {
		if managed, _, err := admission.Managed(); err != nil || managed {
			if err != nil {
				return nil, err
			}
			return nil, domain.Fail(domain.RecoveryRequired, "Native service ownership changed.", "Inspect the original registration before retrying.")
		}
	}
	if mode != startupEnsure {
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
	if admission != nil {
		if managed, _, err := admission.Managed(); err != nil || managed {
			if err != nil {
				return nil, err
			}
			return nil, domain.Fail(domain.RecoveryRequired, "Native service ownership changed.", "Inspect the original registration before retrying.")
		}
	}
	if config.Logger != nil {
		config.Logger.InfoContext(ctx, "server_start_admitted", "mode", mode)
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

// Concurrent fresh hosts join the original bounded startup rather than failing
// with a controller conflict. This grants no termination or cleanup authority.
func waitStartupController(ctx context.Context, root string) (*security.Lock, error) {
	child, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		lock, err := security.TryLock(filepath.Join(root, "startup.lock"))
		if err == nil || domain.SafeError(err).Code != domain.Conflict {
			return lock, err
		}
		select {
		case <-child.Done():
			return nil, domain.SafeError(child.Err())
		case <-ticker.C:
		}
	}
}
