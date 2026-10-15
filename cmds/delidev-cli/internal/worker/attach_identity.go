// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

// attachRequestIdentity owns the idempotency key of the negotiated Worker
// attach request. The server binds a request ID to the exact request content, so
// the key must change whenever any advertised field changes and must stay the
// same when an identical request is retried after a reconnect.
type attachRequestIdentity struct {
	id          domain.ID
	fingerprint string
}

// assign stamps request.RequestId. The fingerprint is the deterministic wire
// encoding of the whole request (without its own RequestId), so the exact
// ordered capability list and every network field participate. The optional
// profile carries Worker-local negotiation state that does not appear on the
// wire but must still rotate the identity when it changes.
func (a *attachRequestIdentity) assign(profile string, request *pb.AttachWorkerRequest) {
	unkeyed := proto.Clone(request).(*pb.AttachWorkerRequest)
	unkeyed.RequestId = ""
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(unkeyed)
	fingerprint := profile + "\x00" + string(wire)
	// An unencodable request cannot be proven identical to an earlier one, so it
	// never reuses a key.
	if err != nil || a.id == "" || a.fingerprint != fingerprint {
		a.id, a.fingerprint = domain.NewID(), fingerprint
	}
	request.RequestId = string(a.id)
}
