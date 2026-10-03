// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Bind one immutable route before native startup. The manager's later pointer
// replacement is irrelevant to this runtime and cannot rewrite its environment.
func openCodexNativeProxy(ctx context.Context, config Config, execution domain.ID) (*nativeproxy.Proxy, error) {
	if config.network == nil {
		return nil, nil
	}
	snapshot := config.network.current()
	if snapshot.bundle.Route.Profile.Mode == domain.ProxyDirect {
		return nil, nil
	}
	connection := config.execution
	if connection == nil || connection.Assignment == nil || connection.Client == nil || snapshot.metadata.Generation == 0 || snapshot.metadata.RouteID != snapshot.bundle.RouteID || snapshot.metadata.Generation != snapshot.bundle.Generation || snapshot.bundle.Authority != networkAuthority(connection.Credential) {
		return nil, publicationUncertain()
	}
	report := func(ctx context.Context, state pb.WorkerNativeRouteState) (bool, error) {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		response, err := connection.Client.ReportWorkerNativeRoute(bounded, authenticated(connection.Credential, &pb.ReportWorkerNativeRouteRequest{
			Mutation:  &pb.Mutation{RequestId: string(domain.NewID()), Id: connection.Assignment.Id, ExpectedRevision: connection.Assignment.Revision},
			MachineId: string(connection.Credential.MachineID), InstanceId: string(connection.Instance), ExecutionId: string(execution),
			RouteId: string(snapshot.metadata.RouteID), Generation: snapshot.metadata.Generation, State: state,
		}))
		if err != nil {
			return false, rpc.ClientError(err)
		}
		if response == nil || response.Msg == nil {
			return false, publicationUncertain()
		}
		return response.Msg.Replayed, nil
	}
	replayed, err := report(ctx, pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_UNVERIFIED)
	if err != nil {
		return nil, err
	}
	if replayed {
		return nil, publicationUncertain()
	}
	proxy, err := nativeproxy.Open(ctx, nativeproxy.Config{Origin: connection.Credential.Endpoint, Profile: snapshot.bundle.Route.Profile, Credential: snapshot.bundle.Credential, Observed: func(ctx context.Context) {
		_, err := report(ctx, pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_OBSERVED)
		if err != nil && config.Logger != nil {
			config.Logger.WarnContext(ctx, "worker_native_route_publication_unconfirmed", "job_id", connection.Assignment.Id, "execution_id", execution, "route_id", snapshot.metadata.RouteID, "generation", snapshot.metadata.Generation, "code", domain.SafeError(err).Code)
		}
	}, Failed: func(ctx context.Context) {
		_, err := report(ctx, pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_FAILED)
		if err != nil && config.Logger != nil {
			config.Logger.WarnContext(ctx, "worker_native_route_failure_unconfirmed", "job_id", connection.Assignment.Id, "generation", snapshot.metadata.Generation, "code", domain.SafeError(err).Code)
		}
	}})
	if err != nil {
		_, _ = report(ctx, pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_FAILED)
		return nil, err
	}
	if config.Logger != nil {
		config.Logger.InfoContext(ctx, "worker_native_route_bound", "job_id", connection.Assignment.Id, "execution_id", execution, "route_id", snapshot.metadata.RouteID, "generation", snapshot.metadata.Generation, "state", domain.WorkerRouteUnverified)
	}
	return proxy, nil
}
