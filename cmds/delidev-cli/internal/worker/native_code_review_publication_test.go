// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type nativeReviewReceiptClient struct {
	delidevv1connect.WorkerServiceClient
	publish func(context.Context, *pb.PublishNativeCodeReviewRequest) error
}

func (c nativeReviewReceiptClient) PublishNativeCodeReview(ctx context.Context, req *connect.Request[pb.PublishNativeCodeReviewRequest]) (*connect.Response[pb.PublishNativeCodeReviewResponse], error) {
	if err := c.publish(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.PublishNativeCodeReviewResponse{Revision: 2}), nil
}

func TestNativeReviewRetainsExactObservationAcrossCancellationAndLostReceipt(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "loss-before-commit", true: "loss-after-commit"}[committed], func(t *testing.T) {
			config := Config{Root: t.TempDir()}
			c := &PublicationConfig{Credential: Credential{MachineID: domain.NewID(), ServerID: domain.NewID(), DeviceID: domain.NewID()}, Instance: domain.NewID(), Assignment: &pb.Resource{Id: string(domain.NewID()), Revision: 7, DocumentJson: []byte(`{"original":"assignment"}`)}}
			root := filepath.Join(config.Root, "jobs", c.Assignment.Id)
			if err := security.PrivateDir(root); err != nil {
				t.Fatal(err)
			}
			request := &pb.PublishNativeCodeReviewRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: c.Assignment.Id, ExpectedRevision: c.Assignment.Revision}, MachineId: string(c.Credential.MachineID), InstanceId: string(c.Instance), ItemId: "original-entered-item"}
			calls, receipts := 0, 0
			retained := map[string]bool{}
			c.Client = nativeReviewReceiptClient{publish: func(ctx context.Context, observed *pb.PublishNativeCodeReviewRequest) error {
				calls++
				if ctx.Err() != nil || !proto.Equal(request, observed) {
					t.Fatal("canceled evidence or receipt replay changed its request")
				}
				if committed || calls > 3 {
					if !retained[observed.Mutation.RequestId] {
						retained[observed.Mutation.RequestId] = true
						receipts++
					}
				}
				if calls <= 3 {
					return connect.NewError(connect.CodeUnavailable, errors.New("lost original reply"))
				}
				return nil
			}}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if err := publishNativeReviewObservation(canceled, c, root, 1, request); err == nil {
				t.Fatal("lost observation receipt reported success")
			}
			// Reconnect can settle the original receipt while native work stays
			// canceled. It cannot create a new request or review operation.
			if err := replayNativeReviewObservations(canceled, config, c); err != nil {
				t.Fatal(err)
			}
			if calls != 4 || receipts != 1 {
				t.Fatal("reconnect changed or duplicated original evidence", calls, receipts)
			}
			if err := replayNativeReviewObservations(canceled, config, c); err != nil || calls != 4 {
				t.Fatal("confirmed receipt was republished", err)
			}
			foreign := *c
			foreign.Instance = domain.NewID()
			if err := replayNativeReviewObservations(context.Background(), config, &foreign); err == nil || calls != 4 {
				t.Fatal("foreign instance adopted original observation")
			}
		})
	}
}
