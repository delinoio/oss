// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

func TestNativeSkillReadPreservesLegacyCodexEnvelope(t *testing.T) {
	legacy := domain.SkillReadRequest{}
	before, _ := json.Marshal(legacy)
	digest := domain.SkillPreparationDigest(legacy)
	legacy.Harness = nativeSkillReadHarness(domain.Codex)
	after, _ := json.Marshal(legacy)
	if string(before) != string(after) || strings.Contains(string(after), "harness") || domain.SkillPreparationDigest(legacy) != digest {
		t.Fatal("Codex request shape or preparation digest changed")
	}
	legacy.Harness = nativeSkillReadHarness(domain.OpenCode)
	after, _ = json.Marshal(legacy)
	if !strings.Contains(string(after), `"harness":"opencode"`) || domain.SkillPreparationDigest(legacy) == digest {
		t.Fatal("OpenCode did not retain its independent request profile")
	}
}

func TestNativeSkillCapabilitiesDoNotAuthorizeAnotherHarness(t *testing.T) {
	codex := domain.Machine{WorkerCapabilities: []domain.WorkerCapability{domain.NativeSkillsV1, domain.ManagedNativeSkillsV1}}
	openCode := domain.Machine{WorkerCapabilities: []domain.WorkerCapability{domain.OpenCodeNativeSkillsV1}}
	for _, managed := range []bool{false, true} {
		if !supportsNativeSkills(codex, domain.Codex, managed) || supportsNativeSkills(codex, domain.OpenCode, managed) || !supportsNativeSkills(openCode, domain.OpenCode, managed) || supportsNativeSkills(openCode, domain.Codex, managed) {
			t.Fatal("native capability crossed Harness ownership")
		}
	}
}
