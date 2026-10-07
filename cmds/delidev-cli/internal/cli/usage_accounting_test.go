// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestCLIUsageAccountingNegotiation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("temporary server did not start")
	}
	for _, selection := range [][]string{
		{"usage", "summary", "--accounting-profile", "native-units-v1"},
		{"usage", "summary", "--accounting-profile", "native-units-v1", "--granularity", "day", "--timezone", "Asia/Seoul"},
	} {
		code, result := cliRun(t, root, selection, "")
		if code != 0 || result["result"].(map[string]any)["accounting_profile"] != "USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1" {
			t.Fatal("CLI lost negotiated profile", result)
		}
	}
	code, legacy := cliRun(t, root, []string{"usage", "summary"}, "")
	if code != 0 || legacy["result"].(map[string]any)["accounting_profile"] != "USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1" {
		t.Fatal("default profile did not require native accounting", legacy)
	}
	if code, _ := cliRun(t, root, []string{"usage", "summary", "--accounting-profile", "unknown"}, ""); code == 0 {
		t.Fatal("unknown profile accepted")
	}
}

func TestCLIUsageAccountingRejectsLegacyAndMalformedFamilies(t *testing.T) {
	claude := pb.AccountingUnitKind_ACCOUNTING_UNIT_KIND_CLAUDE_MAIN_LOOP_INPUT
	openCode := pb.AccountingUnitKind_ACCOUNTING_UNIT_KIND_OPENCODE_STEP
	for _, test := range []struct {
		name      string
		kinds     []pb.AccountingUnitKind
		supported bool
	}{
		{"schema-25-echo", nil, false},
		{"missing-family", []pb.AccountingUnitKind{claude}, false},
		{"duplicate-family", []pb.AccountingUnitKind{claude, claude, openCode}, false},
		{"unknown-family", []pb.AccountingUnitKind{claude, openCode, 99}, false},
		{"complete", []pb.AccountingUnitKind{openCode, claude}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			peer := httptest.NewServer(connect.NewUnaryHandler(delidevv1connect.UsageServiceGetUsageSummaryProcedure, func(_ context.Context, r *connect.Request[pb.GetUsageSummaryRequest]) (*connect.Response[pb.GetUsageSummaryResponse], error) {
				if r.Header().Get("Authorization") != "Bearer fixture-secret" || r.Msg.AccountingProfile != pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1 {
					t.Error("usage request lost its authorization or explicit profile")
				}
				result := &pb.GetUsageSummaryResponse{AccountingProfile: r.Msg.AccountingProfile}
				for _, kind := range test.kinds {
					result.NativeAccounting = append(result.NativeAccounting, &pb.NativeAccountingSummary{Totals: &pb.NativeAccountingTotals{Kind: kind}})
				}
				return connect.NewResponse(result), nil
			}))
			defer peer.Close()
			var out, diagnostic strings.Builder
			code := Run(context.Background(), []string{"--data-dir", filepath.Join(t.TempDir(), "unused"), "--server", peer.URL, "--token-stdin", "usage", "summary", "--accounting-profile", "native-units-v1"}, IO{In: strings.NewReader("fixture-secret"), Out: &out, Err: &diagnostic})
			if (code == 0) != test.supported || !test.supported && (!strings.Contains(out.String(), "unsupported") || !strings.Contains(out.String(), "Update the server")) || strings.Contains(out.String(), "fixture-secret") {
				t.Fatal("incompatible native accounting was trusted", code, out.String())
			}
		})
	}
}
