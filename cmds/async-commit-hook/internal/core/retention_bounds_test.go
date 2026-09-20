package core

import (
	"bytes"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestRetentionAgeOverflowRejectedWithoutDeletingEvidence(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	r := runFixture(t, s, repo)
	r.CreatedAt = time.Now().Add(-72 * time.Hour)
	if err := s.Store.SaveRun(r); err != nil {
		t.Fatal(err)
	}
	evidence, err := s.SaveEvidence(r.ID, "retained.txt", []byte("must survive invalid retention"))
	if err != nil {
		t.Fatal(err)
	}
	path, err := s.Store.EvidencePath(r.ID, evidence.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := Encode(r)
	for _, age := range []int{-1, maxRetentionAgeDays + 1, 1000000, int(^uint(0) >> 1)} {
		for _, dry := range []bool{true, false} {
			if _, err = s.Prune(dry, age, 0); err == nil {
				t.Fatalf("accepted retention age %d", age)
			}
			actual, err := s.Store.Run(r.ID)
			if err != nil || !bytes.Equal(before, Encode(actual)) {
				t.Fatal("invalid retention mutated run", err)
			}
			if b, err := os.ReadFile(path); err != nil || string(b) != "must survive invalid retention" {
				t.Fatal("invalid retention removed evidence", err)
			}
		}
		if err = os.WriteFile(s.Paths.Config, []byte(fmt.Sprintf("version=1\n[retention]\nmax_age_days=%d\n", age)), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = ReadPersonal(s.Paths); err == nil {
			t.Fatalf("personal config accepted age %d", age)
		}
	}
	for _, age := range []int{0, maxRetentionAgeDays} {
		if err = os.WriteFile(s.Paths.Config, []byte(fmt.Sprintf("version=1\n[retention]\nmax_age_days=%d\n", age)), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = ReadPersonal(s.Paths); err != nil {
			t.Fatal("valid retention boundary rejected", err)
		}
		if result, err := s.Prune(true, age, 0); err != nil || len(result.RunIDs) != 0 {
			t.Fatal("valid long retention expired recent evidence", err)
		}
	}
}
