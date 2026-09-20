package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExpandedReportStreamsToCompleteEvidence(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand='unused'\n")
	r, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	input := bytes.Repeat([]byte("a"), 8*1024*1024)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	evidence, err := s.saveRedactedEvidence(r.RunID, "expanded.xml", input, []string{"a"})
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4*1024*1024 {
		t.Fatalf("expanded evidence was buffered: allocated %d bytes", allocated)
	}
	if evidence.Size != int64(len(input)*len(redactionMarker)) {
		t.Fatal(evidence)
	}
	path, err := s.Store.EvidencePath(r.RunID, evidence.ID)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(h.Sum(nil)) != evidence.SHA256 {
		t.Fatal("wrong evidence digest")
	}
	tail := make([]byte, len(redactionMarker)*3)
	if _, err = f.ReadAt(tail, evidence.Size-int64(len(tail))); err != nil {
		t.Fatal(err)
	}
	if string(tail) != strings.Repeat(redactionMarker, 3) {
		t.Fatal("missing redacted tail")
	}
	entries, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".ach-report-*"))
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary evidence leaked", entries, err)
	}
}

func TestExpandedFailureTextRetainsBoundedRedactedPrefix(t *testing.T) {
	text, cut := redactedFailureText(strings.Repeat("a", 8*1024*1024), []string{"a"})
	if !cut || len(text) > failureFieldBytes || !strings.Contains(text, "[truncated;") {
		t.Fatal("missing bounded summary", len(text), cut)
	}
	secret := "CROSS-BOUNDARY-SECRET"
	text, cut = redactedFailureText(strings.Repeat("b", failureFieldBytes-5)+secret+"tail", []string{secret})
	if !cut || strings.Contains(text, "CROSS") {
		t.Fatal("secret prefix leaked at summary boundary")
	}
}
