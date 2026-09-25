package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type waitingResources struct {
	delidevv1connect.ResourceServiceClient
	get func(context.Context) (*connect.Response[pb.GetResourceResponse], error)
}

func (r waitingResources) GetResource(ctx context.Context, _ *connect.Request[pb.GetResourceRequest]) (*connect.Response[pb.GetResourceResponse], error) {
	return r.get(ctx)
}

func TestAwaitJobCancellationAndDeadlineRetainAcceptedJob(t *testing.T) {
	for _, blockRPC := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			raw, _ := json.Marshal(domain.Job{State: domain.JobQueued})
			accepted := &pb.Resource{Id: string(domain.NewID()), Revision: 1, DocumentJson: raw}
			current := &pb.Resource{Id: accepted.Id, Revision: 2, DocumentJson: raw}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			calls := 0
			resources := waitingResources{get: func(ctx context.Context) (*connect.Response[pb.GetResourceResponse], error) {
				calls++
				if canceled {
					cancel()
				}
				if blockRPC {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return connect.NewResponse(&pb.GetResourceResponse{Resource: current}), nil
			}}
			got, err := awaitJob(ctx, client{resources: resources}, accepted)
			cancel()
			wantCode := domain.Unavailable
			if canceled {
				wantCode = domain.Canceled
			}
			want := current
			if blockRPC {
				want = accepted
			}
			if got != want || err == nil || domain.SafeError(err).Code != wantCode || domain.SafeError(err).ExitCode() == 0 || calls != 1 {
				t.Fatalf("block=%t canceled=%t: job=%v err=%v calls=%d", blockRPC, canceled, got, err, calls)
			}
		}
	}
}

func TestAwaitJobCompletedAndUncertainResults(t *testing.T) {
	for _, state := range []domain.JobState{domain.JobSucceeded, domain.JobUncertain} {
		raw, _ := json.Marshal(domain.Job{State: state})
		job := &pb.Resource{Id: string(domain.NewID()), DocumentJson: raw}
		resources := waitingResources{get: func(context.Context) (*connect.Response[pb.GetResourceResponse], error) {
			return connect.NewResponse(&pb.GetResourceResponse{Resource: job}), nil
		}}
		got, err := awaitJob(context.Background(), client{resources: resources}, job)
		if got != job {
			t.Fatal("lost job")
		}
		if state == domain.JobSucceeded && err != nil {
			t.Fatal(err)
		}
		if state == domain.JobUncertain && (err == nil || domain.SafeError(err).Code != domain.RecoveryRequired) {
			t.Fatalf("uncertainty succeeded: %v", err)
		}
	}
}
