package server

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func openCodeImageUpload(t *testing.T, harness domain.Harness) (*firstDispatchFixture, *pb.AttachmentUpload, domain.ID) {
	t.Helper()
	f, c, raw := imageRPCFixture(t, harness)
	imageWorkerFixture(t, f, c)
	operation := domain.NewID()
	return f, finishImage(t, f, c, beginImage(t, f, c, raw, domain.NewID(), operation, ""), raw), operation
}

func createWithImage(f *firstDispatchFixture, u *pb.AttachmentUpload, operation domain.ID, prompt string) error {
	selection := f.selection
	selection.Prompt = prompt
	document, _ := json.Marshal(selection)
	_, err := sessionClient(f.accountFixture).CreateSession(context.Background(), ownerRequest(f.identity, &pb.CreateSessionRequest{RequestId: string(operation), DocumentJson: document, Attachments: []*pb.ImageAttachment{u.Attachment}}))
	return err
}

func uploadState(t *testing.T, f *firstDispatchFixture, u *pb.AttachmentUpload) domain.ImageUpload {
	t.Helper()
	var upload domain.ImageUpload
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		_, value, err := imageUploadRecord(tx, domain.ID(u.Attachment.Id))
		upload = value
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return upload
}

func TestOpenCodeImageModelAcceptsAttachmentAtCreation(t *testing.T) {
	f, u, operation := openCodeImageUpload(t, domain.OpenCode)
	if err := createWithImage(f, u, operation, "Describe the attached image"); err != nil {
		t.Fatal("OpenCode image-capable route rejected", err)
	}
	if upload := uploadState(t, f, u); upload.State != domain.ImageClaimed || upload.InputID == "" || len(upload.Owners) != 1 {
		t.Fatal("accepted OpenCode image was not claimed", upload)
	}
}

func TestOpenCodeImageWithoutTextOrImageModelStaysUnclaimed(t *testing.T) {
	t.Run("image-only", func(t *testing.T) {
		f, u, operation := openCodeImageUpload(t, domain.OpenCode)
		if err := createWithImage(f, u, operation, ""); connect.CodeOf(err) != connect.CodeUnimplemented {
			t.Fatal("image-only OpenCode input accepted", err)
		}
		if upload := uploadState(t, f, u); upload.State != domain.ImageReady || len(upload.Owners) != 0 || upload.InputID != "" {
			t.Fatal("rejected input claimed the upload", upload)
		}
	})
	t.Run("text-only model", func(t *testing.T) {
		f, u, operation := openCodeImageUpload(t, domain.OpenCode)
		_, err := f.service.Store.Mutate(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), domain.NewID(), "fixture.text-only-model", nil, func(tx *store.Tx) (any, error) {
			row, err := tx.Get(domain.AgentKind, domain.ID(f.agent.Id))
			if err != nil {
				return nil, err
			}
			agent, err := store.Decode[domain.Agent](row)
			if err != nil {
				return nil, err
			}
			agent.Routes[0].Model.InputModalities = []string{"text"}
			return tx.Put(domain.AgentKind, row.ID, row.Revision, "", "", agent)
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := createWithImage(f, u, operation, "Describe"); connect.CodeOf(err) != connect.CodeUnimplemented {
			t.Fatal("OpenCode model without image modality accepted", err)
		}
		if upload := uploadState(t, f, u); upload.State != domain.ImageReady || len(upload.Owners) != 0 || upload.InputID != "" {
			t.Fatal("rejected input claimed the upload", upload)
		}
	})
}

func TestOpenCodeImageRouteEnforcesCombinedByteBound(t *testing.T) {
	f, _, raw := imageRPCFixture(t, domain.OpenCode)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	ref := func(size uint64) domain.ImageAttachment {
		return domain.ImageAttachment{ID: domain.NewID(), MachineID: domain.ID(f.machine.Id), MediaType: domain.ImagePNG, ByteLength: size, SHA256: "0000000000000000000000000000000000000000000000000000000000000000"}
	}
	_ = raw
	within := []domain.ImageAttachment{ref(domain.MaxOpenCodeInputImagesBytes / 2), ref(domain.MaxOpenCodeInputImagesBytes / 2)}
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		return checkImageRoute(tx, domain.ID(f.agent.Id), domain.ID(f.machine.Id), "describe", within)
	}); err != nil {
		t.Fatal("bounded OpenCode images rejected", err)
	}
	over := []domain.ImageAttachment{ref(domain.MaxOpenCodeInputImagesBytes), ref(1)}
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		return checkImageRoute(tx, domain.ID(f.agent.Id), domain.ID(f.machine.Id), "describe", over)
	}); domain.SafeError(err).Code != domain.InvalidArgument {
		t.Fatal("OpenCode images beyond the adapter bound admitted", err)
	}
}

func TestOpenCodeFollowUpImageUsesFrozenSnapshotAndRejectsSidechat(t *testing.T) {
	f, _, raw := imageRPCFixture(t, domain.OpenCode)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	ref := domain.ImageAttachment{ID: domain.NewID(), MachineID: domain.ID(f.machine.Id), MediaType: domain.ImagePNG, ByteLength: uint64(len(raw)), SHA256: "0000000000000000000000000000000000000000000000000000000000000000"}
	session := func(declared bool, harness domain.Harness, sidechat domain.SidechatPolicy) domain.Session {
		return domain.Session{AgentID: domain.ID(f.agent.Id), MachineID: domain.ID(f.machine.Id), InitialExecution: &domain.InitialExecution{Configuration: domain.ExecutionConfiguration{Harness: harness, ImageInputDeclared: declared, SidechatPolicy: sidechat}}}
	}
	check := func(value domain.Session, prompt string) error {
		return f.service.Store.Read(ctx, func(tx *store.Tx) error {
			return checkSessionImageRoute(tx, value, prompt, []domain.ImageAttachment{ref})
		})
	}
	if err := check(session(true, domain.OpenCode, ""), "describe"); err != nil {
		t.Fatal("declared OpenCode follow-up rejected", err)
	}
	for name, rejected := range map[string]error{
		"undeclared": check(session(false, domain.OpenCode, ""), "describe"),
		"image-only": check(session(true, domain.OpenCode, ""), " "),
		"claude":     check(session(true, domain.ClaudeCode, ""), "describe"),
		"grok":       check(session(true, domain.GrokBuild, ""), "describe"),
		"sidechat":   check(session(true, domain.OpenCode, domain.SidechatPolicy("read-only")), "describe"),
	} {
		if domain.SafeError(rejected).Code != domain.Unsupported {
			t.Fatalf("%s follow-up admitted: %v", name, rejected)
		}
	}
}

func createOpenCodeImageSession(t *testing.T) (*firstDispatchFixture, *pb.SessionChange) {
	t.Helper()
	f, u, operation := openCodeImageUpload(t, domain.OpenCode)
	selection := f.selection
	selection.Prompt = "Describe the attached image"
	document, _ := json.Marshal(selection)
	reply, err := sessionClient(f.accountFixture).CreateSession(context.Background(), ownerRequest(f.identity, &pb.CreateSessionRequest{RequestId: string(operation), DocumentJson: document, Attachments: []*pb.ImageAttachment{u.Attachment}}))
	if err != nil {
		t.Fatal("OpenCode image session rejected", err)
	}
	return f, reply.Msg.Change
}

func queuedInput(t *testing.T, f *firstDispatchFixture, id string) domain.QueuedInput {
	t.Helper()
	var value domain.QueuedInput
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		row, err := tx.Get(domain.QueueKind, domain.ID(id))
		if err != nil {
			return err
		}
		value, err = store.Decode[domain.QueuedInput](row)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestOpenCodeQueuedImageEditKeepsNonblankText(t *testing.T) {
	f, change := createOpenCodeImageSession(t)
	original := queuedInput(t, f, change.Input.Id)
	if len(original.Attachments) != 1 {
		t.Fatal("fixture lost its image reference")
	}
	ctx := context.Background()
	edit := func(input *pb.Resource, prompt string) (*connect.Response[pb.EditQueuedInputResponse], error) {
		return sessionClient(f.accountFixture).EditQueuedInput(ctx, ownerRequest(f.identity, &pb.EditQueuedInputRequest{Mutation: acctMutation(input, domain.NewID()), SessionId: change.Session.Id, Prompt: prompt}))
	}
	for name, prompt := range map[string]string{"empty": "", "whitespace": " \n\t "} {
		if _, err := edit(change.Input, prompt); connect.CodeOf(err) != connect.CodeUnimplemented {
			t.Fatalf("%s prompt kept the images: %v", name, err)
		}
		if current := queuedInput(t, f, change.Input.Id); current.Prompt != original.Prompt || current.ContentRevision != original.ContentRevision || len(current.Attachments) != 1 {
			t.Fatalf("rejected %s edit changed the queued input", name)
		}
	}
	changed, err := edit(change.Input, "A new caption")
	if err != nil {
		t.Fatal("nonblank edit rejected", err)
	}
	if current := queuedInput(t, f, change.Input.Id); current.Prompt != "A new caption" || len(current.Attachments) != 1 || current.Attachments[0] != original.Attachments[0] {
		t.Fatal("edit lost the immutable image reference")
	}
	removed, err := sessionClient(f.accountFixture).RemoveQueuedInput(ctx, ownerRequest(f.identity, &pb.RemoveQueuedInputRequest{Mutation: acctMutation(changed.Msg.Change.Input, domain.NewID()), SessionId: change.Session.Id}))
	if err != nil || queuedInput(t, f, change.Input.Id).Delivery != domain.InputRemoved || removed == nil {
		t.Fatal("removal of an image message was rejected", err)
	}
}
