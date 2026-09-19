package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAttributeBlobsAreBoundedBeforeAcceptance(t *testing.T) {
	for _, size := range []int{maxAttributeBytes, maxAttributeBytes + 1, 64 * maxAttributeBytes} {
		s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo check\"\n")
		ctx := context.Background()
		if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("#"+strings.Repeat("x", size-1)), 0600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "."}, {"-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "attributes"}} {
			if _, err := Git(ctx, repo, args...); err != nil {
				t.Fatal(err)
			}
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := s.Submit(ctx, repo, "HEAD", false)
		runtime.ReadMemStats(&after)
		if size == maxAttributeBytes {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var sourceErr *Error
		if !errors.As(err, &sourceErr) || sourceErr.Code != "attributes-too-large" || sourceErr.Exit != 2 {
			t.Fatalf("oversized attributes were accepted: %v", err)
		}
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16*maxAttributeBytes {
			t.Fatalf("attribute inspection allocated %d bytes", allocated)
		}
		var count int
		if err = s.Store.DB.QueryRow("SELECT COUNT(*) FROM runs").Scan(&count); err != nil || count != 0 {
			t.Fatalf("unsupported source produced a receipt: %d, %v", count, err)
		}
	}
}

func TestSourceLFSDeclarationsIgnoreCommentsAndPatternText(t *testing.T) {
	for _, tc := range []struct {
		name, attributes string
		rejected         bool
	}{
		{"comment", "# *.bin filter=lfs\n", false},
		{"indented-comment", " \t# *.bin filter=lfs\r\n*.txt text\n", false},
		{"bom-comment", "\uFEFF# *.bin filter=lfs\n", false},
		{"pattern", "filter=lfs text\n", false},
		{"quoted-pattern", "\"file filter=lfs\" text\n", false},
		{"different-value", "*.bin filter=lfs-other\n", false},
		{"unset", "*.bin filter=lfs -filter\n", false},
		{"active", "# documentation\n*.bin filter=lfs\n", true},
		{"quoted-active", "\"file \\\"quoted\\\" name\" filter=lfs\n", true},
		{"macro", "[attr]large filter=lfs\n*.bin large\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo check\"\n")
			ctx := context.Background()
			// Nested declarations must use the same parser as root attributes.
			dir := filepath.Join(repo, "nested")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte(tc.attributes), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", "."}, {"-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "attributes"}} {
				if _, err := Git(ctx, repo, args...); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.Submit(ctx, repo, "HEAD", false)
			if tc.rejected {
				var sourceErr *Error
				if !errors.As(err, &sourceErr) || sourceErr.Code != "lfs-unsupported" {
					t.Fatalf("active LFS declaration was not rejected: %v", err)
				}
			} else if err != nil {
				t.Fatalf("inactive declaration blocked submission: %v", err)
			}
		})
	}
}
