package server

import (
	"context"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// legacyWorkerAccepts reproduces the image assignment rules of a Worker built
// before the OpenCode image profile: ExecutionConfiguration.Validate rejected a
// frozen image declaration for every non-Codex harness, and an assignment with
// attachments required Codex plus that declaration.
func legacyWorkerAccepts(c domain.ExecutionConfiguration, attachments []domain.ImageAttachment) bool {
	if c.ImageInputDeclared && c.Harness != domain.Codex {
		return false
	}
	return len(attachments) == 0 || c.Harness == domain.Codex && c.ImageInputDeclared && c.SidechatPolicy == ""
}

// currentWorkerAccepts runs the real assignment rules of this build.
func currentWorkerAccepts(c domain.ExecutionConfiguration, attachments []domain.ImageAttachment, machine, prompt string) bool {
	job := domain.ExecutionJobInput{MachineID: domain.ID(machine), Configuration: c, Input: domain.SessionInput{Prompt: prompt, Attachments: attachments}}
	return c.Validate() == nil && job.ValidateImages() == nil
}

// setWorkerCapabilities replaces the capabilities a Runner Device reported on
// its last attachment, as an older or newer Worker build would.
func setWorkerCapabilities(t *testing.T, f *firstDispatchFixture, capabilities []domain.WorkerCapability) {
	t.Helper()
	_, err := f.service.Store.Mutate(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), domain.NewID(), "fixture.worker-capabilities", nil, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.MachineKind, domain.ID(f.machine.Id))
		if err != nil {
			return nil, err
		}
		machine, err := store.Decode[domain.Machine](row)
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = capabilities
		return tx.Put(domain.MachineKind, row.ID, row.Revision, "", "", machine)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func machineCapabilities(t *testing.T, f *firstDispatchFixture) []domain.WorkerCapability {
	t.Helper()
	var result []domain.WorkerCapability
	if err := f.service.Store.Read(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), func(tx *store.Tx) error {
		row, err := tx.Get(domain.MachineKind, domain.ID(f.machine.Id))
		if err != nil {
			return err
		}
		machine, err := store.Decode[domain.Machine](row)
		result = machine.WorkerCapabilities
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

// TestOpenCodeImageCompatibilityMatrix covers new and old servers against new
// and old Workers for text-only and image-bearing OpenCode inputs. The old
// server and old Worker rules are reproduced by the helpers above because only
// this build can run.
func TestOpenCodeImageCompatibilityMatrix(t *testing.T) {
	f, _, raw := imageRPCFixture(t, domain.OpenCode)
	newWorker := machineCapabilities(t, f)
	if !domain.ImageInputWorkerSupported(domain.OpenCode, newWorker) {
		t.Fatal("fixture Worker lacks the OpenCode image pair")
	}
	oldWorker := make([]domain.WorkerCapability, 0, len(newWorker))
	for _, capability := range newWorker {
		if capability != domain.OpenCodeImageInputsV1 {
			oldWorker = append(oldWorker, capability)
		}
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	machine := f.machine.Id
	ref := domain.ImageAttachment{ID: domain.NewID(), MachineID: domain.ID(machine), MediaType: domain.ImagePNG, ByteLength: uint64(len(raw)), SHA256: "0000000000000000000000000000000000000000000000000000000000000000"}

	type row struct {
		server, worker, input string
		admitted              bool // the server admits the input for this Runner Device
		declared              bool // the first-execution snapshot freezes the image declaration
		workerAccepts         bool // the Worker build accepts the resulting assignment
	}
	var rows []row
	var declaredSnapshot domain.ExecutionConfiguration
	for _, worker := range []string{"old", "new"} {
		caps := oldWorker
		if worker == "new" {
			caps = newWorker
		}
		setWorkerCapabilities(t, f, caps)
		var preview domain.ExecutionConfiguration
		var imageErr error
		if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
			result, err := tx.PreviewInitialExecution(domain.Session{AgentID: domain.ID(f.agent.Id), MachineID: domain.ID(machine)})
			if err != nil {
				return err
			}
			preview = result.Configuration
			imageErr = checkImageRoute(tx, domain.ID(f.agent.Id), domain.ID(machine), "describe", []domain.ImageAttachment{ref})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if worker == "new" {
			declaredSnapshot = preview
		}
		workerAccepts := func(c domain.ExecutionConfiguration, attachments []domain.ImageAttachment) bool {
			if worker == "old" {
				return legacyWorkerAccepts(c, attachments)
			}
			return currentWorkerAccepts(c, attachments, machine, "describe")
		}
		for _, input := range []string{"text-only", "image-bearing"} {
			attachments := []domain.ImageAttachment(nil)
			if input == "image-bearing" {
				attachments = []domain.ImageAttachment{ref}
			}
			// New server: the real preview, admission and dispatch gate.
			admitted := len(attachments) == 0 || imageErr == nil
			if admitted && requireOpenCodeImageWorker(preview, caps) != nil {
				t.Errorf("new server/%s worker/%s: an admitted assignment was refused by the dispatch gate", worker, input)
			}
			rows = append(rows, row{"new", worker, input, admitted, preview.ImageInputDeclared, admitted && workerAccepts(preview, attachments)})
			// Old server: it never freezes an OpenCode image declaration and never
			// admits an OpenCode attachment, so only the text-only assignment exists.
			legacy := preview
			legacy.ImageInputDeclared = false
			rows = append(rows, row{"old", worker, input, len(attachments) == 0, false, len(attachments) == 0 && workerAccepts(legacy, nil)})
		}
	}
	want := []row{
		{"new", "old", "text-only", true, false, true},
		{"old", "old", "text-only", true, false, true},
		{"new", "old", "image-bearing", false, false, false},
		{"old", "old", "image-bearing", false, false, false},
		{"new", "new", "text-only", true, true, true},
		{"old", "new", "text-only", true, false, true},
		{"new", "new", "image-bearing", true, true, true},
		{"old", "new", "image-bearing", false, false, false},
	}
	if len(rows) != len(want) {
		t.Fatalf("matrix has %d rows, want %d", len(rows), len(want))
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d: got %+v, want %+v", i, rows[i], want[i])
		}
	}
	// The snapshot frozen for a new Worker is exactly what an older Worker rejects,
	// which is why the declaration is pinned only after the pair is negotiated.
	if !declaredSnapshot.ImageInputDeclared || legacyWorkerAccepts(declaredSnapshot, nil) {
		t.Fatal("the negotiated snapshot no longer distinguishes older Workers")
	}
	// A frozen declaration that reaches an older Worker is refused by the server
	// before dispatch instead of failing on the Worker.
	if err := requireOpenCodeImageWorker(domain.ExecutionConfiguration{Harness: domain.OpenCode, ImageInputDeclared: true}, oldWorker); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatalf("declared OpenCode assignment reached an older Worker: %v", err)
	}
	for _, c := range []domain.ExecutionConfiguration{{Harness: domain.OpenCode}, {Harness: domain.Codex, ImageInputDeclared: true}} {
		if err := requireOpenCodeImageWorker(c, oldWorker); err != nil {
			t.Fatalf("dispatch gate refused %+v: %v", c, err)
		}
	}
}

func TestAttachWorkerRejectsOpenCodeImagesWithoutBaseImageCapability(t *testing.T) {
	f, _, _ := imageRPCFixture(t, domain.OpenCode)
	before := machineCapabilities(t, f)
	_, err := f.workerClient.AttachWorker(context.Background(), ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{
		ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: string(domain.NewID()), Version: rpc.Version,
		Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_OPENCODE_IMAGE_INPUTS_V1},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("the OpenCode image capability without the base capability was accepted", err)
	}
	if after := machineCapabilities(t, f); !domain.OpenCodeImageInputsPaired(after) || len(after) != len(before) {
		t.Fatal("an unpaired OpenCode image capability was persisted", after)
	}
}

// TestOpenCodeImageMutationPathsShareTheTextRule lists every server path that
// can change the text that travels with image references and asserts that all
// of them reject blank OpenCode text while Codex keeps accepting image-only
// messages. Removal changes no text and is always allowed (the edit test).
// Steer rejects attachments on either side before any harness check.
func TestOpenCodeImageMutationPathsShareTheTextRule(t *testing.T) {
	f, _, raw := imageRPCFixture(t, domain.OpenCode)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	ref := domain.ImageAttachment{ID: domain.NewID(), MachineID: domain.ID(f.machine.Id), MediaType: domain.ImagePNG, ByteLength: uint64(len(raw)), SHA256: "0000000000000000000000000000000000000000000000000000000000000000"}
	refs := []domain.ImageAttachment{ref}
	snapshot := func(harness domain.Harness) domain.Session {
		return domain.Session{AgentID: domain.ID(f.agent.Id), MachineID: domain.ID(f.machine.Id), InitialExecution: &domain.InitialExecution{Configuration: domain.ExecutionConfiguration{Harness: harness, ImageInputDeclared: true}}}
	}
	unclaimed := domain.Session{AgentID: domain.ID(f.agent.Id), MachineID: domain.ID(f.machine.Id)}
	job := func(harness domain.Harness, prompt string) error {
		return domain.ExecutionJobInput{MachineID: domain.ID(f.machine.Id), Configuration: domain.ExecutionConfiguration{Harness: harness, ImageInputDeclared: true}, Input: domain.SessionInput{Prompt: prompt, Attachments: refs}}.ValidateImages()
	}
	type path struct {
		name  string
		check func(tx *store.Tx, harness domain.Harness, prompt string) error
	}
	paths := []path{
		{"create", func(tx *store.Tx, _ domain.Harness, prompt string) error {
			return checkImageRoute(tx, domain.ID(f.agent.Id), domain.ID(f.machine.Id), prompt, refs)
		}},
		{"enqueue before first claim", func(tx *store.Tx, _ domain.Harness, prompt string) error {
			return checkSessionImageRoute(tx, unclaimed, prompt, refs)
		}},
		{"enqueue after first claim", func(tx *store.Tx, harness domain.Harness, prompt string) error {
			return checkSessionImageRoute(tx, snapshot(harness), prompt, refs)
		}},
		{"edit queued before first claim", func(tx *store.Tx, _ domain.Harness, prompt string) error {
			return checkQueuedImageEdit(tx, unclaimed, prompt, refs)
		}},
		{"edit queued after first claim", func(tx *store.Tx, harness domain.Harness, prompt string) error {
			return checkQueuedImageEdit(tx, snapshot(harness), prompt, refs)
		}},
		{"dispatch of any queued, retried, continued or forked input", func(_ *store.Tx, harness domain.Harness, prompt string) error {
			return job(harness, prompt)
		}},
	}
	for _, p := range paths {
		for _, prompt := range []string{"describe", "", " \n\t "} {
			t.Run(fmt.Sprintf("%s/opencode/%q", p.name, prompt), func(t *testing.T) {
				var err error
				if readErr := f.service.Store.Read(ctx, func(tx *store.Tx) error { err = p.check(tx, domain.OpenCode, prompt); return nil }); readErr != nil {
					t.Fatal(readErr)
				}
				switch {
				case prompt == "describe" && err != nil:
					t.Fatalf("nonblank OpenCode text rejected: %v", err)
				case prompt != "describe" && domain.SafeError(err).Code != domain.Unsupported:
					t.Fatalf("blank OpenCode text kept its images: %v", err)
				}
			})
		}
	}
	// Codex keeps its image-only messages on every path that evaluates a harness.
	for _, p := range paths[2:] {
		if p.name == "edit queued before first claim" {
			continue
		}
		t.Run(p.name+"/codex image-only", func(t *testing.T) {
			var err error
			if readErr := f.service.Store.Read(ctx, func(tx *store.Tx) error { err = p.check(tx, domain.Codex, ""); return nil }); readErr != nil {
				t.Fatal(readErr)
			}
			if err != nil {
				t.Fatalf("Codex image-only message rejected: %v", err)
			}
		})
	}
}
