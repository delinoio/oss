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
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func startDetached(ctx context.Context, o options, config server.Config, streams IO) (any, error) {
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
	lock, err := security.TryLock(filepath.Join(o.dataDir, "startup.lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
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
		return map[string]any{"reused": true, "status": status.Msg}, nil
	}
	if status, err := probe(); err == nil {
		return status, nil
	} else if domain.SafeError(err).Code == domain.Unsupported || domain.SafeError(err).Code == domain.RecoveryRequired {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, domain.SafeError(err)
	}
	args := []string{"--data-dir", o.dataDir, "server", "run", "--listen", config.Listen}
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
