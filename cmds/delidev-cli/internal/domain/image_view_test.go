// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestImageViewSnapshotHasNoInputOrFileAuthority(t *testing.T) {
	reference := ImageViewObservation{ReferenceID: NewID(), MachineID: NewID(), ManifestDigest: strings.Repeat("a", 64), Location: "images/original.png"}
	snapshot := ToolSnapshot{Kind: ImageViewTool, Status: ToolRunning, ImageView: &reference}
	if snapshot.Validate() != nil {
		t.Fatal("valid metadata observation rejected")
	}
	for _, change := range []func(*ToolSnapshot){func(s *ToolSnapshot) { s.Status = ToolFailed }, func(s *ToolSnapshot) { s.Kind = CommandTool }, func(s *ToolSnapshot) { s.Changes = []FileChangeObservation{} }, func(s *ToolSnapshot) { s.Command = &CommandObservation{} }, func(s *ToolSnapshot) { s.Read = &OpenCodeReadObservation{} }, func(s *ToolSnapshot) { s.ImageView = nil }} {
		next := snapshot
		change(&next)
		if next.Validate() == nil {
			t.Fatal("image view accepted mixed or fabricated evidence")
		}
	}
}

func TestImageViewMetadataPreservesLiteralPOSIXCharacters(t *testing.T) {
	for _, location := range []string{"images/plain.png", "screens/frame:01.png", `screens/frame\01.png`, `screens/frame:01\raw.png`} {
		observation := ImageViewObservation{ReferenceID: NewID(), MachineID: NewID(), ManifestDigest: strings.Repeat("a", 64), Location: location}
		snapshot := ToolSnapshot{Kind: ImageViewTool, Status: ToolRunning, ImageView: &observation}
		raw, err := json.Marshal(snapshot)
		var retained ToolSnapshot
		if err != nil || Decode(raw, &retained) != nil || retained.Validate() != nil || retained.ImageView == nil || *retained.ImageView != observation {
			t.Fatal("inert original metadata did not round trip", location, err)
		}
	}
	for _, location := range []string{"/absolute.png", "../outside.png", "images/../outside.png", "images//image.png", "https://example.com/image.png", "file:///image.png", ".", "", "images/\x00.png"} {
		observation := ImageViewObservation{ReferenceID: NewID(), MachineID: NewID(), ManifestDigest: strings.Repeat("a", 64), Location: location}
		if observation.Validate() == nil {
			t.Fatal("invalid relative metadata accepted", location)
		}
	}
}
