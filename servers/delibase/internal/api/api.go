// Package api assembles delibase's health and generated Connect handlers.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1/delibasev1connect"
	"github.com/delinoio/oss/servers/delibase/internal/service"
	"github.com/delinoio/oss/servers/internal/authmiddleware"
	"github.com/delinoio/oss/servers/internal/httpserver"
	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safeerr"
	"github.com/delinoio/oss/servers/internal/safelog"
)

const readinessTimeout = 2 * time.Second

type HealthChecker interface {
	Ping(context.Context) error
}

type Dependencies struct {
	Authentication authmiddleware.Validator
	Health         HealthChecker
	Services       service.Dependencies
	CORSOrigins    []string
	Logger         *slog.Logger
}

// New registers all six generated services plus liveness/readiness endpoints.
func New(dependencies Dependencies) (http.Handler, error) {
	if dependencies.Authentication == nil {
		return nil, errors.New("api: authentication validator is required")
	}
	if dependencies.Health == nil {
		return nil, errors.New("api: health checker is required")
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.New(slog.DiscardHandler)
	}

	authentication, err := authmiddleware.NewConnect(dependencies.Authentication, connectPolicy)
	if err != nil {
		return nil, err
	}
	options := []connect.HandlerOption{
		connect.WithInterceptors(
			requestmeta.Interceptor{},
			safeerr.Interceptor{},
			authentication,
		),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", live)
	mux.Handle("GET /readyz", ready(dependencies.Health))

	register := func(path string, handler http.Handler) { mux.Handle(path, handler) }
	path, handler := delibasev1connect.NewAccountServiceHandler(
		service.NewAccount(dependencies.Services), options...,
	)
	register(path, handler)
	path, handler = delibasev1connect.NewOrganizationServiceHandler(
		service.NewOrganization(dependencies.Services), options...,
	)
	register(path, handler)
	path, handler = delibasev1connect.NewTeamServiceHandler(
		service.NewTeam(dependencies.Services), options...,
	)
	register(path, handler)
	path, handler = delibasev1connect.NewCatalogServiceHandler(
		service.NewCatalog(dependencies.Services), options...,
	)
	register(path, handler)
	path, handler = delibasev1connect.NewBillingServiceHandler(
		service.NewBilling(dependencies.Services), options...,
	)
	register(path, handler)
	path, handler = delibasev1connect.NewUsageServiceHandler(
		service.NewUsage(dependencies.Services), options...,
	)
	register(path, handler)

	corsConfig := httpserver.DefaultCORSConfig()
	if len(dependencies.CORSOrigins) > 0 {
		corsConfig.AllowedOrigins = append([]string(nil), dependencies.CORSOrigins...)
	}
	cors, err := httpserver.CORS(corsConfig)
	if err != nil {
		return nil, err
	}
	handler = cors(mux)
	handler = requestLogger(dependencies.Logger)(handler)
	handler = requestmeta.Middleware(nil)(handler)
	return handler, nil
}

func live(writer http.ResponseWriter, _ *http.Request) {
	writeHealth(writer, http.StatusOK, "ok")
}

func ready(checker HealthChecker) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), readinessTimeout)
		defer cancel()
		if err := checker.Ping(ctx); err != nil {
			writeHealth(writer, http.StatusServiceUnavailable, "not_ready")
			return
		}
		writeHealth(writer, http.StatusOK, "ready")
	})
}

func writeHealth(writer http.ResponseWriter, status int, state string) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"status": state})
}

func connectPolicy(procedure string) authmiddleware.Requirement {
	switch procedure {
	case
		delibasev1connect.CatalogServiceListCatalogAppsProcedure,
		delibasev1connect.CatalogServiceGetCatalogAppProcedure,
		delibasev1connect.CatalogServiceListCatalogMetersProcedure,
		delibasev1connect.CatalogServiceGetCatalogMeterProcedure:
		return authmiddleware.Requirement{Mode: authmiddleware.ModePublic}
	case delibasev1connect.AccountServiceGetAccountStateProcedure,
		delibasev1connect.AccountServiceGetAccountDeletionImpactProcedure:
		return userRequirement("delibase:account:read")
	case delibasev1connect.AccountServiceCompleteOnboardingProcedure,
		delibasev1connect.AccountServiceDeleteAccountProcedure:
		return userRequirement("delibase:account:write")
	case delibasev1connect.OrganizationServiceListOrganizationsProcedure,
		delibasev1connect.OrganizationServiceGetOrganizationProcedure,
		delibasev1connect.OrganizationServiceResolveOrganizationSlugProcedure,
		delibasev1connect.OrganizationServiceListOrganizationMembersProcedure,
		delibasev1connect.OrganizationServiceGetOrganizationInvitationProcedure,
		delibasev1connect.OrganizationServiceListOrganizationInvitationsProcedure:
		return userRequirement("delibase:organizations:read")
	case delibasev1connect.OrganizationServiceCreateOrganizationProcedure,
		delibasev1connect.OrganizationServiceUpdateOrganizationProcedure,
		delibasev1connect.OrganizationServiceUpdateOrganizationSlugProcedure,
		delibasev1connect.OrganizationServiceDeleteOrganizationProcedure,
		delibasev1connect.OrganizationServiceUpdateOrganizationMemberRoleProcedure,
		delibasev1connect.OrganizationServiceRemoveOrganizationMemberProcedure,
		delibasev1connect.OrganizationServiceLeaveOrganizationProcedure,
		delibasev1connect.OrganizationServiceCreateOrganizationInvitationProcedure,
		delibasev1connect.OrganizationServiceAcceptOrganizationInvitationProcedure,
		delibasev1connect.OrganizationServiceRevokeOrganizationInvitationProcedure:
		return userRequirement("delibase:organizations:write")
	case delibasev1connect.TeamServiceListTeamsProcedure,
		delibasev1connect.TeamServiceGetTeamProcedure,
		delibasev1connect.TeamServiceListTeamMembershipsProcedure,
		delibasev1connect.TeamServiceListEffectiveTeamAccessProcedure:
		return userRequirement("delibase:teams:read")
	case delibasev1connect.TeamServiceCreateTeamProcedure,
		delibasev1connect.TeamServiceUpdateTeamProcedure,
		delibasev1connect.TeamServiceMoveTeamProcedure,
		delibasev1connect.TeamServiceDeleteTeamSubtreeProcedure,
		delibasev1connect.TeamServiceSetTeamMembershipProcedure,
		delibasev1connect.TeamServiceRemoveTeamMembershipProcedure:
		return userRequirement("delibase:teams:write")
	case delibasev1connect.BillingServiceGetBillingSummaryProcedure,
		delibasev1connect.BillingServiceListLedgerEntriesProcedure,
		delibasev1connect.BillingServiceListUsageRecordsProcedure:
		return userRequirement("delibase:billing:read")
	case delibasev1connect.BillingServiceCreateSubscriptionCheckoutProcedure,
		delibasev1connect.BillingServiceCreateBillingPortalSessionProcedure,
		delibasev1connect.BillingServiceUpdateOverageLimitProcedure:
		return userRequirement("delibase:billing:write")
	case delibasev1connect.UsageServiceReserveUsageProcedure:
		return usageRequirement("delibase:usage:reserve")
	case delibasev1connect.UsageServiceCommitUsageProcedure:
		return usageRequirement("delibase:usage:commit")
	case delibasev1connect.UsageServiceReleaseUsageProcedure:
		return usageRequirement("delibase:usage:release")
	default:
		// The invalid zero mode is intentional: any newly generated procedure
		// must be added explicitly before it is reachable.
		return authmiddleware.Requirement{}
	}
}

func userRequirement(scope string) authmiddleware.Requirement {
	return authmiddleware.Requirement{
		Mode:       authmiddleware.ModeUser,
		UserScopes: []string{scope},
	}
}

func usageRequirement(serviceScope string) authmiddleware.Requirement {
	return authmiddleware.Requirement{
		Mode:       authmiddleware.ModeM2MWithForwardedUser,
		M2MScopes:  []string{serviceScope},
		UserScopes: []string{"delibase:usage:execute"},
	}
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.status != 0 {
		return
	}
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return recorder.ResponseWriter.Write(body)
}

func (recorder *responseRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			recorder := &responseRecorder{ResponseWriter: writer}
			next.ServeHTTP(recorder, request)
			result := safelog.ResultSuccess
			level := slog.LevelInfo
			if recorder.status >= http.StatusBadRequest {
				result = safelog.ResultFailure
				level = slog.LevelWarn
			}
			safelog.Record(request.Context(), logger, level, safelog.EventRequest, safelog.Fields{
				Method:    request.Method,
				Procedure: request.URL.Path,
				Result:    result,
			})
		})
	}
}
