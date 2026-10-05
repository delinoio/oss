// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"time"
)

type serverSubscriptionNative interface {
	StartManagedLogin(context.Context, bool) (codex.ManagedLoginProgress, error)
	WaitManagedLogin(context.Context, string) error
	CancelManagedLogin(context.Context, string) error
	ManagedBundle(context.Context, bool) ([]byte, error)
	LogoutManaged(context.Context) error
	Close([]byte) error
}

// A non-recovery opener error proves joined native and private-file cleanup.
// A recovery error retains the original operation and runtime for inspection.
type serverSubscriptionOpener func(context.Context, string, domain.ID, []byte, *slog.Logger) (serverSubscriptionNative, error)

type serverCodexRuntime struct {
	*codex.Client
	home      string
	original  os.FileInfo
	guard     *http.Server
	guardDone chan struct{}
}

func openServerSubscription(ctx context.Context, root string, owner domain.ID, bundle []byte, logger *slog.Logger) (returned serverSubscriptionNative, err error) {
	runtimeRoot := filepath.Join(root, "subscription-runtime")
	home := filepath.Join(runtimeRoot, "auth", string(owner))
	if _, e := os.Lstat(home); !errors.Is(e, os.ErrNotExist) {
		return nil, subscriptionDenied()
	}
	env, e := harness.PrivateRuntimeEnvironment(home)
	if e != nil {
		return nil, subscriptionDenied()
	}
	info, e := os.Stat(home)
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
	installation, e := harness.DiscoverCodex(ctx, harness.DiscoveryConfig{Root: runtimeRoot, OwnerID: owner, Logger: logger})
	if e != nil {
		return nil, e
	}
	if installation.State != domain.InstallationDetected || !domain.CodexVersionAllowed(installation.Version) || !installation.ProtocolVerified || installation.ExecutableSHA256 == "" {
		return nil, domain.Fail(domain.Unsupported, "Browser sign-in is unavailable on this server.", "Install the supported Codex version on the server and start a new explicit login.")
	}
	digest, e := harness.InspectExecutable(ctx, installation.ResolvedPath)
	if e != nil || digest != installation.ExecutableSHA256 {
		return nil, subscriptionDenied()
	}
	nativeHome := filepath.Join(home, "codex")
	if len(bundle) != 0 {
		if _, _, e := subscription.Parse(bundle); e != nil {
			return nil, e
		}
		if e := security.WriteAtomic(filepath.Join(nativeHome, "auth.json"), bundle); e != nil {
			return nil, subscriptionDenied()
		}
	}
	native, e := codex.Open(ctx, codex.Config{Version: installation.Version, Mode: codex.SubscriptionProtocol, ManagedAuthentication: true, Home: nativeHome, Process: process.Config{Directory: filepath.Join(runtimeRoot, "processes"), OwnerID: owner, Executable: installation.ResolvedPath, Cwd: home, Env: env, Logger: logger}})
	if e != nil {
		return nil, e
	}
	return &serverCodexRuntime{Client: native, home: home, original: info}, nil
}

func (r *serverCodexRuntime) StartManagedLogin(ctx context.Context, device bool) (codex.ManagedLoginProgress, error) {
	if !device {
		// Codex 0.151.0 sends /cancel to an occupied preferred port before using
		// its registered fallback port. Own that preferred listener throughout
		// login so the adapter cannot cancel somebody else's login. The fallback
		// never sends /cancel. Remove this guard when the pinned native protocol
		// permits an exclusively owned callback port without foreign cancellation.
		listener, err := net.Listen("tcp4", "127.0.0.1:1455")
		if err != nil {
			return codex.ManagedLoginProgress{}, domain.Fail(domain.Unavailable, "Another browser login is using the server's login port.", "Finish that login before starting another; no existing login was canceled.")
		}
		r.guard = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNotFound)
		}), ReadHeaderTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
		r.guardDone = make(chan struct{})
		go func() { defer close(r.guardDone); _ = r.guard.Serve(listener) }()
	}
	return r.Client.StartManagedLogin(ctx, device)
}

func (r *serverCodexRuntime) Close(latest []byte) error {
	err := r.Client.Close()
	if r.guard != nil {
		_ = r.guard.Close()
		<-r.guardDone
	}
	if err != nil {
		return subscriptionDenied()
	}
	if len(latest) > 0 {
		closed, readErr := security.ReadPrivate(filepath.Join(r.home, "codex", "auth.json"), subscription.MaxBundle)
		equal := readErr == nil && bytes.Equal(closed, latest)
		clear(closed)
		if !equal {
			return subscriptionDenied()
		}
	}
	return subscription.CleanupRuntime(r.home, r.original)
}
