// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type client struct {
	installation  delidevv1connect.InstallationServiceClient
	storage       delidevv1connect.WorkspaceStorageServiceClient
	nativeModels  delidevv1connect.NativeModelServiceClient
	transport     *http.Transport
	system        delidevv1connect.SystemServiceClient
	resources     delidevv1connect.ResourceServiceClient
	search        delidevv1connect.SearchServiceClient
	activity      delidevv1connect.ActivityServiceClient
	usage         delidevv1connect.UsageServiceClient
	configuration delidevv1connect.ConfigurationServiceClient
	devices       delidevv1connect.DeviceServiceClient
	workers       delidevv1connect.WorkerServiceClient
	browsers      delidevv1connect.BrowserServiceClient
	accounts      delidevv1connect.AccountServiceClient
	subscriptions delidevv1connect.SubscriptionServiceClient
	prFixes       delidevv1connect.PullRequestFixServiceClient
	integrations  delidevv1connect.IntegrationServiceClient
	network       delidevv1connect.NetworkServiceClient
	providers     delidevv1connect.ProviderServiceClient
	forwards      delidevv1connect.ForwardServiceClient
	terminals     delidevv1connect.TerminalServiceClient
	sessions      delidevv1connect.SessionServiceClient
	interactions  delidevv1connect.InteractionServiceClient
	inbox         delidevv1connect.InboxServiceClient
	schedules     delidevv1connect.ScheduleServiceClient
	endpoint      string
	token         string
}

func localEndpoint(o options) (server.Endpoint, error) {
	if o.desktop != nil && o.dataDir == o.desktop.Root {
		if err := o.desktop.Validate(); err != nil {
			return server.Endpoint{}, err
		}
		return server.Endpoint{URL: o.desktop.Endpoint, ServerID: o.desktop.ServerID, Version: rpc.Version, ProtocolVersion: rpc.ProtocolVersion}, nil
	}
	return server.LoadEndpoint(o.dataDir)
}

func localEndpointMatches(o options, serverID domain.ID, stored, live string) bool {
	return stored == live || (o.desktop != nil && o.desktop.ServerID == serverID && o.dataDir == o.desktop.Root)
}

func request[T any](c client, message *T) *connect.Request[T] {
	r := connect.NewRequest(message)
	r.Header().Set("Authorization", "Bearer "+c.token)
	return r
}

func connectClient(o options, input io.Reader) (client, error) {
	endpoint := o.server
	token := ""
	if saved, err := worker.LoadCredential(o.dataDir); err == nil {
		if saved.Type != domain.ClientDevice {
			return client{}, domain.Fail(domain.PermissionDenied, "A Worker scope cannot authenticate product commands.", "Use the owner or a paired client scope.")
		}
		if endpoint == "" {
			endpoint = saved.Endpoint
		}
		if endpoint == saved.Endpoint {
			token = saved.Token
		}
		if o.desktop != nil && saved.ServerID == o.desktop.ServerID && (o.dataDir == filepath.Join(o.desktop.Root, "desktop-client")) {
			endpoint, token = o.desktop.Endpoint, saved.Token
		}
	} else if !os.IsNotExist(err) {
		return client{}, err
	}
	if endpoint == "" {
		saved, err := localEndpoint(o)
		if err != nil {
			return client{}, err
		}
		if saved.ProtocolVersion != rpc.ProtocolVersion {
			return client{}, domain.Fail(domain.Unsupported, "The running server protocol is incompatible.", "Use a compatible CLI; explicitly stop or upgrade the server only after reviewing its sessions.")
		}
		identity, err := security.LoadIdentity(o.dataDir)
		if err != nil {
			return client{}, domain.Fail(domain.Unauthenticated, "The private owner credential is unavailable.", "Restore the selected server's owner credential.")
		}
		if identity.ServerID != saved.ServerID {
			return client{}, domain.Fail(domain.RecoveryRequired, "The endpoint and owner identity disagree.", "Inspect the selected data scope without overwriting it.")
		}
		endpoint = saved.URL
		token = identity.Token
	}
	if token == "" && !o.tokenStdin && o.desktop != nil && o.dataDir == o.desktop.Root {
		identity, err := security.LoadIdentity(o.dataDir)
		if err != nil || identity.ServerID != o.desktop.ServerID {
			return client{}, domain.Fail(domain.Unauthenticated, "The desktop owner is unavailable.", "Preserve the original data scope.")
		}
		token = identity.Token
	}
	if err := rpc.ValidateEndpoint(endpoint); err != nil {
		return client{}, err
	}
	if o.tokenStdin {
		if terminalInput(input) {
			return client{}, domain.Fail(domain.MissingInput, "A credential must be provided through stdin.", "Pipe the credential from protected storage; do not include it in argv.")
		}
		raw, err := io.ReadAll(io.LimitReader(input, 4097))
		if err != nil || len(raw) > 4096 {
			return client{}, domain.Fail(domain.InvalidArgument, "Invalid authentication input.", "Send only the credential through stdin.")
		}
		token = strings.TrimSpace(string(raw))
	}
	if token == "" {
		return client{}, domain.Fail(domain.MissingInput, "The selected server requires a credential.", "Provide it through --token-stdin or pair this device; never put secrets in argv.")
	}
	httpClient, transport := rpc.HTTPClient()
	if o.desktop != nil && o.desktop.ServerID != "" {
		// Owner/local-client operations retain their original identity but use
		// only this verified host transport. Saved scopes remain independent.
		if o.dataDir == o.desktop.Root || o.dataDir == filepath.Join(o.desktop.Root, "desktop-client") {
			target := *o.desktop
			httpClient.Transport = &desktopruntime.Transport{Base: transport, Resolve: func() (desktopruntime.Target, error) { return target, nil }}
		}
	}
	opts := []connect.ClientOption{connect.WithReadMaxBytes(5 << 20), connect.WithSendMaxBytes(2 << 20)}
	return client{
		storage:      delidevv1connect.NewWorkspaceStorageServiceClient(httpClient, endpoint, opts...),
		nativeModels: delidevv1connect.NewNativeModelServiceClient(httpClient, endpoint, opts...),
		transport:    transport, endpoint: endpoint, token: token,
		browsers:      delidevv1connect.NewBrowserServiceClient(httpClient, endpoint, opts...),
		inbox:         delidevv1connect.NewInboxServiceClient(httpClient, endpoint, opts...),
		schedules:     delidevv1connect.NewScheduleServiceClient(httpClient, endpoint, opts...),
		interactions:  delidevv1connect.NewInteractionServiceClient(httpClient, endpoint, opts...),
		sessions:      delidevv1connect.NewSessionServiceClient(httpClient, endpoint, opts...),
		forwards:      delidevv1connect.NewForwardServiceClient(httpClient, endpoint, opts...),
		terminals:     delidevv1connect.NewTerminalServiceClient(httpClient, endpoint, opts...),
		accounts:      delidevv1connect.NewAccountServiceClient(httpClient, endpoint, opts...),
		subscriptions: delidevv1connect.NewSubscriptionServiceClient(httpClient, endpoint, opts...),
		prFixes:       delidevv1connect.NewPullRequestFixServiceClient(httpClient, endpoint, opts...),
		integrations:  delidevv1connect.NewIntegrationServiceClient(httpClient, endpoint, opts...),
		network:       delidevv1connect.NewNetworkServiceClient(httpClient, endpoint, opts...),
		providers:     delidevv1connect.NewProviderServiceClient(httpClient, endpoint, opts...),
		devices:       delidevv1connect.NewDeviceServiceClient(httpClient, endpoint, opts...),
		workers:       delidevv1connect.NewWorkerServiceClient(httpClient, endpoint, opts...),
		installation:  delidevv1connect.NewInstallationServiceClient(httpClient, endpoint, opts...),
		system:        delidevv1connect.NewSystemServiceClient(httpClient, endpoint, opts...),
		resources:     delidevv1connect.NewResourceServiceClient(httpClient, endpoint, opts...),
		search:        delidevv1connect.NewSearchServiceClient(httpClient, endpoint, opts...),
		activity:      delidevv1connect.NewActivityServiceClient(httpClient, endpoint, opts...),
		usage:         delidevv1connect.NewUsageServiceClient(httpClient, endpoint, opts...),
		configuration: delidevv1connect.NewConfigurationServiceClient(httpClient, endpoint, opts...),
	}, nil
}
