// Package service provides generated delibase.v1 service implementations.
//
// Services embed Connect's generated unimplemented handlers so RPCs outside
// the implemented transactional slices fail explicitly; no placeholder can
// report a false success.
package service

import (
	"log/slog"

	"github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1/delibasev1connect"
	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/database"
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
	Store           *database.Store
	Clock           contracts.Clock
	Polar           contracts.PolarClient
	IdentityManager contracts.IdentityManager
	IDs             IDGenerator
	Pseudonymizer   *safelog.Pseudonymizer
	Logger          *slog.Logger
}

func (dependencies Dependencies) withDefaults() Dependencies {
	if dependencies.Clock == nil {
		dependencies.Clock = contracts.SystemClock{}
	}
	if dependencies.IDs == nil {
		dependencies.IDs = defaultIDGenerator{}
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.New(slog.DiscardHandler)
	}
	return dependencies
}

type Account struct {
	delibasev1connect.UnimplementedAccountServiceHandler
	dependencies Dependencies
}

func NewAccount(dependencies Dependencies) *Account {
	return &Account{dependencies: dependencies.withDefaults()}
}

type Organization struct {
	delibasev1connect.UnimplementedOrganizationServiceHandler
	dependencies Dependencies
}

func NewOrganization(dependencies Dependencies) *Organization {
	return &Organization{dependencies: dependencies.withDefaults()}
}

type Team struct {
	delibasev1connect.UnimplementedTeamServiceHandler
	dependencies Dependencies
}

func NewTeam(dependencies Dependencies) *Team {
	return &Team{dependencies: dependencies.withDefaults()}
}

type Catalog struct {
	delibasev1connect.UnimplementedCatalogServiceHandler
	dependencies Dependencies
}

func NewCatalog(dependencies Dependencies) *Catalog {
	return &Catalog{dependencies: dependencies.withDefaults()}
}

type Billing struct {
	delibasev1connect.UnimplementedBillingServiceHandler
	dependencies Dependencies
}

func NewBilling(dependencies Dependencies) *Billing {
	return &Billing{dependencies: dependencies.withDefaults()}
}

type Usage struct {
	delibasev1connect.UnimplementedUsageServiceHandler
	dependencies Dependencies
}

func NewUsage(dependencies Dependencies) *Usage {
	return &Usage{dependencies: dependencies.withDefaults()}
}

var (
	_ delibasev1connect.AccountServiceHandler      = (*Account)(nil)
	_ delibasev1connect.OrganizationServiceHandler = (*Organization)(nil)
	_ delibasev1connect.TeamServiceHandler         = (*Team)(nil)
	_ delibasev1connect.CatalogServiceHandler      = (*Catalog)(nil)
	_ delibasev1connect.BillingServiceHandler      = (*Billing)(nil)
	_ delibasev1connect.UsageServiceHandler        = (*Usage)(nil)
)
