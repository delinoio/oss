// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeConfigurationWorkerOriginalAssignment(t *testing.T) {
	scope, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(scope, "config.toml"), []byte("service_tier = 'fast'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := domain.NativeConfigurationJobInput{Actor: domain.Principal{Type: domain.OwnerDevice}, Read: domain.NativeConfigurationRead{Scopes: []domain.NativeConfigurationScope{{Kind: domain.NativeConfigurationHome, Path: scope}}}, MachineID: domain.NewID(), DeviceID: domain.NewID(), InstanceID: domain.NewID()}
	raw, _ := json.Marshal(input)
	job := domain.Job{Type: domain.InspectCodexConfigurationJob, MachineID: input.MachineID, AssignedDeviceID: input.DeviceID, InstanceID: input.InstanceID, Input: raw}
	config := Config{Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	output, err := execute(context.Background(), config, domain.NewID(), job)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot domain.NativeConfigurationSnapshot
	if domain.Decode(output, &snapshot) != nil || snapshot.Validate() != nil || len(snapshot.Entries) != 1 || snapshot.Entries[0].Value != "fast" {
		t.Fatal("bounded source preview not dispatched")
	}
	for _, field := range []string{"machine", "device", "instance"} {
		changed := job
		switch field {
		case "machine":
			changed.MachineID = domain.NewID()
		case "device":
			changed.AssignedDeviceID = domain.NewID()
		case "instance":
			changed.InstanceID = domain.NewID()
		}
		if _, err = execute(context.Background(), config, domain.NewID(), changed); err == nil {
			t.Fatalf("replacement %s read source", field)
		}
	}
}
