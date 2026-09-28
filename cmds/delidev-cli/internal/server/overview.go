package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) GetOverview(ctx context.Context, req *connect.Request[pb.GetOverviewRequest]) (*connect.Response[pb.GetOverviewResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Only an owner or paired client can read the overview.", "Use an authorized product client."), correlation)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	s.connectionsMu.Lock()
	if len(s.workerStreams) > 10000 {
		s.connectionsMu.Unlock()
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The connected Worker inventory exceeds its read bound.", "Inspect individual Workers; no partial overview was returned."), correlation)
	}
	active := make(map[domain.ID]bool, len(s.workerStreams))
	for id := range s.workerStreams {
		active[id] = true
	}
	s.connectionsMu.Unlock()
	now := time.Now().UTC()
	var counts store.OverviewCounts
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		counts, err = tx.Overview(active, now)
		return err
	})
	if err != nil {
		s.logger.WarnContext(ctx, "overview_read_failed", "correlation_id", correlation, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, correlation)
	}
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	response := connect.NewResponse(&pb.GetOverviewResponse{ObservedAt: now.Format(time.RFC3339Nano), ActiveSessions: counts.ActiveSessions, PendingInteractions: counts.PendingInteractions, RegisteredWorkers: counts.RegisteredWorkers, ConnectedWorkers: counts.ConnectedWorkers, TodayFromUnixMs: from.UnixMilli(), TodayUntilUnixMs: now.UnixMilli() + 1})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
