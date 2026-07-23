// Package service provides generated delibase.v1 service implementations.
//
// The foundation deliberately embeds Connect's generated unimplemented
// handlers. Every business RPC therefore returns a typed Unimplemented error
// until its transactional policy is implemented; no placeholder can report a
// false success.
package service

import (
	"github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1/delibasev1connect"
	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/database"
)

type Dependencies struct {
	Store           *database.Store
	Clock           contracts.Clock
	Polar           contracts.PolarClient
	IdentityManager contracts.IdentityManager
}

func (dependencies Dependencies) withDefaults() Dependencies {
	if dependencies.Clock == nil {
		dependencies.Clock = contracts.SystemClock{}
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
