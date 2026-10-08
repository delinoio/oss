// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func stageImage(t *testing.T, root string, machine domain.ID) domain.ImageAttachment {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	raw := out.Bytes()
	ref := domain.ImageAttachment{ID: domain.NewID(), MachineID: machine, MediaType: domain.ImagePNG, ByteLength: uint64(len(raw)), SHA256: imageinput.Digest(raw)}
	m := imageinput.Manager{Root: root}
	if _, err := m.Transfer(machine, &pb.AttachmentTransfer{Id: string(domain.NewID()), Attachment: imageinput.ToProto(ref), Operation: pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_WRITE, Data: raw, Sha256: imageinput.Digest(raw)}); err != nil {
		t.Fatal(err)
	}
	return ref
}
func TestOwnedImagesUseNativePrimitiveAndImmutableInputBinding(t *testing.T) {
	for _, prompt := range []string{"Describe both pictures", ""} {
		t.Run(prompt, func(t *testing.T) {
			c, capture, _, _ := boundTurnFixture(t, "images")
			c.imageRoot = t.TempDir()
			c.imageMachine = domain.NewID()
			refs := []domain.ImageAttachment{stageImage(t, c.imageRoot, c.imageMachine), stageImage(t, c.imageRoot, c.imageMachine)}
			input := domain.SessionInput{Prompt: prompt, Mode: domain.ExecuteMode, Attachments: refs}
			inputID := domain.NewID()
			turn, err := c.StartTurn(context.Background(), domain.NewID(), inputID, input)
			if err != nil {
				t.Fatal(err)
			}
			parts := requestsOf(t, capture, "turn/start")[0]["input"].([]any)
			expected := 2
			if prompt != "" {
				expected++
			}
			if len(parts) != expected {
				t.Fatal(parts)
			}
			for index, ref := range refs {
				part := parts[len(parts)-2+index].(map[string]any)
				if part["type"] != "localImage" || part["path"] != filepath.Join(c.imageRoot, "image-inputs", string(ref.ID)+".data") || part["text"] != nil {
					t.Fatal("native image primitive changed", part)
				}
			}
			nextKind(t, c, TurnStartedEvent)
			event := nextKind(t, c, MessageCompletedEvent)
			if event.Message == nil || event.Message.Text != prompt || !slices.Equal(event.Message.Attachments, refs) || event.Message.ClientInputID != inputID {
				t.Fatal(event)
			}
			if _, err := c.Steer(context.Background(), domain.NewID(), domain.NewID(), turn.TurnID, input); err == nil {
				t.Fatal("unproved image Steer accepted")
			}
			if len(requestsOf(t, capture, "turn/steer")) != 0 {
				t.Fatal("image Steer reached native")
			}
			paths, _ := (imageinput.Manager{Root: c.imageRoot}).Resolve(c.imageMachine, refs)
			if err := os.WriteFile(paths[0], []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"type": "localImage", "path": paths[0]})
			if _, err := c.nativeImageInput([]json.RawMessage{raw}); err == nil {
				t.Fatal("changed history bytes accepted")
			}
		})
	}
}
func TestUnsupportedModelDoesNotSendImageTurn(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "ready")
	c.imageRoot = t.TempDir()
	c.imageMachine = domain.NewID()
	ref := stageImage(t, c.imageRoot, c.imageMachine)
	_, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Attachments: []domain.ImageAttachment{ref}})
	assertCode(t, err, domain.Unsupported)
	if len(requestsOf(t, capture, "turn/start")) != 0 {
		t.Fatal("unsupported image sent")
	}
}
func TestNativeImageHistoryPreservesOrderedDigest(t *testing.T) {
	refs := []domain.ImageAttachment{{ID: domain.NewID(), MachineID: domain.NewID(), MediaType: domain.ImagePNG, ByteLength: 1, SHA256: imageinput.Digest([]byte("a"))}, {ID: domain.NewID(), MachineID: domain.NewID(), MediaType: domain.ImageJPEG, ByteLength: 2, SHA256: imageinput.Digest([]byte("bb"))}}
	parts := []json.RawMessage{json.RawMessage(`{"type":"text","text":"Look","text_elements":[]}`), json.RawMessage(`{"type":"localImage","path":"one"}`), json.RawMessage(`{"type":"localImage","path":"two"}`)}
	lookup := func(path string) (domain.ImageAttachment, error) {
		if path == "one" {
			return refs[0], nil
		}
		return refs[1], nil
	}
	got, err := decodeNativeInputParts(parts, lookup)
	if err != nil || got.InputDigest() != (domain.SessionInput{Prompt: "Look", Mode: domain.ExecuteMode, Attachments: refs}).InputDigest() {
		t.Fatal(got, err)
	}
	parts[1], parts[2] = parts[2], parts[1]
	swapped, err := decodeNativeInputParts(parts, lookup)
	if err != nil || got.InputDigest() == swapped.InputDigest() {
		t.Fatal("image order lost", err)
	}
}
