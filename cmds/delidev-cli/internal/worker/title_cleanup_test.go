package worker

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestTitleRuntimeRemovalPreservesCleanupUncertainty(t *testing.T) {
	inference := domain.Fail(domain.Unavailable, "Title inference timed out.", "Keep the original operation.")
	for _, test := range []struct {
		name               string
		removeErr, syncErr error
		inference          error
		wantRecovery       bool
	}{
		{name: "inference and removal failure", removeErr: errors.New("private removal failure"), inference: inference, wantRecovery: true},
		{name: "inference and synchronization failure", syncErr: errors.New("private synchronization failure"), inference: inference, wantRecovery: true},
		{name: "success and removal failure", removeErr: errors.New("private removal failure"), wantRecovery: true},
		{name: "success and synchronization failure", syncErr: errors.New("private synchronization failure"), wantRecovery: true},
		{name: "verified cleanup preserves inference failure", inference: inference},
		{name: "verified cleanup preserves success"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := json.RawMessage(`{"title":"retained output"}`)
			var calls []string
			got, err := finishTitleRuntimeRemoval("private-title-home", output, test.inference, func(path string) error {
				if path != "private-title-home" {
					t.Fatal("removal changed original runtime scope")
				}
				calls = append(calls, "remove")
				return test.removeErr
			}, func(path string) error {
				if path != "private-title-home" {
					t.Fatal("synchronization changed original runtime scope")
				}
				calls = append(calls, "sync")
				return test.syncErr
			})
			wantCalls := 2
			if test.removeErr != nil {
				wantCalls = 1
			}
			if len(calls) != wantCalls || calls[0] != "remove" || len(calls) == 2 && calls[1] != "sync" {
				t.Fatalf("unexpected cleanup order: %v", calls)
			}
			if test.wantRecovery {
				if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || got != nil {
					t.Fatal("cleanup uncertainty did not discard output and retain recovery")
				}
				if domain.SafeError(err).Message == "private removal failure" || domain.SafeError(err).Message == "private synchronization failure" {
					t.Fatal("cleanup result exposed underlying filesystem diagnostics")
				}
			} else if err != test.inference || string(got) != string(output) {
				t.Fatal("verified cleanup changed the original outcome")
			}
		})
	}
}
