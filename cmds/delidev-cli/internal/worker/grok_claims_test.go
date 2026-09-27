package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func newGrokClaimsFixture(t *testing.T) (*ExecutionPublisher, *grokClaimJournal, []grokClaim) {
	t.Helper()
	f := newCheckpointFixture(t)
	f.input.Configuration.Harness = domain.GrokBuild
	f.input.Configuration.Effort = ""
	f.input.Configuration.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
	f.input.ConfigurationDigest, _ = f.input.Configuration.Digest()
	f.input.Installation.Harness, f.input.Installation.Version = domain.GrokBuild, grok.SupportedVersion
	f.input.Installation.Protocol = &domain.ProtocolObservation{Protocol: domain.GrokACP, State: domain.ProtocolVerified}
	f.input.Input.Mode = domain.ExecuteMode
	f.job.Input, _ = json.Marshal(f.input)
	f.job.InstanceID, f.job.AcceptedAt = domain.NewID(), time.Now().UTC()
	raw, _ := json.Marshal(f.job)
	credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: "http://127.0.0.1:1", ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), MachineID: f.input.MachineID, Token: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	p, err := OpenExecutionPublisher(PublicationConfig{Root: f.root, Credential: credential, Instance: f.job.InstanceID, Assignment: &pb.Resource{Id: string(f.jobID), Revision: 2, Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, SessionId: string(f.input.SessionID), DocumentJson: raw}, Client: delidevv1connect.NewWorkerServiceClient(http.DefaultClient, credential.Endpoint)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	journal, err := openGrokClaims(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	creation := grok.CreationClaim{Phase: grok.ClaimCreation, RequestID: f.input.ThreadRequestID, ProductSessionID: f.input.SessionID, BodyDigest: strings.Repeat("ab", 32), ConfigurationDigest: strings.Repeat("cd", 32)}
	bound := creation
	bound.Phase, bound.NativeSessionID = grok.BindCreation, domain.NewID()
	digest, err := grok.TextInputClaimDigest(bound.NativeSessionID, f.input.Input.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	input := grok.InputClaim{Phase: grok.ClaimInput, RequestID: f.input.TurnRequestID, ProductSessionID: f.input.SessionID, NativeSessionID: bound.NativeSessionID, BodyDigest: digest}
	running := input
	running.Phase, running.NativePromptID = grok.BindInput, "e5833c4a-d764-4428-8bd8-6c2968a34b1b"
	return p, journal, []grokClaim{{Creation: &creation}, {Creation: &bound}, {Input: &input}, {Input: &running}}
}

func recordGrokClaim(ctx context.Context, journal *grokClaimJournal, c grokClaim) error {
	if c.Creation != nil {
		return journal.Creation(ctx, *c.Creation)
	}
	return journal.Input(ctx, *c.Input)
}

func TestGrokClaimsRetainOriginalOperationsWithoutReplay(t *testing.T) {
	p, journal, claims := newGrokClaimsFixture(t)
	ctx := context.Background()
	for i, claim := range claims {
		if err := recordGrokClaim(ctx, journal, claim); err != nil {
			t.Fatal(err)
		}
		retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
		if err != nil || len(retained) != i+1 || !reflect.DeepEqual(retained[i], claim) {
			t.Fatal("original claim was not synchronized", err)
		}
		if err := recordGrokClaim(ctx, journal, claim); err == nil {
			t.Fatal("duplicate claim regained send authority")
		}
	}
	raw, err := security.ReadPrivate(journal.path, maxGrokClaimBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{p.input.Input.Prompt, p.config.Credential.Token, p.config.Credential.Endpoint, p.config.Root} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("private content entered claim metadata")
		}
	}
	if _, err := openGrokClaims(p); err == nil {
		t.Fatal("two writers acquired original assignment")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if journal.prompt != "" {
		t.Fatal("closed journal retained original prompt")
	}
	if err := journal.Close(); err != nil {
		t.Fatal("closure was not idempotent", err)
	}
	if _, err := openGrokClaims(p); err == nil {
		t.Fatal("retained journal acquired a new native send lease")
	}
	if _, err := readGrokClaims(p.config.Root, journal.state.Reference); err != nil {
		t.Fatal("closure discarded original evidence", err)
	}
}

func TestGrokClaimsRejectChangedOrOutOfOrderOwnership(t *testing.T) {
	p, journal, claims := newGrokClaimsFixture(t)
	ctx := context.Background()
	for _, claim := range claims[1:] {
		if recordGrokClaim(ctx, journal, claim) == nil {
			t.Fatal("claim preceded its original boundary")
		}
	}
	if journal.claim(ctx, grokClaim{Creation: claims[0].Creation, Input: claims[2].Input}) == nil || journal.claim(ctx, grokClaim{}) == nil {
		t.Fatal("ambiguous claim accepted")
	}
	for i, claim := range claims {
		var changes []grokClaim
		if claim.Creation != nil {
			for _, mutate := range []func(*grok.CreationClaim){
				func(c *grok.CreationClaim) { c.RequestID = domain.NewID() },
				func(c *grok.CreationClaim) { c.ProductSessionID = domain.NewID() },
				func(c *grok.CreationClaim) { c.Phase = grok.ClaimCreation; c.NativeSessionID = domain.NewID() },
				func(c *grok.CreationClaim) { c.BodyDigest = strings.Repeat("AB", 32) },
			} {
				copy := *claim.Creation
				mutate(&copy)
				changes = append(changes, grokClaim{Creation: &copy})
			}
			if i == 1 {
				for _, mutate := range []func(*grok.CreationClaim){
					func(c *grok.CreationClaim) { c.ConfigurationDigest = strings.Repeat("ef", 32) },
					func(c *grok.CreationClaim) { c.BodyDigest = strings.Repeat("ef", 32) },
				} {
					copy := *claim.Creation
					mutate(&copy)
					changes = append(changes, grokClaim{Creation: &copy})
				}
			}
		} else {
			for _, mutate := range []func(*grok.InputClaim){
				func(c *grok.InputClaim) { c.RequestID = domain.NewID() },
				func(c *grok.InputClaim) { c.ProductSessionID = domain.NewID() },
				func(c *grok.InputClaim) { c.NativeSessionID = domain.NewID() },
				func(c *grok.InputClaim) { c.NativePromptID = string(domain.NewID()) },
				func(c *grok.InputClaim) { c.BodyDigest = strings.Repeat("AB", 32) },
			} {
				copy := *claim.Input
				mutate(&copy)
				changes = append(changes, grokClaim{Input: &copy})
			}
		}
		before, _ := security.ReadPrivate(journal.path, maxGrokClaimBytes)
		for _, changed := range changes {
			if recordGrokClaim(ctx, journal, changed) == nil {
				t.Fatal("foreign operation accepted")
			}
			after, _ := security.ReadPrivate(journal.path, maxGrokClaimBytes)
			if !bytes.Equal(before, after) {
				t.Fatal("invalid ownership changed original journal")
			}
		}
		if err := recordGrokClaim(ctx, journal, claim); err != nil {
			t.Fatal(err)
		}
	}
	ref := journal.state.Reference
	ref.ConnectionID = domain.NewID()
	if _, err := readGrokClaims(p.config.Root, ref); err == nil {
		t.Fatal("original evidence rebound to replacement account connection")
	}
}

func TestGrokClaimsRejectChangedInputAndRetainedJournal(t *testing.T) {
	for _, mode := range []string{"input", "changed", "missing", "symlink", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			p, journal, claims := newGrokClaimsFixture(t)
			ctx := context.Background()
			for _, claim := range claims[:2] {
				if err := recordGrokClaim(ctx, journal, claim); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := security.ReadPrivate(journal.path, maxGrokClaimBytes)
			input := *claims[2].Input
			switch mode {
			case "input":
				input.BodyDigest, _ = grok.TextInputClaimDigest(input.NativeSessionID, p.input.Input.Prompt+" replacement")
			case "changed":
				if err := security.WriteAtomic(journal.path, append(before, '\n')); err != nil {
					t.Fatal(err)
				}
			case "missing", "symlink":
				if err := os.Remove(journal.path); err != nil {
					t.Fatal(err)
				}
				if mode == "symlink" {
					if err := security.WriteAtomic(journal.path+".retained", before); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(journal.path+".retained", journal.path); err != nil {
						t.Skip("symlink creation unavailable")
					}
				}
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			if err := journal.Input(ctx, input); err == nil || len(journal.state.Claims) != 2 {
				t.Fatal("unsafe input gained claim authority", err)
			}
			if mode != "canceled" {
				if !journal.failed || journal.Input(context.Background(), *claims[2].Input) == nil {
					t.Fatal("uncertain journal allowed a replacement attempt")
				}
			}
		})
	}
}

func TestGrokClaimsConcurrentAttemptIsSingleUse(t *testing.T) {
	_, journal, claims := newGrokClaimsFixture(t)
	var successes atomic.Uint32
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			if journal.Creation(context.Background(), *claims[0].Creation) == nil {
				successes.Add(1)
			}
		})
	}
	group.Wait()
	if successes.Load() != 1 || len(journal.state.Claims) != 1 {
		t.Fatal("concurrent creation regained send authority")
	}
}

func TestGrokClaimsReadRejectsNoncanonicalOrForeignRecords(t *testing.T) {
	for _, mode := range []string{"whitespace", "alias", "unknown", "null", "reordered", "digest", "bound", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			p, journal, claims := newGrokClaimsFixture(t)
			for _, claim := range claims {
				if err := recordGrokClaim(context.Background(), journal, claim); err != nil {
					t.Fatal(err)
				}
			}
			raw := append([]byte(nil), journal.saved...)
			switch mode {
			case "whitespace":
				raw = append(raw, '\n')
			case "alias":
				raw = bytes.Replace(raw, []byte(`"native_prompt_id"`), []byte(`"Native_Prompt_ID"`), 1)
			case "unknown":
				raw = append([]byte(`{"unrecognized":true,`), raw[1:]...)
			case "null":
				raw, _ = json.Marshal(grokClaimState{Reference: journal.state.Reference})
			case "reordered", "digest", "bound":
				var state grokClaimState
				_ = json.Unmarshal(raw, &state)
				switch mode {
				case "reordered":
					state.Claims[0], state.Claims[1] = state.Claims[1], state.Claims[0]
				case "digest":
					state.Claims[3].Input.BodyDigest = strings.Repeat("ef", 32)
				case "bound":
					state.Claims[2].Input.NativeSessionID = domain.NewID()
				}
				raw, _ = json.Marshal(state)
			case "oversized":
				raw = bytes.Repeat([]byte("x"), maxGrokClaimBytes+1)
			}
			if err := security.WriteAtomic(journal.path, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := readGrokClaims(p.config.Root, journal.state.Reference); err == nil {
				t.Fatal("invalid retained claims became original evidence")
			}
		})
	}
}

func TestGrokClaimsFailedPersistenceAndClosureRetainUncertainty(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("controlled Unix owner write-permission failure")
	}
	p, journal, claims := newGrokClaimsFixture(t)
	directory := filepath.Dir(journal.path)
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(directory, 0700) }()
	if journal.Creation(context.Background(), *claims[0].Creation) == nil || !journal.failed || len(journal.state.Claims) != 1 {
		t.Fatal("failed synchronization reopened original send boundary")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if journal.Creation(context.Background(), *claims[0].Creation) == nil {
		t.Fatal("restored filesystem permission allowed native retry")
	}
	release, calls := journal.release, 0
	journal.release = func() error {
		calls++
		_ = release()
		return grokClaimUncertain()
	}
	if journal.Close() == nil || journal.Close() == nil || calls != 1 {
		t.Fatal("closure erased prior cleanup uncertainty")
	}
	if _, err := openGrokClaims(p); err == nil {
		t.Fatal("empty retained initialization permitted replacement execution")
	}
	retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
	if err != nil || len(retained) != 0 {
		t.Fatal("failed claim manufactured retained native authority", err)
	}
}

func TestGrokClaimsRequireOriginalPublisherAuthority(t *testing.T) {
	if _, err := openGrokClaims(nil); err == nil {
		t.Fatal("missing original publisher granted claim authority")
	}
	for _, mutate := range []func(*ExecutionPublisher){
		func(p *ExecutionPublisher) { p.state.LastSequence = 1 },
		func(p *ExecutionPublisher) { p.state.Pending = &pendingPublication{} },
		func(p *ExecutionPublisher) { p.state.InstanceID = domain.NewID() },
		func(p *ExecutionPublisher) { p.state.ServerID = domain.NewID() },
		func(p *ExecutionPublisher) { p.state.DeviceID = domain.NewID() },
		func(p *ExecutionPublisher) { p.state.JobID = domain.NewID() },
		func(p *ExecutionPublisher) { p.state.Revision = 0 },
		func(p *ExecutionPublisher) { p.execution = domain.NewID() },
		func(p *ExecutionPublisher) { p.input.Configuration.Harness = domain.OpenCode },
		func(p *ExecutionPublisher) { p.input.Installation.Version = "0.0.0" },
		func(p *ExecutionPublisher) { p.input.ThreadRequestID = p.input.TurnRequestID },
		func(p *ExecutionPublisher) { p.input.Continuation = &domain.ExecutionContinuation{} },
	} {
		p, journal, _ := newGrokClaimsFixture(t)
		if err := journal.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(journal.path); err != nil {
			t.Fatal(err)
		}
		mutate(p)
		if opened, err := openGrokClaims(p); err == nil {
			_ = opened.Close()
			t.Fatal("foreign, consumed or invalid publisher granted a native claim")
		}
		if _, err := os.Lstat(journal.path); !os.IsNotExist(err) {
			t.Fatal("invalid publisher created native ownership state")
		}
	}
}

func TestGrokClaimsClosurePreservesOriginalInputAndNeverRepeats(t *testing.T) {
	p, journal, claims := newGrokClaimsFixture(t)
	ctx := context.Background()
	digest, _ := grok.ClosureClaimDigest(claims[3].Input.NativeSessionID)
	closure := grok.ClosureClaim{Phase: grok.ClaimClosure, RequestID: domain.NewID(), ProductSessionID: p.input.SessionID, NativeSessionID: claims[3].Input.NativeSessionID, NativePromptID: claims[3].Input.NativePromptID, BodyDigest: digest}
	if journal.Closure(ctx, closure) == nil {
		t.Fatal("closure preceded original input binding")
	}
	for _, claim := range claims {
		if err := recordGrokClaim(ctx, journal, claim); err != nil {
			t.Fatal(err)
		}
	}
	for _, change := range []func(*grok.ClosureClaim){
		func(c *grok.ClosureClaim) { c.RequestID = p.input.ThreadRequestID },
		func(c *grok.ClosureClaim) { c.RequestID = p.input.TurnRequestID },
		func(c *grok.ClosureClaim) { c.ProductSessionID = domain.NewID() },
		func(c *grok.ClosureClaim) { c.NativePromptID = "f5833c4a-d764-4428-8bd8-6c2968a34b1b" },
		func(c *grok.ClosureClaim) {
			c.NativeSessionID = domain.NewID()
			c.BodyDigest, _ = grok.ClosureClaimDigest(c.NativeSessionID)
		},
		func(c *grok.ClosureClaim) { c.BodyDigest = strings.Repeat("ef", 32) },
		func(c *grok.ClosureClaim) { c.Phase = grok.BindClosure },
	} {
		copy := closure
		change(&copy)
		if journal.Closure(ctx, copy) == nil || len(journal.state.Claims) != 4 {
			t.Fatal("foreign closure changed original evidence")
		}
	}
	if err := journal.Closure(ctx, closure); err != nil {
		t.Fatal(err)
	}
	if journal.Closure(ctx, closure) == nil {
		t.Fatal("repeated closure regained native send authority")
	}
	bound := closure
	bound.Phase = grok.BindClosure
	wrong := bound
	wrong.RequestID = domain.NewID()
	if journal.Closure(ctx, wrong) == nil {
		t.Fatal("another closure request acquired original acknowledgment")
	}
	if err := journal.Closure(ctx, bound); err != nil {
		t.Fatal(err)
	}
	if journal.Closure(ctx, bound) == nil {
		t.Fatal("closure acknowledgment repeated")
	}
	retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
	if err != nil || len(retained) != 6 || *retained[5].Closure != bound {
		t.Fatal("durable closure evidence missing", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openGrokClaims(p); err == nil {
		t.Fatal("closed native session reopened original journal")
	}
}

func TestGrokStopClaimBindsOriginalWorkerAndExcludesClosureReplay(t *testing.T) {
	p, journal, claims := newGrokClaimsFixture(t)
	native := claims[3].Input
	digest, _ := grok.ClosureClaimDigest(native.NativeSessionID)
	stop := grok.StopClaim{Version: 1, OwnerID: p.job, ProductSessionID: p.input.SessionID, InputRequestID: p.input.TurnRequestID, RequestID: domain.NewID(), NativeSessionID: native.NativeSessionID, NativePromptID: native.NativePromptID, BodyDigest: digest}
	if journal.Stop(context.Background(), stop) == nil {
		t.Fatal("unbound input acquired Stop")
	}
	for _, claim := range claims {
		if err := recordGrokClaim(context.Background(), journal, claim); err != nil {
			t.Fatal(err)
		}
	}
	for _, change := range []func(*grok.StopClaim){
		func(c *grok.StopClaim) { c.OwnerID = domain.NewID() },
		func(c *grok.StopClaim) { c.ProductSessionID = domain.NewID() },
		func(c *grok.StopClaim) { c.InputRequestID = p.input.InputID },
		func(c *grok.StopClaim) { c.RequestID = p.input.ThreadRequestID },
		func(c *grok.StopClaim) {
			c.NativeSessionID = domain.NewID()
			c.BodyDigest, _ = grok.ClosureClaimDigest(c.NativeSessionID)
		},
		func(c *grok.StopClaim) { c.NativePromptID = "f5833c4a-d764-4428-8bd8-6c2968a34b1b" },
		func(c *grok.StopClaim) { c.BodyDigest = strings.Repeat("ef", 32) },
	} {
		copy := stop
		change(&copy)
		if journal.Stop(context.Background(), copy) == nil || len(journal.state.Claims) != 4 {
			t.Fatal("foreign Stop changed journal")
		}
	}
	if err := journal.Stop(context.Background(), stop); err != nil {
		t.Fatal(err)
	}
	retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
	if err != nil || len(retained) != 5 || retained[4].Stop == nil || *retained[4].Stop != stop {
		t.Fatal("original Stop not synchronized", err)
	}
	if journal.Stop(context.Background(), stop) == nil {
		t.Fatal("Stop replay accepted")
	}
	closure := grok.ClosureClaim{Phase: grok.ClaimClosure, RequestID: domain.NewID(), ProductSessionID: stop.ProductSessionID, NativeSessionID: stop.NativeSessionID, NativePromptID: stop.NativePromptID, BodyDigest: stop.BodyDigest}
	if journal.Closure(context.Background(), closure) == nil {
		t.Fatal("stopped runtime acquired successful closure")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openGrokClaims(p); err == nil {
		t.Fatal("retained Stop acquired native send lease")
	}
}

func fileReplyClaimFixture(p *ExecutionPublisher, running *grok.InputClaim, index int) grok.FilePermissionClaim {
	digest := func(value string) string { v := sha256.Sum256([]byte(value)); return hex.EncodeToString(v[:]) }
	return grok.FilePermissionClaim{Version: 1, OwnerID: p.job, ProductSessionID: p.input.SessionID, InputRequestID: p.input.TurnRequestID, RequestID: domain.NewID(), NativeSessionID: running.NativeSessionID, NativePromptID: running.NativePromptID, ArrivalID: domain.NewID(), ToolID: fmt.Sprintf("original-tool-%d", index), RequestDigest: digest(fmt.Sprintf("s:request-%d", index)), ProposalDigest: digest(fmt.Sprintf("proposal-%d", index)), Decision: grok.AllowFileOnce, BodyDigest: digest(`{"outcome":{"outcome":"selected","optionId":"allow-once"}}`)}
}

func TestGrokFileReplyClaimsRetainOriginalOwnershipAndBound(t *testing.T) {
	p, journal, claims := newGrokClaimsFixture(t)
	ctx := context.Background()
	original := fileReplyClaimFixture(p, claims[3].Input, 0)
	if journal.FileReply(ctx, original) == nil {
		t.Fatal("file reply preceded original input binding")
	}
	for _, claim := range claims {
		if err := recordGrokClaim(ctx, journal, claim); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*grok.FilePermissionClaim){
		func(c *grok.FilePermissionClaim) { c.OwnerID = domain.NewID() },
		func(c *grok.FilePermissionClaim) { c.ProductSessionID = domain.NewID() },
		func(c *grok.FilePermissionClaim) { c.InputRequestID = domain.NewID() },
		func(c *grok.FilePermissionClaim) { c.RequestID = p.input.ThreadRequestID },
		func(c *grok.FilePermissionClaim) { c.NativeSessionID = domain.NewID() },
		func(c *grok.FilePermissionClaim) { c.NativePromptID = "e5833c4a-d764-4428-8bd8-6c2968a34b1c" },
		func(c *grok.FilePermissionClaim) { c.Decision = grok.RejectFileOnce },
		func(c *grok.FilePermissionClaim) { c.ProposalDigest = strings.Repeat("AB", 32) },
	} {
		changed := original
		mutate(&changed)
		before, _ := security.ReadPrivate(journal.path, maxGrokClaimBytes)
		if journal.FileReply(ctx, changed) == nil {
			t.Fatal("foreign file reply claimed")
		}
		after, _ := security.ReadPrivate(journal.path, maxGrokClaimBytes)
		if !bytes.Equal(before, after) {
			t.Fatal("invalid file reply changed journal")
		}
	}
	if err := journal.FileReply(ctx, original); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*grok.FilePermissionClaim){
		func(c *grok.FilePermissionClaim) { c.RequestID = original.RequestID },
		func(c *grok.FilePermissionClaim) { c.ArrivalID = original.ArrivalID },
		func(c *grok.FilePermissionClaim) { c.ToolID = original.ToolID },
		func(c *grok.FilePermissionClaim) { c.RequestDigest = original.RequestDigest },
	} {
		c := fileReplyClaimFixture(p, claims[3].Input, 1)
		mutate(&c)
		if journal.FileReply(ctx, c) == nil {
			t.Fatal("original file reply identity reused")
		}
	}
	for i := 1; i < 128; i++ {
		if err := journal.FileReply(ctx, fileReplyClaimFixture(p, claims[3].Input, i)); err != nil {
			t.Fatal("bounded original reply refused", i, err)
		}
	}
	if journal.FileReply(ctx, fileReplyClaimFixture(p, claims[3].Input, 128)) == nil {
		t.Fatal("unbounded file replies accepted")
	}
	retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
	if err != nil || len(retained) != 132 || *retained[4].FileReply != original {
		t.Fatal("original replies not retained", err)
	}
	if p.state.Pending != nil || p.state.LastSequence != 0 {
		t.Fatal("file claims granted public publication")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openGrokClaims(p); err == nil {
		t.Fatal("file replies regained send authority on reopen")
	}
}

func TestGrokFileRepliesExcludeTextStopAndClosure(t *testing.T) {
	for _, preceding := range []string{"reply", "stop", "closure"} {
		t.Run(preceding, func(t *testing.T) {
			p, journal, claims := newGrokClaimsFixture(t)
			ctx := context.Background()
			for _, claim := range claims {
				if err := recordGrokClaim(ctx, journal, claim); err != nil {
					t.Fatal(err)
				}
			}
			running := claims[3].Input
			digest, _ := grok.ClosureClaimDigest(running.NativeSessionID)
			stop := grok.StopClaim{Version: 1, OwnerID: p.job, ProductSessionID: p.input.SessionID, InputRequestID: p.input.TurnRequestID, RequestID: domain.NewID(), NativeSessionID: running.NativeSessionID, NativePromptID: running.NativePromptID, BodyDigest: digest}
			closure := grok.ClosureClaim{Phase: grok.ClaimClosure, RequestID: domain.NewID(), ProductSessionID: p.input.SessionID, NativeSessionID: running.NativeSessionID, NativePromptID: running.NativePromptID, BodyDigest: digest}
			reply := fileReplyClaimFixture(p, running, 0)
			switch preceding {
			case "reply":
				if err := journal.FileReply(ctx, reply); err != nil {
					t.Fatal(err)
				}
				if journal.Stop(ctx, stop) == nil || journal.Closure(ctx, closure) == nil {
					t.Fatal("file reply granted text-only claims")
				}
			case "stop":
				if err := journal.Stop(ctx, stop); err != nil {
					t.Fatal(err)
				}
				if journal.FileReply(ctx, reply) == nil {
					t.Fatal("stopped text granted file reply")
				}
			case "closure":
				if err := journal.Closure(ctx, closure); err != nil {
					t.Fatal(err)
				}
				if journal.FileReply(ctx, reply) == nil {
					t.Fatal("closed text granted file reply")
				}
			}
		})
	}
}

func TestGrokRememberedEditsRetainExactOriginalReplyClaim(t *testing.T) {
	p, journal, claims := newGrokClaimsFixture(t)
	ctx := context.Background()
	for _, claim := range claims {
		if err := recordGrokClaim(ctx, journal, claim); err != nil {
			t.Fatal(err)
		}
	}
	reply := fileReplyClaimFixture(p, claims[3].Input, 0)
	reply.Decision = grok.AllowFileSession
	if journal.FileReply(ctx, reply) == nil {
		t.Fatal("once-only reply digest authorized remembered edits")
	}
	digest := sha256.Sum256([]byte(`{"outcome":{"outcome":"selected","optionId":"allow-edits-session"}}`))
	reply.BodyDigest = hex.EncodeToString(digest[:])
	if err := journal.FileReply(ctx, reply); err != nil {
		t.Fatal(err)
	}
	retained, err := readGrokClaims(p.config.Root, journal.state.Reference)
	if err != nil || len(retained) != 5 || *retained[4].FileReply != reply {
		t.Fatal("remembered original decision lost durable ownership", err)
	}
	if journal.FileReply(ctx, reply) == nil {
		t.Fatal("remembered policy acquired response replay")
	}
	if p.state.Pending != nil || p.state.LastSequence != 0 {
		t.Fatal("remembered edits claimed public publication")
	}
}
