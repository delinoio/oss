package core

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
)

type repeatedTreeReader struct {
	record            string
	remaining, offset int
}

func (r *repeatedTreeReader) Read(b []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := copy(b, r.record[r.offset:])
	r.offset += n
	if r.offset == len(r.record) {
		r.offset = 0
		r.remaining--
	}
	return n, nil
}

func TestTreeListingStreamsWithoutRetainingAllPaths(t *testing.T) {
	const count = 100000
	reader := &repeatedTreeReader{record: "100644 blob " + strings.Repeat("a", 40) + "\t" + strings.Repeat("p", 1024) + "\x00", remaining: count}
	runtime.GC()
	var baseline, usage runtime.MemStats
	runtime.ReadMemStats(&baseline)
	seen := 0
	err := readTreeEntries(reader, func(e treeEntry) error {
		seen++
		if seen == 1 && reader.remaining == 0 {
			t.Fatal("buffered the whole listing before visiting")
		}
		if len(e.Path) != 1024 {
			t.Fatal("path was truncated")
		}
		if seen%10000 == 0 {
			runtime.GC()
			runtime.ReadMemStats(&usage)
			if usage.HeapAlloc > baseline.HeapAlloc+8*1024*1024 {
				t.Fatal("tree entries retained in memory", usage.HeapAlloc-baseline.HeapAlloc)
			}
		}
		return nil
	})
	if err != nil || seen != count {
		t.Fatal(seen, err)
	}
}

func TestTreeListingBoundsRecordsAndStopsOnVisitorFailure(t *testing.T) {
	prefix := "100644 blob " + strings.Repeat("a", 40) + "\t"
	for _, body := range []string{prefix + "unterminated", prefix + "../escape\x00", prefix + ".git/config\x00", prefix + strings.Repeat("x", maxTreeRecordBytes) + "\x00"} {
		if err := readTreeEntries(strings.NewReader(body), func(treeEntry) error { t.Fatal("invalid entry visited"); return nil }); err == nil {
			t.Fatal("invalid record accepted")
		}
	}
	boundary := prefix + strings.Repeat("x", maxTreeRecordBytes-len(prefix)) + "\x00"
	if err := readTreeEntries(strings.NewReader(boundary), func(treeEntry) error { return nil }); err != nil {
		t.Fatal("exact limit rejected", err)
	}
	sentinel := errors.New("stop visiting")
	r := &repeatedTreeReader{record: prefix + "file\x00", remaining: 10000}
	if err := readTreeEntries(r, func(treeEntry) error { return sentinel }); !errors.Is(err, sentinel) || r.remaining == 0 {
		t.Fatal("visitor failure did not stop stream", err)
	}
	s, repo := fixture(t, "version=1\n")
	_ = s
	if err := walkTree(context.Background(), repo, "HEAD", func(treeEntry) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal("Git stream did not propagate visitor failure", err)
	}
}
