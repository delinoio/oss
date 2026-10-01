// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/userservice"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const DefaultListen = "127.0.0.1:46310"

type Config struct {
	userServiceBackend        userservice.Backend
	StartupID                 domain.ID
	DataDir                   string
	Listen                    string
	TLSCertificate            string
	TLSKey                    string
	AllowedOrigins            []string
	Logger                    *slog.Logger
	accountSecrets            accountSecrets
	disableCatalogMaintenance bool
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
	userServiceOptions userservice.ServerOptions
	userServiceBackend userservice.Backend
	delidevv1connect.UnimplementedSystemServiceHandler
	delidevv1connect.UnimplementedResourceServiceHandler
	delidevv1connect.UnimplementedConfigurationServiceHandler
	delidevv1connect.UnimplementedDeviceServiceHandler
	delidevv1connect.UnimplementedForwardServiceHandler
	delidevv1connect.UnimplementedWorkerServiceHandler
	delidevv1connect.UnimplementedBrowserServiceHandler
	delidevv1connect.UnimplementedAccountServiceHandler
	delidevv1connect.UnimplementedProviderServiceHandler
	delidevv1connect.UnimplementedNetworkServiceHandler
	delidevv1connect.UnimplementedIntegrationServiceHandler
	integrationOnce    sync.Once
	integrationGate    chan struct{}
	integrationChecks  map[domain.ID]*integrationCheck
	integrationSecrets integrationSecrets
	ownedPAT           *credentials.PATStore
	github             githubIdentity
	githubAccess       githubRepositoryAccess
	githubQueries      githubRepositoryQueries
	accountOnce        sync.Once
	accountGate        chan struct{}
	accountChecks      map[domain.ID]map[domain.ID]accountCheck
	accountSecrets     accountSecrets
	ownedVault         *credentials.Vault
	Store              *store.Store
	Identity           security.Identity
	Endpoint           Endpoint
	logger             *slog.Logger
	stop               context.CancelFunc
	stopping           atomic.Bool
	connectionsMu      sync.Mutex
	connections        map[domain.ID]map[domain.ID]context.CancelFunc
	pairAttempts       map[string]attemptWindow
	workerStreams      map[domain.ID]workerStream
	auxiliaryStreams   map[domain.ID]workerStream
	forwardsOnce       sync.Once
	forwardEpoch       domain.ID
	forwardsMu         sync.Mutex
	forwardRelays      map[domain.ID]*forwardRelay
	forwardLanes       map[domain.ID]*forwardLane
	workspaceReadsMu   sync.Mutex
	workspaceReaders   map[domain.ID]*workspaceReader
	executionOnce      sync.Once
	executionAuthority *executionAuthority
}
