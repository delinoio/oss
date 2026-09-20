package core

import (
	"bytes"
	"context"
	"os"
	"strings"
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
		var assembled strings.Builder
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
			end := logs.Msg.NextOffset
			if end <= offset || end > min(offset+int64(limit)+3, int64(len(raw))) || !utf8.ValidString(logs.Msg.Text) || logs.Msg.Text != report.Msg.Text || report.Msg.NextOffset != end || logs.Msg.Complete != (end == int64(len(raw))) {
				t.Fatalf("invalid text page: %+v", logs.Msg)
			}
			offset = end
			assembled.WriteString(logs.Msg.Text)
		}
		if !strings.HasSuffix(assembled.String(), "b€") {
			t.Fatalf("valid text was damaged: %q", assembled.String())
		}
	}
	path, _ := s.Store.EvidencePath(r.ID, evidence.ID)
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, raw) {
		t.Fatal("stored evidence was modified", err)
	}
}

func TestEvidencePagesPreserveRunesAndIncompleteLiveTail(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"unused\"\n")
	r, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"a한€🙂z", strings.Repeat("x", 65535) + "🙂한"} {
		e, err := s.SaveEvidence(r.RunID, "text", []byte(text))
		if err != nil {
			t.Fatal(err)
		}
		for _, limit := range []int{1, 2, 3, 4, 65536} {
			var got strings.Builder
			for offset := int64(0); ; {
				p, err := s.evidencePage(r.RunID, e.ID, offset, limit, true)
				if err != nil {
					t.Fatal(err)
				}
				got.WriteString(p.Text)
				if p.Complete {
					break
				}
				if p.NextOffset <= offset {
					t.Fatal("page did not advance")
				}
				offset = p.NextOffset
			}
			if got.String() != text {
				t.Fatalf("limit %d corrupted valid UTF-8", limit)
			}
		}
	}
	e, err := s.SaveEvidence(r.RunID, "live", []byte{'a', 0xf0, 0x9f})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.evidencePage(r.RunID, e.ID, 0, 100, false)
	if err != nil || p.Text != "a" || p.NextOffset != 1 || p.Complete {
		t.Fatalf("live partial rune: %+v %v", p, err)
	}
	path, _ := s.Store.EvidencePath(r.RunID, e.ID)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write([]byte{0x99, 0x82})
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.evidencePage(r.RunID, e.ID, 1, 1, true)
	if err != nil || p.Text != "🙂" || p.NextOffset != 5 || !p.Complete {
		t.Fatalf("completed rune: %+v %v", p, err)
	}
}
