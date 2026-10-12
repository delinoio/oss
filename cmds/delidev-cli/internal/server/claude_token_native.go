// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
)

type serverClaudeTokenNative interface {
	Progress() (claude.AuthLoginProgress, error)
	Submit([]byte) error
	Done() <-chan struct{}
	Token() ([]byte, error)
	Close() error
}
type serverClaudeTokenOpener func(context.Context, string, domain.ID, *slog.Logger) (serverClaudeTokenNative, error)
type serverClaudeTokenRuntime struct {
	*claude.SetupTokenLogin
	home     string
	original os.FileInfo
}

func (r *serverClaudeTokenRuntime) Close() error {
	if err := r.SetupTokenLogin.Close(); err != nil {
		return err
	}
	return subscription.CleanupRuntime(r.home, r.original)
}
func openServerClaudeToken(ctx context.Context, root string, owner domain.ID, logger *slog.Logger) (returned serverClaudeTokenNative, err error) {
	// The inspected setup-token distribution is Darwin ARM64 2.1.236. Additional
	// platforms must supply their pinned native source/packaging evidence before
	// widening this gate; terminal availability alone is not profile support.
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nil, domain.Fail(domain.Unsupported, "Server Claude sign-in is unavailable on this platform.", "Use a server with the verified Claude setup-token profile.")
	}
	base := filepath.Join(root, "subscription-runtime")
	home := filepath.Join(base, "claude-token", string(owner))
	if _, e := os.Lstat(home); !errors.Is(e, os.ErrNotExist) {
		return nil, subscriptionDenied()
	}
	env, e := harness.PrivateRuntimeEnvironment(home)
	if e != nil {
		return nil, e
	}
	info, e := security.StableStat(home)
	if e != nil {
		return nil, subscriptionDenied()
	}
	defer func() {
		if err != nil && domain.SafeError(err).Code != domain.RecoveryRequired {
			if subscription.CleanupRuntime(home, info) != nil {
				err = subscriptionDenied()
			}
		}
	}()
	installation, e := harness.DiscoverClaude(ctx, harness.DiscoveryConfig{Root: base, OwnerID: owner, Logger: logger})
	if e != nil {
		return nil, e
	}
	if installation.Version != claude.SupportedVersion || installation.State != domain.InstallationDetected || !installation.ProtocolVerified || installation.Problem != nil || installation.Protocol == nil || installation.Protocol.State != domain.ProtocolVerified || installation.ExecutableSHA256 == "" {
		return nil, domain.Fail(domain.Unsupported, "Server sign-in requires verified Claude Code 2.1.236.", "Install the supported original CLI on this server.")
	}
	digest, e := harness.InspectExecutable(ctx, installation.ResolvedPath)
	if e != nil || digest != installation.ExecutableSHA256 {
		return nil, subscriptionDenied()
	}
	config := claude.AuthConfig{Version: installation.Version, Home: filepath.Join(home, "claude"), Process: process.Config{Directory: filepath.Join(base, "processes"), OwnerID: owner, Executable: installation.ResolvedPath, Cwd: home, Env: env, Logger: logger}}
	if e := claude.VerifyAuthVersion(ctx, config); e != nil {
		return nil, e
	}
	login, e := claude.StartSetupToken(ctx, config, "/usr/bin/true")
	if e != nil {
		return nil, e
	}
	return &serverClaudeTokenRuntime{login, home, info}, nil
}
