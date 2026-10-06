// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// Receive an assignment through the authenticated primary lane. Closing this
// stream preserves the original claim for a same-instance reconnect.
func watchWorkAssignment(t *testing.T, client delidevv1connect.WorkerServiceClient, identity security.Identity, machine, instance domain.ID) *pb.Resource {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	stream, err := client.WatchWork(ctx, ownerRequest(identity, &pb.WatchWorkRequest{MachineId: string(machine), InstanceId: string(instance)}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal("missing primary readiness heartbeat", stream.Err())
	}
	if !stream.Receive() || stream.Msg().Job == nil {
		t.Fatal("missing primary assignment", stream.Err())
	}
	return stream.Msg().Job
}

// Preserve whitespace in the embedded document so the test reaches the actual
// byte boundary instead of json.Marshal compacting a RawMessage first.
func workerClaimJSON(t *testing.T, record store.Record, document []byte) []byte {
	t.Helper()
	record.Data = json.RawMessage(`null`)
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Replace(raw, []byte(`"data":null`), append([]byte(`"data":`), document...), 1)
}

func padClaimDocument(t *testing.T, raw []byte, size int) []byte {
	t.Helper()
	if len(raw) > size {
		t.Fatalf("fixture exceeds target bound: %d > %d", len(raw), size)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '}' {
		t.Fatal("boundary fixture must be a JSON object")
	}
	result := append(bytes.Clone(raw[:len(raw)-1]), bytes.Repeat([]byte(" "), size-len(raw))...)
	return append(result, '}')
}

func TestWorkerClaimOrdinaryBoundsAndStrictSchema(t *testing.T) {
	job := domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobClaimed, MachineID: domain.NewID(), InstanceID: domain.NewID(), AssignedDeviceID: domain.NewID(), Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()}
	document, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	record := store.Record{ID: domain.NewID(), Kind: domain.JobKind, Revision: 2, CreatedAt: job.AcceptedAt, UpdatedAt: job.AcceptedAt}
	maximum := workerClaimJSON(t, record, padClaimDocument(t, document, 1<<20))
	if _, got, err := decodeWorkerClaim(maximum); err != nil || got.Type != job.Type {
		t.Fatal("ordinary maximum was rejected", err)
	}
	if _, _, err := decodeWorkerClaim(padClaimDocument(t, maximum, 1<<20+maxWorkerClaimMetadataBytes)); err != nil {
		t.Fatal("maximum claim metadata was rejected", err)
	}
	for name, raw := range map[string][]byte{
		"ordinary-one-byte-over": workerClaimJSON(t, record, padClaimDocument(t, document, 1<<20+1)),
		"receipt-one-byte-over":  padClaimDocument(t, maximum, maxWorkerClaimBytes+1),
		"metadata-one-byte-over": padClaimDocument(t, workerClaimJSON(t, record, document), len(document)+maxWorkerClaimMetadataBytes+1),
		"unknown-record-field":   bytes.Replace(maximum, []byte(`"kind":"job"`), []byte(`"kind":"job","unknown":true`), 1),
		"duplicate-record-field": bytes.Replace(maximum, []byte(`"kind":"job"`), []byte(`"kind":"job","kind":"job"`), 1),
		"unknown-job-field":      workerClaimJSON(t, record, append([]byte(`{"unknown":true,`), document[1:]...)),
		"duplicate-job-field":    workerClaimJSON(t, record, append([]byte(fmt.Sprintf(`{"type":%q,`, job.Type)), document[1:]...)),
		"trailing-document":      append(bytes.Clone(maximum), []byte(`{}`)...),
		"invalid-utf8":           append(bytes.Clone(maximum), 0xff),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeWorkerClaim(raw); err == nil {
				t.Fatal("invalid claim was accepted")
			}
		})
	}
	record.Kind = domain.SessionKind
	if _, _, err := decodeWorkerClaim(workerClaimJSON(t, record, document)); err == nil {
		t.Fatal("non-job claim was accepted")
	}
}

func assertWorkerClaimExceptionBounds(t *testing.T, resource *pb.Resource, inputLimit, jobLimit int) {
	t.Helper()
	record := store.Record{ID: domain.ID(resource.Id), Kind: domain.JobKind, Revision: resource.Revision, SessionID: domain.ID(resource.SessionId), ProjectID: domain.ID(resource.ProjectId)}
	for _, size := range []int{jobLimit, jobLimit + 1} {
		raw := workerClaimJSON(t, record, padClaimDocument(t, resource.DocumentJson, size))
		if _, _, err := decodeWorkerClaim(raw); (err == nil) != (size == jobLimit) {
			t.Fatalf("exception job size %d: %v", size, err)
		}
		if size == jobLimit {
			if _, _, err := decodeWorkerClaim(padClaimDocument(t, raw, maxWorkerClaimBytes)); err != nil {
				t.Fatal("maximum exception receipt was rejected", err)
			}
		}
	}
	var job domain.Job
	if err := domain.DecodeWithLimit(resource.DocumentJson, &job, jobLimit); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{inputLimit, inputLimit + 1} {
		document := bytes.Replace(resource.DocumentJson, job.Input, padClaimDocument(t, job.Input, size), 1)
		if _, _, err := decodeWorkerClaim(workerClaimJSON(t, record, document)); (err == nil) != (size == inputLimit) {
			t.Fatalf("exception input size %d: %v", size, err)
		}
	}
	job.Type = domain.InspectRepositoryJob
	ordinary, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeWorkerClaim(workerClaimJSON(t, record, ordinary)); err == nil {
		t.Fatal("ordinary job acquired an exception bound")
	}
}
