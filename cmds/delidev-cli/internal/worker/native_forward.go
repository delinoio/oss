// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeforward"
)

// Native inference may wait for provider response headers as long as the
// server relay does. The shorter control-plane bound would cut those requests.
const nativeForwardHeaderTimeout = 10*time.Minute + 30*time.Second

// openCodexNativeForwarder binds one execution-owned private loopback forwarder
// when, and only when, this Worker is a positively verified Local desktop Worker
// that currently follows an app-owned runtime and its selected route is Direct.
// It returns nil for Saved/remote Workers, unchanged Local addresses and
// configured-proxy routes, which keep their original native endpoint.
//
// The native process receives only the forwarder's private origin. Every
// forwarded request uses the Worker's selected desktop transport, which proves
// the current runtime generation and wraps the execution bearer before any
// bearer transmission; the pairing address is never a fallback.
func openCodexNativeForwarder(ctx context.Context, config Config, execution domain.ID, token string, titleProfile bool, snapshot *networkSnapshot) (*nativeforward.Forwarder, error) {
	connection := config.execution
	if connection == nil {
		return nil, nil
	}
	if snapshot != nil && snapshot.bundle.Route.Profile.Mode != domain.ProxyDirect {
		return nil, nil
	}
	credential := connection.Credential
	following := injectedDesktopTarget(ctx, config.Root, credential) != nil
	if !following {
		var err error
		following, err = desktopruntime.Following(config.Root, credential.ServerID, credential.Endpoint)
		if err != nil {
			logNativeForwardRefused(ctx, config, execution, err)
			return nil, err
		}
	}
	if !following {
		return nil, nil
	}
	client, closeIdle, err := networkHTTPClientForNativeRoute(ctx, config, credential, snapshot, nativeForwardHeaderTimeout)
	if err != nil {
		logNativeForwardRefused(ctx, config, execution, err)
		return nil, err
	}
	limit := nativeforward.MaxRequestBody
	if titleProfile {
		limit = nativeforward.MaxTitleBody
	}
	forwarder, err := nativeforward.Open(ctx, nativeforward.Config{OpenRouterTools: config.openRouterTools, Origin: credential.Endpoint, Token: token, Client: client, CloseIdle: closeIdle, BodyLimit: limit, Logger: config.Logger, ExecutionID: execution})
	if err != nil {
		closeIdle()
		logNativeForwardRefused(ctx, config, execution, err)
		return nil, err
	}
	return forwarder, nil
}

func logNativeForwardRefused(ctx context.Context, config Config, execution domain.ID, err error) {
	if config.Logger != nil {
		config.Logger.WarnContext(ctx, "worker_native_forward_refused", "execution_id", execution, "stage", string(nativeforward.StageOpen), "code", domain.SafeError(err).Code)
	}
}
