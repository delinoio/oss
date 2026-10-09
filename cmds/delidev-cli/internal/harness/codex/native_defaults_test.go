// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

func TestHarnessNativeDefaultsOriginalConfigAndStaleProof(t *testing.T) {
	c := &Client{mode: ThreadProtocol, version: domain.CodexProtocolVersion}
	config := map[string]json.RawMessage{"model": json.RawMessage(`"native-fixture"`), "model_reasoning_effort": json.RawMessage(`"medium"`), "service_tier": json.RawMessage(`null`)}
	if err := c.captureNativeDefaults(config); err != nil {
		t.Fatal(err)
	}
	d := c.NativeDefaults()
	if d == nil || d.Model == nil || *d.Model != "native-fixture" || d.Effort == nil || *d.Effort != "medium" || d.ServiceTier != nil {
		t.Fatal("lost original defaults or manufactured null value")
	}
	digest, _ := d.Digest()
	p := domain.NativeHarnessDefaultProof{Version: 1, MachineID: domain.NewID(), DeviceID: domain.NewID(), AccountID: domain.NewID(), ConnectionID: domain.NewID(), JobID: domain.NewID(), ExecutionID: domain.NewID(), ProfileDigest: strings.Repeat("a", 64), ExecutableSHA256: strings.Repeat("b", 64), NativeVersion: c.version, Defaults: *d, DefaultsDigest: digest}
	if err := c.VerifyNativeDefaults(&p, p.ExecutableSHA256); err != nil {
		t.Fatal(err)
	}
	// Repeated config verification immediately before thread overrides must fence
	// changes even after initialization validated the original actual process.
	config["model"] = json.RawMessage(`"other-model"`)
	if err := c.captureNativeDefaults(config); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("stale defaults accepted: %v", err)
	}
	delete(config, "model")
	if err := c.captureNativeDefaults(config); err == nil {
		t.Fatal("unknown model accepted original default proof")
	}
	malformed := &Client{mode: ThreadProtocol}
	malformed.captureNativeDefaults(map[string]json.RawMessage{"model": json.RawMessage(`{}`)})
	if malformed.NativeDefaults() != nil {
		t.Fatal("malformed default became authority")
	}
	advisory := &Client{mode: ProbeProtocol}
	advisory.captureNativeDefaults(config)
	if advisory.NativeDefaults() != nil {
		t.Fatal("discovery process acquired default authority")
	}
}
