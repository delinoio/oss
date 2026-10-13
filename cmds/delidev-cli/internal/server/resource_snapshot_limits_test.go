// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

type snapshotJobKind string

const (
	snapshotCompactJob     snapshotJobKind = "compact-session"
	snapshotStorageRecover snapshotJobKind = "workspace-storage-recover"
	snapshotStorageOther   snapshotJobKind = "workspace-storage-inspect"
	snapshotOrdinaryJob    snapshotJobKind = "execute-session"
)

// snapshotJobDocument returns a Job document of exactly size bytes whose
// padding lives inside the typed input, like the retained recovery evidence.
func snapshotJobDocument(t *testing.T, kind snapshotJobKind, size int) []byte {
	t.Helper()
	build := func(padding int) []byte {
		input := fmt.Sprintf(`{"pad":%q}`, strings.Repeat("x", padding))
		jobType := string(kind)
		switch kind {
		case snapshotStorageRecover:
			jobType, input = string(domain.WorkspaceStorageJob), fmt.Sprintf(`{"action":"recover","pad":%q}`, strings.Repeat("x", padding))
		case snapshotStorageOther:
			jobType, input = string(domain.WorkspaceStorageJob), fmt.Sprintf(`{"action":"inspect","pad":%q}`, strings.Repeat("x", padding))
		}
		return []byte(fmt.Sprintf(`{"type":%q,"state":"queued","input":%s,"accepted_at":"2026-01-01T00:00:00Z"}`, jobType, input))
	}
	base := len(build(0))
	if size < base {
		t.Fatalf("job document size %d below minimum %d", size, base)
	}
	document := build(size - base)
	if len(document) != size {
		t.Fatalf("job document has %d bytes, want %d", len(document), size)
	}
	return document
}

type snapshotLimitFixture struct {
	t        *testing.T
	service  *Service
	raw      *sql.DB
	server   *httptest.Server
	identity security.Identity
	binary   delidevv1connect.ResourceServiceClient
	json     delidevv1connect.ResourceServiceClient
}

func newSnapshotLimitFixture(t *testing.T) *snapshotLimitFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	db, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(root, "state.sqlite")+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	identity := security.Identity{ServerID: domain.NewID(), Token: "temporary-snapshot-limit-owner"}
	service := &Service{Store: db, Identity: identity, logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	host := httptest.NewServer(service.Handler(nil, true))
	f := &snapshotLimitFixture{t: t, service: service, raw: raw, server: host, identity: identity}
	// Connect measures compressed messages, so disable gzip: the repeated padding
	// would otherwise hide the uncompressed transport ceiling under test.
	uncompressed := connect.WithAcceptCompression("gzip", nil, nil)
	httpClient := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	t.Cleanup(httpClient.CloseIdleConnections)
	f.binary = delidevv1connect.NewResourceServiceClient(httpClient, host.URL, uncompressed)
	f.json = delidevv1connect.NewResourceServiceClient(httpClient, host.URL, uncompressed, connect.WithProtoJSON())
	t.Cleanup(func() {
		host.Close()
		raw.Close()
		db.Close()
	})
	return f
}

// putJob writes a retained Job document directly so fixtures can exceed the
// admission-time bounds that owning flows enforce before ordinary storage.
func (f *snapshotLimitFixture) putJob(kind snapshotJobKind, size int) domain.ID {
	f.t.Helper()
	id := domain.NewID()
	_, err := f.raw.Exec("INSERT INTO entities(kind,id,revision,body,created_at,updated_at) VALUES('job',?,1,?,0,0)", string(id), snapshotJobDocument(f.t, kind, size))
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *snapshotLimitFixture) resize(id domain.ID, kind snapshotJobKind, size int) {
	f.t.Helper()
	if _, err := f.raw.Exec("UPDATE entities SET body=? WHERE id=?", snapshotJobDocument(f.t, kind, size), string(id)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *snapshotLimitFixture) snapshot(client delidevv1connect.ResourceServiceClient) (*pb.GetSnapshotResponse, error) {
	f.t.Helper()
	response, err := client.GetSnapshot(context.Background(), ownerRequest(f.identity, &pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_JOB, PageSize: 50}}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func snapshotWireSize(t *testing.T, message proto.Message) int {
	t.Helper()
	encoded, err := protojson.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return max(proto.Size(message), len(encoded))
}

func (f *snapshotLimitFixture) requireRejected(label string) {
	f.t.Helper()
	for name, client := range map[string]delidevv1connect.ResourceServiceClient{"protobuf": f.binary, "protojson": f.json} {
		response, err := f.snapshot(client)
		problem := rpc.ClientError(err)
		if response != nil || connect.CodeOf(err) != connect.CodeResourceExhausted || problem.Code != domain.ResourceExhausted || !strings.Contains(problem.Guidance, "narrower") || problem.CorrelationID == "" {
			f.t.Fatalf("%s over %s: expected explicit narrower-scope failure without partial snapshot: %v", label, name, err)
		}
	}
}

func TestSnapshotAcceptsTypedLargeJobDocumentsThroughConnectHandler(t *testing.T) {
	const mib = 1 << 20
	sizes := []int{2 * mib, 3*mib + mib/2, 4 * mib}
	for _, kind := range []snapshotJobKind{snapshotCompactJob, snapshotStorageRecover} {
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%s-%d", kind, size), func(t *testing.T) {
				f := newSnapshotLimitFixture(t)
				id := f.putJob(kind, size)
				want := snapshotJobDocument(t, kind, size)
				for name, client := range map[string]delidevv1connect.ResourceServiceClient{"protobuf": f.binary, "protojson": f.json} {
					response, err := f.snapshot(client)
					if err != nil {
						t.Fatalf("%s: complete typed Job snapshot failed: %v", name, err)
					}
					if len(response.Resources) != 1 || response.Resources[0].Id != string(id) || !bytes.Equal(response.Resources[0].DocumentJson, want) || response.Cursor == "" {
						t.Fatalf("%s: snapshot lost the Job or event cursor", name)
					}
					if got := snapshotWireSize(t, response); got > domain.MaxLargeJobSnapshotBytes {
						t.Fatalf("%s: response used %d bytes", name, got)
					}
				}
			})
		}
	}
}

func TestSnapshotRejectsTypedJobDocumentsAboveFourMiB(t *testing.T) {
	for _, kind := range []snapshotJobKind{snapshotCompactJob, snapshotStorageRecover} {
		t.Run(string(kind), func(t *testing.T) {
			f := newSnapshotLimitFixture(t)
			f.putJob(kind, domain.MaxLargeJobDocumentBytes+1)
			f.requireRejected("typed Job one byte over 4 MiB")
		})
	}
}

func TestSnapshotRejectsOversizeDocumentsBesideTypedJobs(t *testing.T) {
	// A larger budget must not admit documents that clients refuse to decode.
	for _, other := range []snapshotJobKind{snapshotOrdinaryJob, snapshotStorageOther} {
		t.Run(string(other), func(t *testing.T) {
			f := newSnapshotLimitFixture(t)
			f.putJob(snapshotCompactJob, 3*(1<<20))
			ordinary := f.putJob(other, domain.MaxResourceDocumentBytes)
			if _, err := f.snapshot(f.binary); err != nil {
				t.Fatalf("ordinary document at the 1 MiB bound was rejected: %v", err)
			}
			f.resize(ordinary, other, domain.MaxResourceDocumentBytes+1)
			f.requireRejected("ordinary Job document one byte over 1 MiB")
		})
	}
}

func TestSnapshotWithoutLargeJobsKeepsFourMiBBudget(t *testing.T) {
	f := newSnapshotLimitFixture(t)
	// Typed Jobs at the ordinary bound do not need the larger budget.
	for i := 0; i < 5; i++ {
		f.putJob(snapshotCompactJob, 500<<10)
	}
	response, err := f.snapshot(f.json)
	if err != nil || len(response.Resources) != 5 || snapshotWireSize(t, response) > maxResourcePageBytes {
		t.Fatalf("ordinary snapshot below 4 MiB failed: %v", err)
	}
	for i := 0; i < 5; i++ {
		f.putJob(snapshotCompactJob, 500<<10)
	}
	f.requireRejected("ordinary-only aggregate above 4 MiB")
	if got := domain.MaxLargeJobSnapshotBytes; got != 2*maxResourcePageBytes {
		t.Fatalf("typed Job snapshot budget changed: %d", got)
	}
}

func TestSnapshotTypedJobBudgetBoundaryIncludesCursorAndEnvelope(t *testing.T) {
	const limit = domain.MaxLargeJobSnapshotBytes
	f := newSnapshotLimitFixture(t)
	fixed := f.putJob(snapshotStorageRecover, domain.MaxLargeJobDocumentBytes)
	variable := f.putJob(snapshotCompactJob, domain.MaxResourceDocumentBytes+1)
	probe, err := f.snapshot(f.binary)
	if err != nil || len(probe.Resources) != 2 {
		t.Fatal("probe snapshot failed", err)
	}
	variableIndex := 0
	if probe.Resources[1].Id == string(variable) {
		variableIndex = 1
	}
	if probe.Resources[1-variableIndex].Id != string(fixed) {
		t.Fatal("unexpected snapshot membership")
	}
	// The expected wire size uses the same cursor and envelope as the server.
	expected := func(size int) int {
		message := proto.Clone(probe).(*pb.GetSnapshotResponse)
		message.Resources[variableIndex].DocumentJson = snapshotJobDocument(t, snapshotCompactJob, size)
		return snapshotWireSize(t, message)
	}
	low, high := domain.MaxResourceDocumentBytes+1, domain.MaxLargeJobDocumentBytes
	if expected(low) > limit || expected(high) <= limit {
		t.Fatalf("fixture cannot straddle the budget: %d..%d", expected(low), expected(high))
	}
	for high-low > 1 {
		middle := (low + high) / 2
		if expected(middle) <= limit {
			low = middle
		} else {
			high = middle
		}
	}
	if expected(low) > limit || expected(high) <= limit {
		t.Fatal("boundary search is inconsistent")
	}
	if limit-expected(low) >= 4 {
		t.Fatalf("largest passing fixture is %d bytes below the budget", limit-expected(low))
	}

	f.resize(variable, snapshotCompactJob, low)
	for name, client := range map[string]delidevv1connect.ResourceServiceClient{"protobuf": f.binary, "protojson": f.json} {
		response, err := f.snapshot(client)
		if err != nil || len(response.Resources) != 2 || response.Cursor == "" {
			t.Fatalf("%s: response just below the budget failed: %v", name, err)
		}
		if got := snapshotWireSize(t, response); got > limit {
			t.Fatalf("%s: accepted response has %d bytes", name, got)
		}
	}
	f.resize(variable, snapshotCompactJob, high)
	f.requireRejected("response just above the 8 MiB budget")
}

func TestSnapshotBudgetMeasuresLargerOfProtobufAndJSONEncodings(t *testing.T) {
	// Protobuf-JSON repeats field names and Base64 expands bytes, so it binds
	// document-heavy messages. The shared measurement still takes either maximum.
	message := &pb.GetSnapshotResponse{Resources: []*pb.Resource{{Id: "a", DocumentJson: bytes.Repeat([]byte{0xff}, 3<<20)}}, Cursor: "cursor"}
	binary, text, err := encodedSizes(message)
	if err != nil || text <= binary {
		t.Fatalf("fixture must be bound by protobuf-JSON: binary=%d json=%d %v", binary, text, err)
	}
	for _, limit := range []int{text - 1, text, binary} {
		fits, err := resourceMessageFits(message, limit)
		if err != nil || fits != (limit >= max(binary, text)) {
			t.Fatalf("limit %d: fits=%t err=%v", limit, fits, err)
		}
	}
	// A message whose binary encoding is larger than its JSON form must also be bound.
	wide := &pb.Resource{Id: "a", Revision: 1<<63 - 1, SchemaVersion: 1}
	binary, text, err = encodedSizes(wide)
	if err != nil {
		t.Fatal(err)
	}
	if fits, err := resourceMessageFits(wide, max(binary, text)-1); err != nil || fits {
		t.Fatal("limit below the larger encoding was accepted", err)
	}
}

func TestTypedLargeJobPredicateMatchesClientRule(t *testing.T) {
	for name, tc := range map[string]struct {
		document string
		want     bool
	}{
		"compact-session":         {`{"type":"compact-session","input":{}}`, true},
		"storage-recover":         {`{"type":"workspace-storage","input":{"action":"recover"}}`, true},
		"storage-other":           {`{"type":"workspace-storage","input":{"action":"inspect"}}`, false},
		"unrelated-job":           {`{"type":"execute-session","input":{}}`, false},
		"compact-without-input":   {`{"type":"compact-session"}`, false},
		"compact-null-input":      {`{"type":"compact-session","input":null}`, false},
		"compact-array-input":     {`{"type":"compact-session","input":[]}`, false},
		"storage-recover-no-body": {`{"type":"workspace-storage","input":null}`, false},
		"invalid-json":            {`{`, false},
	} {
		if got := typedLargeJobDocument([]byte(tc.document)); got != tc.want {
			t.Errorf("%s: got %t, want %t", name, got, tc.want)
		}
	}
}

func TestTypedJobBudgetDoesNotEnlargeOtherResourceRPCs(t *testing.T) {
	f := newSnapshotLimitFixture(t)
	id := f.putJob(snapshotCompactJob, domain.MaxLargeJobDocumentBytes)
	if _, err := f.snapshot(f.json); err != nil {
		t.Fatal("GetSnapshot did not use its larger limit", err)
	}
	// A 4 MiB Job is about 5.3 MiB in protobuf-JSON: List pages keep their 4 MiB
	// cap and the shared 5 MiB send ceiling instead of inheriting the snapshot's.
	for name, client := range map[string]delidevv1connect.ResourceServiceClient{"protobuf": f.binary, "protojson": f.json} {
		page, err := client.ListResources(context.Background(), ownerRequest(f.identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_JOB, PageSize: 50}}))
		if page != nil || connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("%s: list page for a 4 MiB Job was not rejected: %v", name, err)
		}
	}
	if _, err := f.binary.GetResource(context.Background(), ownerRequest(f.identity, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_JOB, Id: string(id)})); err != nil {
		t.Fatal("GetResource regressed", err)
	}
}
