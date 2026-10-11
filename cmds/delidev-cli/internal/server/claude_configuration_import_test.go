// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestClaudeImportNativePermissionOnlyUpdatesExplicitDestination(t *testing.T) {
	for _, kind := range []domain.NativeConfigurationTargetKind{domain.NativeConfigurationServerDefaults, domain.NativeConfigurationProjectDefaults, domain.NativeConfigurationAgentWorker} {
		t.Run(string(kind), func(t *testing.T) {
			f := newHarnessNativeImportServerFixture(t, domain.ClaudeCode, kind)
			preview := f.preview()
			request := f.applyRequest(preview, []string{f.observation.Entries[4].ID}, false)
			accepted, err := f.s.ApplyNativeConfigurationImport(transferOwner(), connect.NewRequest(request))
			if err != nil {
				t.Fatal(err)
			}
			result := f.report(f.claim(accepted.Msg.Job), f.observation)
			var job domain.Job
			if domain.Decode(result.DocumentJson, &job) != nil || job.State != domain.JobSucceeded {
				t.Fatal("Claude permission apply failed", job.Problem)
			}
			record, err := f.s.Store.Get(context.Background(), f.selection.Target.EntityKind(), f.selection.Target.ID)
			if err != nil || record.Revision != 2 {
				t.Fatal(err)
			}
			var settings domain.HarnessSettings
			switch kind {
			case domain.NativeConfigurationServerDefaults:
				value, _ := store.Decode[domain.Settings](record)
				settings = value.HarnessDefaults.Entries[0].Settings
			case domain.NativeConfigurationProjectDefaults:
				value, _ := store.Decode[domain.Project](record)
				settings = value.HarnessDefaults.Entries[0].Settings
			case domain.NativeConfigurationAgentWorker:
				value, _ := store.Decode[domain.Agent](record)
				settings = *value.HarnessSettings
				if value.Routes[0].Model.NativeID != "" || len(value.Templates) != 0 {
					t.Fatal("unselected route or instructions changed")
				}
			}
			if settings.ClaudePermission == nil || *settings.ClaudePermission.Value != domain.ClaudePermissionAcceptEdits || settings.Permission != nil || settings.Model != nil || settings.Effort != nil {
				t.Fatal("native mode translated or unselected fields changed", settings)
			}
		})
	}
}
