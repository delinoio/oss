package server

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestOverviewAuthorizesWithoutAccountInspection(t *testing.T) {
	s, secrets := newDoctorFixture(t)
	ctx := context.Background()
	request := connect.NewRequest(&pb.GetOverviewRequest{})
	for _, actor := range []domain.Principal{{}, {Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: domain.NewID()}} {
		if _, err := s.GetOverview(domain.WithPrincipal(ctx, actor), request); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("non-client read overview", err)
		}
	}
	owner := domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	result, err := s.GetOverview(owner, request)
	if err != nil {
		t.Fatal(err)
	}
	v := result.Msg
	observed, err := time.Parse(time.RFC3339Nano, v.ObservedAt)
	from := time.UnixMilli(v.TodayFromUnixMs).UTC()
	if err != nil || from.Hour() != 0 || from.Minute() != 0 || from.Second() != 0 || from.Nanosecond() != 0 || from.YearDay() != observed.YearDay() || v.TodayUntilUnixMs != observed.UnixMilli()+1 || v.ActiveSessions != 0 || v.PendingInteractions != 0 || len(secrets.refs) != 0 {
		t.Fatal("invalid read-only UTC overview", v)
	}
	client := domain.NewID()
	doctorPut(t, s, domain.DeviceKind, client, 0, domain.Device{Type: domain.ClientDevice, Revoked: true})
	if _, err := s.GetOverview(domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: client}), request); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked client read overview", err)
	}
}
