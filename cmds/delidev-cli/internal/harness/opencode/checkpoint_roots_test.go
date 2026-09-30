package opencode

import (
	"bytes"
	"encoding/json"
	"os"
	"runtime"
	"testing"
)

func TestHistoricalV1CheckpointBytesAndDigestsRemainUnchanged(t *testing.T) {
	platform, digest := "unix", "33592edc05ef46941a4d949fc101b123c2e63c761634c352249a89b929ddf017"
	if runtime.GOOS == "windows" {
		platform, digest = "windows", "075288e2b43a508d8f9f65ca30deb4efac28d82b8e40811994c7826bbe8316b5"
	}
	// Captured with main ad0e3e9a's version-1 struct and synthetic original
	// history; paths name only fictitious fixtures. No live runtime is inspected.
	raw, err := os.ReadFile("testdata/checkpoint-v1-" + platform + ".json")
	if err != nil || mutationDigest(raw) != digest {
		t.Fatal("historical checkpoint fixture changed", err)
	}
	var original nativeCheckpoint
	if json.Unmarshal(raw, &original) != nil {
		t.Fatal("invalid historical fixture")
	}
	ref := original.Reference
	ref.SHA256 = digest
	value, err := decodeCheckpoint(raw, ref, original.RuntimeHome)
	if err != nil || value.Version != 1 || value.FilesystemRoot != "" {
		t.Fatal("historical root validation or omitted-field meaning changed", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		t.Fatal("historical canonical bytes changed", err)
	}
	value.FilesystemRoot = original.NativeRoot
	if validCheckpointRoots(value) {
		t.Fatal("version-1 history acquired new filesystem authority")
	}
}
