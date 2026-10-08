// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQuotaExternalAccessOnlyAndRefusedRefresh(t *testing.T) {
	for _, mode := range []string{"quota-success", "quota-refresh", "quota-foreign-store"} {
		t.Run(mode, func(t *testing.T) {
			cfg := fixtureConfig(t, mode)
			cfg.Mode = QuotaProtocol
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := Open(ctx, cfg)
			if mode == "quota-foreign-store" {
				if err == nil {
					c.Close()
					t.Fatal("accepted file-backed auth")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			v, err := c.ReadExternalQuota(ctx, domain.NewID(), QuotaAuthentication{Access: "synthetic-access-only", Account: "synthetic-account", Plan: "plus"})
			if mode == "quota-success" {
				if err != nil || len(v.Windows) != 1 || v.Windows[0].Remaining == nil || *v.Windows[0].Remaining != .8 {
					t.Fatal("access-only projection failed", err)
				}
			} else {
				if err == nil {
					t.Fatal("refresh callback succeeded")
				}
				if _, err := os.Stat(filepath.Join(cfg.Home, "quota-refused")); err != nil {
					t.Fatal("refresh not explicitly refused", err)
				}
			}
			if _, err := os.Stat(filepath.Join(cfg.Home, "auth.json")); !os.IsNotExist(err) {
				t.Fatal("quota wrote managed auth file")
			}
			if _, err := c.ReadExternalQuota(ctx, domain.NewID(), QuotaAuthentication{Access: "synthetic-access-only", Account: "synthetic-account", Plan: "plus"}); err == nil {
				t.Fatal("quota replay sent twice")
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQuotaExternalRejectsForeignOrMalformedCompletionBeforeRead(t *testing.T) {
	for _, mode := range []string{"failed", "foreign", "error", "onboarding", "unknown", "missing-success", "malformed-success"} {
		t.Run(mode, func(t *testing.T) {
			cfg := fixtureConfig(t, "quota-completion-"+mode)
			cfg.Mode = QuotaProtocol
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.ReadExternalQuota(ctx, domain.NewID(), QuotaAuthentication{Access: "synthetic-access-only", Account: "synthetic-account", Plan: "plus"})
			if err == nil {
				t.Fatal("invalid completion authorized quota")
			}
			if _, err := os.Stat(filepath.Join(cfg.Home, "quota-read")); !os.IsNotExist(err) {
				t.Fatal("invalid completion sent quota")
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
