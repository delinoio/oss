package core

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestCommittedConfigReadIsBoundedBeforeParsing(t *testing.T) {
	const prefix = "version=1\n[checks.test]\ncommand=\"true\"\n"
	for _, size := range []int{maxProjectConfigBytes - 1, maxProjectConfigBytes, maxProjectConfigBytes + 1, 64 * maxProjectConfigBytes} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s, repo := fixture(t, prefix+strings.Repeat("\n", size-len(prefix)))
			ctx := context.Background()
			sha, err := ResolveCommit(ctx, repo, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			p, err := CommitConfig(ctx, repo, sha)
			runtime.ReadMemStats(&after)
			if size <= maxProjectConfigBytes {
				if err != nil || p.Checks["test"].Command != "true" {
					t.Fatalf("exact byte boundary rejected: %v", err)
				}
				return
			}
			var configErr *Error
			if !errors.As(err, &configErr) || configErr.Code != "invalid-config" || configErr.Exit != 2 {
				t.Fatalf("oversized config: %v", err)
			}
			// The bound includes read-buffer growth and process plumbing, but
			// rules out buffering the 64 MiB object before enforcing the limit.
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16*maxProjectConfigBytes {
				t.Fatalf("configuration read allocated %d bytes", allocated)
			}
			if _, err = s.Submit(ctx, repo, sha, false); !errors.As(err, &configErr) || configErr.Code != "invalid-config" {
				t.Fatalf("oversized configuration was accepted: %v", err)
			}
			var count int
			if err = s.Store.DB.QueryRow("SELECT COUNT(*) FROM runs").Scan(&count); err != nil || count != 0 {
				t.Fatalf("invalid configuration produced a receipt: %d, %v", count, err)
			}
		})
	}
}

func TestCommittedConfigMissingObjectRemainsUnavailable(t *testing.T) {
	_, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"true\"\n")
	_, err := CommitConfig(context.Background(), repo, strings.Repeat("0", 40))
	var configErr *Error
	if !errors.As(err, &configErr) || configErr.Code != "config-unavailable" || configErr.Exit != 2 {
		t.Fatalf("missing configuration: %v", err)
	}
}
