package core

import (
	"bytes"
	"context"
	"os"
	"testing"
	"unicode/utf8"

	"connectrpc.com/connect"
	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"google.golang.org/protobuf/proto"
)

func TestEvidenceTextNormalizesInvalidUTF8WithoutChangingByteOffsets(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"unused\"\n")
	receipt, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Store.Run(receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte{'a', 0xff, 0xc3, 'b', 0xe2, 0x82, 0xac}
	evidence, err := s.SaveEvidence(r.ID, "output", raw)
	if err != nil {
		t.Fatal(err)
	}
	c := r.Checks[0]
	c.Log = evidence
	c.Reports = []Evidence{evidence}
	c.State = Passed
	if err := s.Store.SaveCheck(c); err != nil {
		t.Fatal(err)
	}
	a := &API{s: s}
	for _, limit := range []uint32{1, 3, 100} {
		for offset := int64(0); offset < int64(len(raw)); {
			logs, err := a.GetLogs(context.Background(), connect.NewRequest(&pb.GetLogsRequest{RunId: r.ID, CheckId: c.ID, Offset: offset, Limit: limit}))
			if err != nil {
				t.Fatal(err)
			}
			report, err := a.GetReport(context.Background(), connect.NewRequest(&pb.GetReportRequest{RunId: r.ID, ReportId: evidence.ID, Offset: offset, Limit: limit}))
			if err != nil {
				t.Fatal(err)
			}
			for _, msg := range []proto.Message{logs.Msg, report.Msg} {
				if _, err := proto.Marshal(msg); err != nil {
					t.Fatal(err)
				}
			}
			end := min(offset+int64(limit), int64(len(raw)))
			if !utf8.ValidString(logs.Msg.Text) || logs.Msg.Text != report.Msg.Text || logs.Msg.NextOffset != end || report.Msg.NextOffset != end || logs.Msg.Complete != (end == int64(len(raw))) {
				t.Fatalf("invalid text page: %+v", logs.Msg)
			}
			offset = end
		}
	}
	path, _ := s.Store.EvidencePath(r.ID, evidence.ID)
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, raw) {
		t.Fatal("stored evidence was modified", err)
	}
}
