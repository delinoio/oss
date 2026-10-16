// SPDX-License-Identifier: Apache-2.0
package server

import (
	"container/list"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/knownmodels"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tailscale"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tokenprices"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const DefaultListen = "127.0.0.1:46310"

type Config struct {
	TailnetStartWorker func(context.Context, string) error
	// Desktop hosts supply their already-bound listener and keep discovery
	// separate from ordinary CLI/service endpoints.
	Desktop *desktopruntime.Target
	// In-process access only; never mounted as an HTTP or Connect endpoint.
	DesktopCredentials *DesktopCredentialAccess
	Listener           net.Listener
	releaseVerifier    func([]byte, string, time.Time) (updates.Verified, error)
	releaseFactory     func() (releaseClient, error)
	userServiceBackend userservice.Backend
	StartupID          domain.ID
	DataDir            string
	Listen             string
	TLSCertificate     string
	TLSKey             string
	AllowedOrigins     []string
	Logger             *slog.Logger
	// DisableBackgroundMaintenanceForTesting prevents isolated external test
	// fixtures from contacting the official catalog endpoints. Production
	// startup leaves this false so maintenance remains enabled.
	DisableBackgroundMaintenanceForTesting bool

	accountSecrets               accountSecrets
	disableCatalogMaintenance    bool
	disableKnownModelMaintenance bool
}

type Endpoint struct {
	URL             string    `json:"url"`
	ServerID        domain.ID `json:"server_id"`
	Version         string    `json:"version"`
	ProtocolVersion int       `json:"protocol_version"`
	StartedAt       time.Time `json:"started_at"`
}

type writeControllerKey struct{}

type Service struct {
	tailscale          *tailscale.Manager
	tailnetMu          sync.Mutex
	tailnetStartWorker func(context.Context, string) error
	// Optional seams for tests; nil selects the production clock, outbound HTTP
	// client and Worker pairing retry.
	tailnetNow  func() time.Time
	tailnetHTTP func() (*http.Client, *http.Transport)
	tailnetPair func(context.Context, string, worker.PairingCode, domain.DeviceType, string) (worker.Credential, error)
	// tailnetPeerLookup and tailnetProbe replace native peer discovery and the
	// outbound identity probe in tests. withCause is true only for the explicit
	// CheckTailscale contract.
	tailnetPeerLookup func(context.Context, string) (tailscale.Peer, error)
	tailnetProbe      func(ctx context.Context, origin string, out any, withCause bool) error
	delidevv1connect.UnimplementedTailscaleServiceHandler
	delidevv1connect.UnimplementedAttachmentServiceHandler
	imageTransfersMu      sync.Mutex
	imageTransferReaders  map[domain.ID]*imageTransferReader
	skillPreparationsOnce sync.Once
	skillPreparationsWake chan struct{}
	tokenPricesOnce       sync.Once
	prices                *tokenprices.Manager
	knownModelsOnce       sync.Once
	knownModels           *knownmodels.Manager
	releaseVerifier       func([]byte, string, time.Time) (updates.Verified, error)
	releaseFactory        func() (releaseClient, error)
	delidevv1connect.UnimplementedInstallationServiceHandler
	delidevv1connect.UnimplementedWorkspaceStorageServiceHandler
	userServiceOptions userservice.ServerOptions
	userServiceBackend userservice.Backend
	delidevv1connect.UnimplementedSystemServiceHandler
	delidevv1connect.UnimplementedResourceServiceHandler
	delidevv1connect.UnimplementedConfigurationServiceHandler
	delidevv1connect.UnimplementedDeviceServiceHandler
	delidevv1connect.UnimplementedForwardServiceHandler
	delidevv1connect.UnimplementedWorkerServiceHandler
	delidevv1connect.UnimplementedTerminalServiceHandler
	delidevv1connect.UnimplementedBrowserServiceHandler
	delidevv1connect.UnimplementedAccountServiceHandler
	delidevv1connect.UnimplementedSubscriptionServiceHandler
	delidevv1connect.UnimplementedProviderServiceHandler
	delidevv1connect.UnimplementedNetworkServiceHandler
	delidevv1connect.UnimplementedIntegrationServiceHandler
	integrationQueued     map[domain.ID]int
	integrationForeground map[domain.ID]int
	prAvatarMu            sync.Mutex
	prAvatars             map[string]*prAvatarEntry
	prAvatarSequence      uint64
	integrationOnce       sync.Once
	integrationGate       chan struct{}
	integrationChecks     map[domain.ID]*integrationCheck
	integrationPreviews   map[domain.ID]*integrationCheck
	integrationSecrets    integrationSecrets
	ownedPAT              *credentials.PATStore
	github                githubIdentity
	githubQueries         githubRepositoryQueries
	githubWorkspaces      githubPullRequestWorkspaces
	githubAvatars         githubAvatarFetcher
	githubRepositories    githubRepositoryInventory
	terminalOutputMu      sync.Mutex
	terminalOutputChanged chan struct{}
	// Set only by isolated tests: runs after the account deletion replay check
	// misses and before failed-subscription cleanup is admitted or observed.
	afterAccountDeleteReplay func(context.Context)
	// Test hooks bounding the failed-login deletion completion race: one after a
	// deletion releases the account gate and before its cleanup run, one before
	// the per-account runner acquires the gate.
	afterFailedSubscriptionDeleteObserve func(context.Context)
	beforeFailedCleanupAccountGate       func(context.Context, domain.ID)
	// Set only by isolated subscription tests: pauses after the server operation
	// watcher starts and before credential access begins.
	beforeServerSubscriptionCredentials func()
	// Set before stream admission in isolated tests; production uses 100 ms.
	terminalWatchInterval time.Duration
	terminalOutputs       map[domain.ID]*terminalOutputRing
	terminalOutputOrder   list.List
	prFixRequests         prFixRequestTracker
	subscriptionOpen      serverSubscriptionOpener
	claudeSetupOpen       managedClaudeOpener
	// serverCleanupRetryBase is the first delay before a failed server-owned
	// credential cleanup is retried. Zero selects the default; tests shorten it.
	serverCleanupRetryBase        time.Duration
	claudeFinishAck               func() error
	quotaOpen                     serverQuotaOpener
	creditOpen                    serverCreditOpener
	subscriptionCallbackTransport http.RoundTripper
	subscriptionOnce              sync.Once
	subscriptionEpoch             domain.ID
	subscriptionProgress          map[domain.ID]subscriptionProgress
	// grokLoginTransport replaces the outbound transport of the server-owned
	// Grok login in isolated tests; production routes through outboundResolver.
	grokLoginTransport http.RoundTripper
	// grokDeviceClock replaces the wall clock of the Grok device poll loop in
	// isolated tests; nil selects the system clock.
	grokDeviceClock subscription.GrokDeviceClock
	// grokListen binds the loopback callback listener; tests may observe it.
	grokListen func() (net.Listener, error)
	// grokLoginBeforeSettle runs after the browser flow decided its outcome and
	// before the settlement transaction; tests use it to model a deadline or
	// shutdown that lands in that window.
	grokLoginBeforeSettle  func()
	grokLoginsMu           sync.Mutex
	grokLogins             map[domain.ID]context.CancelFunc
	claudeLoginCodes       map[domain.ID][]byte
	accountOnce            sync.Once
	accountGate            chan struct{}
	oauthGeneration        domain.ID
	oauthLive              map[domain.ID]*oauthLive
	oauthRegistrations     map[domain.ProviderPresetID]providers.OAuthRegistration
	oauthDeviceJobs        sync.WaitGroup
	oauthClosing           bool
	oauthDeviceClient      oauthDeviceClient
	oauthTokenClient       oauthTokenClient
	oauthRefreshMu         sync.Mutex
	oauthRefreshes         map[domain.ID]chan struct{}
	oauthExchange          oauthExchange
	credentialRuntimeCheck func(context.Context) error
	accountChecks          map[domain.ID]map[domain.ID]accountCheck
	accountSecrets         accountSecrets
	ownedVault             *credentials.Vault
	Store                  *store.Store
	Identity               security.Identity
	Endpoint               Endpoint
	logger                 *slog.Logger
	stop                   context.CancelFunc
	stopping               atomic.Bool
	backupRestoresMu       sync.Mutex
	backupRestoresContext  context.Context
	backupRestoresCancel   context.CancelFunc
	backupRestoresClosing  bool
	backupRestoresJobs     sync.WaitGroup
	connectionsMu          sync.Mutex
	connections            map[domain.ID]map[domain.ID]context.CancelFunc
	pairAttempts           map[string]attemptWindow
	workerStreams          map[domain.ID]workerStream
	auxiliaryStreams       map[domain.ID]workerStream
	forwardsOnce           sync.Once
	forwardEpoch           domain.ID
	forwardsMu             sync.Mutex
	forwardRelays          map[domain.ID]*forwardRelay
	forwardLanes           map[domain.ID]*forwardLane
	workspaceReadsMu       sync.Mutex
	workspaceReaders       map[domain.ID]*workspaceReader
	executionOnce          sync.Once
	executionAuthority     *executionAuthority
}
