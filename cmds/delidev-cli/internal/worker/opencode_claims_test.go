package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func newOpenCodeClaimsFixture(t *testing.T) (*ExecutionPublisher, *openCodeClaimJournal, []opencode.SessionClaim) {
	t.Helper()
	return newOpenCodeClaimsFixtureMode(t, domain.ExecuteMode)
}

func newOpenCodeClaimsFixtureMode(t *testing.T, mode domain.SessionMode) (*ExecutionPublisher, *openCodeClaimJournal, []opencode.SessionClaim) {
	t.Helper()
	f := newCheckpointFixture(t)
	f.input.Configuration.Harness = domain.OpenCode
	f.input.Configuration.Effort = ""
	f.input.Configuration.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
	f.input.ConfigurationDigest, _ = f.input.Configuration.Digest()
	f.input.Installation.Harness, f.input.Installation.Version = domain.OpenCode, opencode.SupportedVersion
	f.input.Installation.Protocol = &domain.ProtocolObservation{Protocol: domain.OpenCodeHTTP, State: domain.ProtocolVerified}
	f.input.Input.Mode = mode
	f.job.Input, _ = json.Marshal(f.input)
	f.job.InstanceID, f.job.AcceptedAt = domain.NewID(), time.Now().UTC()
	raw, _ := json.Marshal(f.job)
	credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: "http://127.0.0.1:1", ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), MachineID: f.input.MachineID, Token: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	p, err := OpenExecutionPublisher(PublicationConfig{Root: f.root, Credential: credential, Instance: f.job.InstanceID, Assignment: &pb.Resource{Id: string(f.jobID), Revision: 2, Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, SessionId: string(f.input.SessionID), DocumentJson: raw}, Client: delidevv1connect.NewWorkerServiceClient(http.DefaultClient, credential.Endpoint)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	j, err := openOpenCodeClaims(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	empty := sha256.Sum256(nil)
	claims := []opencode.SessionClaim{
		{RequestID: f.input.ThreadRequestID, Kind: opencode.CreateSessionMutation, BodyDigest: strings.Repeat("ab", 32)},
		{RequestID: f.input.TurnRequestID, Kind: opencode.SubmitInputMutation, SessionID: "ses_01960dcbe1faabcdefghijklmn", MessageID: "msg_01960dcbe1faABCDEFGHIJKLMN", PartID: "prt_01960dcbe1fa1234567890ABCD", BodyDigest: strings.Repeat("cd", 32)},
		{RequestID: domain.NewID(), Kind: opencode.ReplyPermissionMutation, SessionID: "ses_01960dcbe1faabcdefghijklmn", MessageID: "msg_01960dcbe1fbABCDEFGHIJKLMN", PartID: "prt_01960dcbe1fb1234567890ABCD", InputRequestID: f.input.TurnRequestID, InteractionID: "per_01960dcbe1faabcdefghijklmn", ArrivalID: "evt_01960dcbe1faabcdefghijklmn", CallID: "private-call", BodyDigest: strings.Repeat("ef", 32)},
		{RequestID: domain.NewID(), Kind: opencode.StopOwnedRuntimeMutation, SessionID: "ses_01960dcbe1faabcdefghijklmn", MessageID: "msg_01960dcbe1faABCDEFGHIJKLMN", PartID: "prt_01960dcbe1fa1234567890ABCD", InputRequestID: f.input.TurnRequestID, BodyDigest: hex.EncodeToString(empty[:])},
	}
	requested, err := openCodeExecutionSettings(p.input.Configuration, p.input.Input.Mode, "Original native input claim")
	if err != nil {
		t.Fatal(err)
	}
	claims[1].BodyDigest, err = opencode.TextInputClaimDigest(requested.Session, claims[1].MessageID, claims[1].PartID, p.input.Input.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	recovery := claims[3]
	recovery.Kind, recovery.RequestID, recovery.StopRequestID = opencode.RecoverStoppedRuntimeMutation, domain.NewID(), claims[3].RequestID
	return p, j, append(claims, recovery)
}

func TestOpenCodeClaimsAreDurableBoundedOriginalOperations(t *testing.T) {
	p, j, claims := newOpenCodeClaimsFixture(t)
	for i, claim := range claims {
		if err := j.Claim(context.Background(), claim); err != nil {
			t.Fatal(err)
		}
		retained, err := readOpenCodeClaims(p.config.Root, j.state.Reference)
		if err != nil || len(retained) != i+1 || retained[i] != claim {
			t.Fatal("claim returned before retaining its exact original metadata")
		}
		if j.Claim(context.Background(), claim) == nil {
			t.Fatal("a repeated request regained native send authority")
		}
	}
	raw, err := security.ReadPrivate(j.path, maxOpenCodeClaimBytes)
	if err != nil || bytes.Contains(raw, []byte(p.input.Input.Prompt)) || bytes.Contains(raw, []byte(p.config.Credential.Token)) || bytes.Contains(raw, []byte(p.config.Credential.Endpoint)) || bytes.Contains(raw, []byte(p.config.Root)) {
		t.Fatal("private content or credentials entered metadata-only claims")
	}
	if _, err := openOpenCodeClaims(p); err == nil {
		t.Fatal("another live journal writer acquired the original assignment")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openOpenCodeClaims(p); err == nil {
		t.Fatal("reopening a retained journal granted fresh execution authority")
	}
	if _, err := readOpenCodeClaims(p.config.Root, j.state.Reference); err != nil {
		t.Fatal("read-only original metadata was lost on closure")
	}
}

func TestOpenCodeInputClaimMustMatchImmutablePayloadBeforeMutation(t *testing.T) {
	for _, change := range []string{"prompt", "model", "provider", "agent"} {
		t.Run(change, func(t *testing.T) {
			p, journal, claims := newOpenCodeClaimsFixture(t)
			ctx := context.Background()
			if err := journal.Claim(ctx, claims[0]); err != nil {
				t.Fatal(err)
			}
			before, _ := security.ReadPrivate(journal.path, maxOpenCodeClaimBytes)
			settings, err := openCodeExecutionSettings(p.input.Configuration, p.input.Input.Mode, "Original native input claim")
			if err != nil {
				t.Fatal(err)
			}
			prompt := p.input.Input.Prompt
			switch change {
			case "prompt":
				prompt += " changed input"
			case "model":
				settings.Session.Model = "foreign-model"
			case "provider":
				settings.Session.Provider = "foreign-provider"
			case "agent":
				settings.Session.Agent = opencode.PlanAgent
			}
			wrong := claims[1]
			wrong.BodyDigest, err = opencode.TextInputClaimDigest(settings.Session, wrong.MessageID, wrong.PartID, prompt)
			if err != nil {
				t.Fatal(err)
			}
			if journal.Claim(ctx, wrong) == nil || !journal.failed || len(journal.state.Claims) != 1 || journal.Claim(ctx, claims[1]) == nil {
				t.Fatal("changed immutable payload gained mutation or replacement authority")
			}
			after, _ := security.ReadPrivate(journal.path, maxOpenCodeClaimBytes)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected payload changed the original synchronized claims")
			}
		})
	}
}

func TestOpenCodeClaimOwnershipCannotChangeOrReorder(t *testing.T) {
	p, j, claims := newOpenCodeClaimsFixture(t)
	if j.Claim(context.Background(), claims[1]) == nil || j.Claim(context.Background(), claims[2]) == nil {
		t.Fatal("native input/reply preceded original creation")
	}
	for _, c := range claims[:2] {
		if err := j.Claim(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	for _, change := range []func(*opencode.SessionClaim){
		func(c *opencode.SessionClaim) { c.SessionID = "ses_01960dcbe1fbabcdefghijklmn" },
		func(c *opencode.SessionClaim) { c.InputRequestID = domain.NewID() },
		func(c *opencode.SessionClaim) { c.MessageID = claims[1].MessageID },
		func(c *opencode.SessionClaim) { c.PartID = claims[1].PartID },
		func(c *opencode.SessionClaim) { c.InteractionID = "que_01960dcbe1faabcdefghijklmn" },
		func(c *opencode.SessionClaim) { c.BodyDigest = "AB" + c.BodyDigest[2:] },
		func(c *opencode.SessionClaim) { c.StopRequestID = domain.NewID() },
	} {
		c := claims[2]
		change(&c)
		if j.Claim(context.Background(), c) == nil {
			t.Fatal("foreign/malformed native response acquired a durable claim")
		}
	}
	if j.Claim(context.Background(), claims[4]) == nil {
		t.Fatal("cleanup recovery preceded original Stop")
	}
	if err := j.Claim(context.Background(), claims[3]); err != nil {
		t.Fatal(err)
	}
	if j.Claim(context.Background(), claims[2]) == nil {
		t.Fatal("interaction response regained authority after Stop")
	}
	for _, change := range []func(*openCodeClaimReference){
		func(r *openCodeClaimReference) { r.AccountID = domain.NewID() },
		func(r *openCodeClaimReference) { r.ConnectionID = domain.NewID() },
		func(r *openCodeClaimReference) { r.InstanceID = domain.NewID() },
		func(r *openCodeClaimReference) { r.ServerID = domain.NewID() },
		func(r *openCodeClaimReference) { r.Revision++ },
		func(r *openCodeClaimReference) { r.AssignmentDigest = strings.Repeat("00", 32) },
	} {
		ref := j.state.Reference
		change(&ref)
		if _, err := readOpenCodeClaims(p.config.Root, ref); err == nil {
			t.Fatal("changed immutable ownership adopted original native claims")
		}
	}
}

func TestOpenCodeClaimsKeepUncertaintyAfterTamperingAndCancellation(t *testing.T) {
	p, j, claims := newOpenCodeClaimsFixture(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if j.Claim(canceled, claims[0]) == nil || len(j.state.Claims) != 0 {
		t.Fatal("already canceled claim mutated the journal")
	}
	if err := j.Claim(context.Background(), claims[0]); err != nil {
		t.Fatal(err)
	}
	prior := append([]byte(nil), j.saved...)
	if err := os.WriteFile(j.path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if j.Claim(context.Background(), claims[1]) == nil || !j.failed {
		t.Fatal("replaced journal granted another native mutation")
	}
	if err := security.WriteAtomic(j.path, prior); err != nil {
		t.Fatal(err)
	}
	if j.Claim(context.Background(), claims[1]) == nil {
		t.Fatal("restoring old bytes erased observed journal uncertainty")
	}
	state := j.state
	state.Claims = append(state.Claims, claims[0])
	raw, _ := json.Marshal(state)
	if err := security.WriteAtomic(j.path, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := readOpenCodeClaims(p.config.Root, j.state.Reference); err == nil {
		t.Fatal("read-only reconciliation accepted repeated mutation identities")
	}
	alias := bytes.Replace(prior, []byte(`"reference"`), []byte(`"Reference"`), 1)
	if err := security.WriteAtomic(j.path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := readOpenCodeClaims(p.config.Root, j.state.Reference); err == nil {
		t.Fatal("read-only reconciliation normalized an altered canonical journal")
	}
}

func TestOpenCodeSimultaneousClaimsHaveOneDurableWinner(t *testing.T) {
	_, j, claims := newOpenCodeClaimsFixture(t)
	var winners atomic.Int32
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			if j.Claim(context.Background(), claims[0]) == nil {
				winners.Add(1)
			}
		})
	}
	group.Wait()
	if winners.Load() != 1 || len(j.state.Claims) != 1 {
		t.Fatal("simultaneous claims authorized more than one native mutation")
	}
}

func TestOpenCodePersistenceFailureCannotRestoreFreshAuthority(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("controlled Unix owner write-permission failure")
	}
	p, j, claims := newOpenCodeClaimsFixture(t)
	directory := filepath.Dir(j.path)
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(directory, 0700) }()
	if j.Claim(context.Background(), claims[0]) == nil || !j.failed || len(j.state.Claims) != 1 {
		t.Fatal("failed persistence did not consume its original in-memory attempt")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if j.Claim(context.Background(), claims[0]) == nil {
		t.Fatal("restored filesystem permissions granted automatic retry")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openOpenCodeClaims(p); err == nil {
		t.Fatal("empty retained initializer journal granted fresh-process authority")
	}
}

func TestOpenCodeRecoveryClaimBoundsAndOriginalStop(t *testing.T) {
	_, j, claims := newOpenCodeClaimsFixture(t)
	for _, c := range []opencode.SessionClaim{claims[0], claims[1], claims[3]} {
		if err := j.Claim(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	foreign := claims[4]
	foreign.StopRequestID = domain.NewID()
	if j.Claim(context.Background(), foreign) == nil {
		t.Fatal("recovery adopted a different Stop intent")
	}
	for range 32 {
		c := claims[4]
		c.RequestID = domain.NewID()
		if err := j.Claim(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	if j.Claim(context.Background(), claims[4]) == nil {
		t.Fatal("recovery exceeded its original native attempt bound")
	}
}

func TestOpenCodeClaimClosureRetainsOriginalUncertainty(t *testing.T) {
	_, j, claims := newOpenCodeClaimsFixture(t)
	release := j.release
	var calls int
	j.release = func() error {
		calls++
		_ = release()
		return openCodeClaimUncertain()
	}
	if j.Close() == nil || j.Close() == nil || calls != 1 || j.Claim(context.Background(), claims[0]) == nil {
		t.Fatal("repeated closure erased the original release uncertainty")
	}
}
