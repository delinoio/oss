// Package service implements the persisted Deck Connect services.
package service

import (
	"log/slog"

	"github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1/deckv1connect"
	"github.com/delinoio/oss/servers/devhud-deck/internal/audit"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
)

type IDGenerator interface {
	New() (uuid.UUID, error)
}

type defaultIDGenerator struct{}

func (defaultIDGenerator) New() (uuid.UUID, error) { return uuidv7.New() }

type Dependencies struct {
	Store          *database.Store
	Clock          contracts.Clock
	IDs            IDGenerator
	Hasher         *security.Hasher
	Repositories   contracts.RepositoryAuthorizer
	Audits         audit.Recorder
	Pseudonymizer  *safelog.Pseudonymizer
	Logger         *slog.Logger
	GitHubBroker   *deckgithub.Broker
	GitHubClient   *deckgithub.Client
	LiveUsage      contracts.LiveRefreshUsage
	RefreshMetrics contracts.RefreshMetrics
}

func (dependencies Dependencies) withDefaults() Dependencies {
	if dependencies.Clock == nil {
		dependencies.Clock = contracts.SystemClock{}
	}
	if dependencies.IDs == nil {
		dependencies.IDs = defaultIDGenerator{}
	}
	if dependencies.Repositories == nil {
		dependencies.Repositories = contracts.DenyAllRepositories{}
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.New(slog.DiscardHandler)
	}
	if dependencies.Audits == nil {
		dependencies.Audits = dependencies.Store
	}
	if dependencies.RefreshMetrics == nil {
		dependencies.RefreshMetrics = contracts.NoopRefreshMetrics{}
	}
	return dependencies
}

type View struct {
	deckv1connect.UnimplementedDeckViewServiceHandler
	dependencies Dependencies
}

func NewView(dependencies Dependencies) *View {
	return &View{dependencies: dependencies.withDefaults()}
}

type Device struct {
	deckv1connect.UnimplementedDeckDeviceServiceHandler
	dependencies Dependencies
}

func NewDevice(dependencies Dependencies) *Device {
	return &Device{dependencies: dependencies.withDefaults()}
}

type Integration struct {
	deckv1connect.UnimplementedDeckIntegrationServiceHandler
	dependencies Dependencies
}

func NewIntegration(dependencies Dependencies) *Integration {
	return &Integration{dependencies: dependencies.withDefaults()}
}

var (
	_ deckv1connect.DeckViewServiceHandler        = (*View)(nil)
	_ deckv1connect.DeckDeviceServiceHandler      = (*Device)(nil)
	_ deckv1connect.DeckIntegrationServiceHandler = (*Integration)(nil)
)
