// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"image"
	"image/png"
	"testing"
)

func TestGeneratedImageCleanupUsesEveryFrozenOriginalGeneration(t *testing.T) {
	root := t.TempDir()
	manager := imageinput.Manager{Root: root}
	machine, instance := domain.NewID(), domain.NewID()
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	work := domain.SessionDeletionWork{MachineID: machine, GeneratedImageCleanup: true}
	refs := []domain.ImageAttachment{}
	for n := 0; n < 3; n++ {
		owner := imageinput.GenerationOwner{JobID: domain.NewID(), ExecutionID: domain.NewID(), InstanceID: instance, MachineID: machine}
		ref, err := manager.SaveGenerated(owner, "original-call", raw.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, ref)
		// The third generation is newer than the frozen deletion and is not acquired.
		if n < 2 {
			work.Copies = append(work.Copies, domain.SessionDeletionCopy{Type: domain.ExecuteSessionJob, JobID: owner.JobID, ExecutionID: owner.ExecutionID, InstanceID: owner.InstanceID})
		}
	}
	work.PreservedGeneratedImages = []domain.ImageAttachment{refs[0]}
	wrong := work
	wrong.Copies = append([]domain.SessionDeletionCopy(nil), work.Copies...)
	wrong.Copies[0].InstanceID = domain.NewID()
	if cleanupGeneratedCopies(root, wrong, false) == nil {
		t.Fatal("replacement instance adopted original intents")
	}
	if err := cleanupGeneratedCopies(root, work, false); err != nil {
		t.Fatal(err)
	}
	if manager.Removed(machine, refs[1]) != nil {
		t.Fatal("second original generation was omitted")
	}
	for _, ref := range []domain.ImageAttachment{refs[0], refs[2]} {
		if _, err := manager.Resolve(machine, []domain.ImageAttachment{ref}); err != nil {
			t.Fatal("independent or newer image retired", err)
		}
	}
	if err := cleanupGeneratedCopies(root, work, true); err != nil {
		t.Fatal("durable original cleanup replay failed", err)
	}
	if err := manager.DeleteGenerated(machine, refs[0]); err != nil {
		t.Fatal("last independent owner cleanup failed", err)
	}
}
