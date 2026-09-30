// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestSessionForkObservationRequiresCurrentOwnerOrClient(t *testing.T) {
	for _, published := range []bool{false, true} {
		name := "queued-job"
		if published {
			name = "published-child"
		}
		t.Run(name, func(t *testing.T) {
			var f *continuationFixture
			var jobID string
			if published {
				var child *pb.Resource
				f, child, _ = publishedForkFixture(t)
				var session domain.Session
				if domain.Decode(child.DocumentJson, &session) != nil || session.Fork == nil {
					t.Fatal("missing published fork metadata")
				}
				jobID = string(session.Fork.JobID)
			} else {
				var accepted *pb.ForkSessionResponse
				f, _, accepted = acceptedForkFixture(t)
				jobID = accepted.Job.Id
			}
			before := f.refresh(t)
			request := &pb.GetSessionForkRequest{JobId: jobID}
			owner, err := sessionClient(f.accountFixture).GetSessionFork(context.Background(), ownerRequest(f.identity, request))
			if err != nil || owner.Msg.Job.Id != jobID || (owner.Msg.Session != nil) != published {
				t.Fatal("authorized owner lost fork observation", err)
			}
			// Worker credentials must fail before even checking job existence.
			for _, id := range []string{jobID, string(domain.NewID())} {
				_, err := sessionClient(f.accountFixture).GetSessionFork(context.Background(), ownerRequest(f.workerIdentity, &pb.GetSessionForkRequest{JobId: id}))
				if connect.CodeOf(err) != connect.CodePermissionDenied {
					t.Fatal("Worker retrieved a client-only fork observation", err)
				}
			}
			if _, err := f.service.GetSessionFork(context.Background(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("missing principal retrieved fork observation", err)
			}
			clientID := domain.NewID()
			doctorPut(t, f.service, domain.DeviceKind, clientID, 0, domain.Device{Name: "Fork observer", Type: domain.ClientDevice})
			client := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: clientID})
			observed, err := f.service.GetSessionFork(client, connect.NewRequest(request))
			if err != nil || observed.Msg.Job.Id != jobID || (observed.Msg.Session != nil) != published {
				t.Fatal("current paired client lost fork observation", err)
			}
			doctorPut(t, f.service, domain.DeviceKind, clientID, 1, domain.Device{Name: "Fork observer", Type: domain.ClientDevice, Revoked: true})
			if _, err := f.service.GetSessionFork(client, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("stale revoked principal retrieved fork observation", err)
			}
			after := f.refresh(t)
			if before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
				t.Fatal("fork observation changed source state")
			}
		})
	}
}
