// SPDX-License-Identifier: Apache-2.0
package domain

import (
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

func TestImageViewRelativeMetadataPreservesPOSIXCharacters(t *testing.T) {
	v := ImageViewObservation{ReferenceID: NewID(), MachineID: NewID(), ManifestDigest: strings.Repeat("a", 64)}
	for _, location := range []string{"screens/frame:01.png", `screens/frame\01.png`, `screens\archive/frame:01.png`, "frame:01.png", "C:/frame.png", `\\server\share.png`, `C:\frame.png`} {
		v.Location = location
		if err := v.Validate(); err != nil {
			t.Fatalf("inert POSIX location %q rejected: %v", location, err)
		}
	}
	for _, location := range []string{"/image.png", "https://example.com/image.png", "../image.png", "images/../image.png", "images//image.png", "images/./image.png", "images/\x00image.png"} {
		v.Location = location
		if v.Validate() == nil {
			t.Fatalf("absolute or malformed metadata accepted: %q", location)
		}
	}
}
