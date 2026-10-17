// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type dynamicToolPublication struct {
	ID   domain.ID
	Tool domain.DynamicTool
}

func (c *CodexEventPublisher) publishDynamicTool(ctx context.Context, event codex.Event) error {
	if !c.publisher.input.NativeDynamicTools || event.TurnID != c.turn || event.DynamicTool == nil || event.DynamicTool.NativeID != event.ItemID {
		return publicationUncertain()
	}
	if (event.Late || c.finished) && event.DynamicTool.Stage != domain.DynamicCompleted && event.DynamicTool.Stage != domain.DynamicResolved {
		return publicationUncertain()
	}
	retained, known := c.dynamicTools[event.ItemID]
	if !known {
		if event.Late || c.finished || c.itemKnown(event.ItemID) || c.itemLimitReached() || event.DynamicTool.Stage != domain.DynamicStarted && event.DynamicTool.Stage != domain.DynamicCompleted && event.DynamicTool.Stage != domain.DynamicRequested {
			return publicationUncertain()
		}
		retained.ID = domain.NewID()
	}
	update := *event.DynamicTool
	update.ID = retained.ID
	var prior *domain.DynamicTool
	if known {
		prior = &retained.Tool
	}
	next, err := domain.ApplyDynamicTool(prior, update)
	if err != nil {
		return publicationUncertain()
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionDynamicToolObserved, DynamicTool: &update}); err != nil {
		return err
	}
	retained.Tool = next
	c.dynamicTools[event.ItemID] = retained
	return nil
}
func (c *CodexEventPublisher) dynamicToolsSettled() bool {
	for _, retained := range c.dynamicTools {
		if !retained.Tool.Settled() {
			return false
		}
	}
	return true
}

// Only immutable original ownership and reply intent enter this journal.
// Native arguments, locators and outputs remain in private native history.
type dynamicReplyJournal struct {
	Backend          dynamicBackendKind          `json:"backend,omitempty"`
	WireID           json.RawMessage             `json:"wire_id"`
	Version          uint32                      `json:"version"`
	ServerID         domain.ID                   `json:"server_id"`
	DeviceID         domain.ID                   `json:"device_id"`
	MachineID        domain.ID                   `json:"machine_id"`
	InstanceID       domain.ID                   `json:"instance_id"`
	JobID            domain.ID                   `json:"job_id"`
	Revision         uint64                      `json:"assignment_revision"`
	AssignmentDigest string                      `json:"assignment_digest"`
	SessionID        domain.ID                   `json:"session_id"`
	ExecutionID      domain.ID                   `json:"execution_id"`
	ThreadID         domain.ID                   `json:"thread_id"`
	TurnID           domain.ID                   `json:"turn_id"`
	CallID           string                      `json:"call_id"`
	Request          domain.DynamicToolRequest   `json:"request"`
	ClaimID          domain.ID                   `json:"claim_id"`
	State            responseJournalState        `json:"state"`
	Delivery         domain.DynamicReplyDelivery `json:"delivery,omitempty"`
}
type nativeDynamicResponder interface {
	DynamicRequestID(context.Context, domain.ID) (json.RawMessage, error)
	ReplyDynamicUnavailable(context.Context, domain.ID, domain.ID, domain.ID, func() error) (domain.DynamicToolReply, error)
}

type dynamicBackendKind string

const openRouterImageDynamicBackend dynamicBackendKind = "openrouter-image"

type dynamicReplySender func(context.Context, domain.ID, domain.ID, domain.ID, func() error) (domain.DynamicToolReply, error)

func (c *CodexEventPublisher) deliverDynamicUnavailable(ctx, publicationCtx context.Context, event codex.Event, client nativeDynamicResponder) error {
	return c.deliverDynamicReply(ctx, publicationCtx, event, client, "", client.ReplyDynamicUnavailable)
}

func (c *CodexEventPublisher) deliverOpenRouterImage(ctx, publicationCtx context.Context, event codex.Event, client *codex.Client, backend func(context.Context, json.RawMessage) (string, error)) error {
	return c.deliverDynamicReply(ctx, publicationCtx, event, client, openRouterImageDynamicBackend, func(ctx context.Context, token, turn, claim domain.ID, commit func() error) (domain.DynamicToolReply, error) {
		return client.ReplyDynamicImage(ctx, token, turn, claim, commit, backend)
	})
}

func (c *CodexEventPublisher) deliverDynamicReply(ctx, publicationCtx context.Context, event codex.Event, client nativeDynamicResponder, backend dynamicBackendKind, send dynamicReplySender) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return domain.SafeError(ctx.Err())
	}
	if c.publisher == nil || c.blocked || c.finished || c.publisher.input.Configuration.SidechatPolicy != "" || event.DynamicTool == nil || event.DynamicTool.Stage != domain.DynamicRequested || event.DynamicTool.Request == nil || event.TurnID != c.turn {
		return publicationUncertain()
	}
	retained, known := c.dynamicTools[event.ItemID]
	if !known || retained.Tool.Request == nil || retained.Tool.Reply != nil || retained.Tool.Resolved || retained.Tool.Request.Token != event.DynamicTool.Request.Token {
		return publicationUncertain()
	}
	original := *retained.Tool.Request
	if backend != "" && (backend != openRouterImageDynamicBackend || !c.publisher.input.NativeOpenRouterTools || original.Identity.Namespace == nil || *original.Identity.Namespace != "delidev_image" || original.Identity.Tool != "imagegen") {
		return publicationUncertain()
	}
	wireID, err := client.DynamicRequestID(ctx, original.Token)
	if err != nil {
		return err
	}
	config := c.publisher.config
	directory := filepath.Join(config.Root, "jobs", string(c.publisher.job), "dynamic-replies")
	if security.PrivateDir(directory) != nil {
		return publicationUncertain()
	}
	// Exclusive creation fences restarts and competing delivery owners before
	// any reply claim. An existing attempt requires inspection, never resend.
	byCall := filepath.Join(directory, "by-call")
	byRequest := filepath.Join(directory, "by-request")
	if security.PrivateDir(byCall) != nil || security.PrivateDir(byRequest) != nil {
		return publicationUncertain()
	}
	requestAttempt := filepath.Join(byRequest, dynamicJournalKey(string(original.IDKind)+":"+original.IDValue)+".json")
	attempt := filepath.Join(byCall, dynamicJournalKey(event.ItemID))
	path := filepath.Join(attempt, "reply.json")
	journal := dynamicReplyJournal{Backend: backend, WireID: wireID, Version: 1, ServerID: config.Credential.ServerID, DeviceID: config.Credential.DeviceID, MachineID: config.Credential.MachineID, InstanceID: config.Instance, JobID: c.publisher.job, Revision: c.publisher.state.Revision, AssignmentDigest: c.publisher.state.AssignmentDigest, SessionID: c.publisher.input.SessionID, ExecutionID: c.publisher.execution, ThreadID: c.thread, TurnID: c.turn, CallID: event.ItemID, Request: original, ClaimID: domain.NewID(), State: responsePrepared}
	// Publish a complete immutable owner document as the first uniqueness claim.
	// A crash before the call journal exists remains inspectable and fences send.
	if err := claimDynamicReplyOwner(requestAttempt, journal); err != nil {
		return c.recoverDynamicReply(publicationCtx, event, retained, journal, requestAttempt, attempt, path)
	}
	if err := security.CreatePrivateDirExclusive(attempt); err != nil {
		return c.recoverDynamicReply(publicationCtx, event, retained, journal, requestAttempt, attempt, path)
	}
	if security.SyncParent(attempt) != nil {
		return publicationUncertain()
	}
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	replyBudget := 15 * time.Second
	if backend != "" {
		replyBudget = 15 * time.Minute
	}
	bounded, cancel := context.WithTimeout(ctx, replyBudget)
	reply, sendErr := send(bounded, original.Token, c.turn, journal.ClaimID, func() error {
		journal.State = responseSendIntent
		return writeJSON(path, journal)
	})
	cancel()
	if reply.Token != original.Token || reply.ClaimID != journal.ClaimID || reply.Unavailable != (backend == "") {
		return publicationUncertain()
	}
	journal.State = responseObserved
	journal.Delivery = reply.Delivery
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	if _, err := inspectDynamicReplyJournal(path, journal); err != nil {
		return err
	}
	return c.publishDynamicReply(publicationCtx, event, retained, journal, reply, sendErr)
}

// Reconcile only a fully observed journal entry. Prepared or send-intent
// entries retain uncertainty and never authorize another native send.
func (c *CodexEventPublisher) recoverDynamicReply(publicationCtx context.Context, event codex.Event, retained dynamicToolPublication, expected dynamicReplyJournal, requestAttempt, attempt, path string) error {
	if security.RegularPrivate(requestAttempt) != nil {
		return publicationUncertain()
	}
	raw, err := security.ReadPrivate(requestAttempt, 32<<10)
	var original dynamicReplyJournal
	if err != nil || domain.Decode(raw, &original) != nil {
		return publicationUncertain()
	}
	expected.ClaimID = original.ClaimID
	owner, err := inspectDynamicReplyJournal(requestAttempt, expected)
	if err != nil || owner.State != responsePrepared {
		return publicationUncertain()
	}
	if c.publisher.config.Logger != nil {
		c.publisher.config.Logger.InfoContext(publicationCtx, "dynamic_tool_original_reply_owner_inspected", "job_id", owner.JobID, "execution_id", owner.ExecutionID, "claim_id", owner.ClaimID, "state", owner.State)
	}
	// Missing or partial later journals never permit completing a restored send.
	if security.CheckPrivateDir(attempt) != nil {
		return publicationUncertain()
	}
	verified, err := inspectDynamicReplyJournal(path, expected)
	if err != nil || verified.State != responseObserved {
		return publicationUncertain()
	}
	return c.publishDynamicReply(publicationCtx, event, retained, verified, domain.DynamicToolReply{Token: verified.Request.Token, ClaimID: verified.ClaimID, Delivery: verified.Delivery, Unavailable: verified.Backend == ""}, nil)
}

func (c *CodexEventPublisher) publishDynamicReply(publicationCtx context.Context, event codex.Event, retained dynamicToolPublication, journal dynamicReplyJournal, reply domain.DynamicToolReply, sendErr error) error {
	update := domain.DynamicToolUpdate{ID: retained.ID, NativeID: event.ItemID, Stage: domain.DynamicReplied, Reply: &reply}
	next, err := domain.ApplyDynamicTool(&retained.Tool, update)
	if err != nil {
		return publicationUncertain()
	}
	bounded, cancel := context.WithTimeout(publicationCtx, 15*time.Second)
	err = c.publish(bounded, domain.ExecutionEvent{Kind: domain.ExecutionDynamicToolObserved, DynamicTool: &update})
	cancel()
	if err != nil {
		return err
	}
	retained.Tool = next
	c.dynamicTools[event.ItemID] = retained
	if c.publisher != nil && c.publisher.config.Logger != nil {
		c.publisher.config.Logger.InfoContext(publicationCtx, "dynamic_tool_reply_retained", "job_id", journal.JobID, "execution_id", journal.ExecutionID, "request_token", journal.Request.Token, "claim_id", journal.ClaimID, "delivery", reply.Delivery)
	}
	if reply.Delivery == domain.DynamicUncertain {
		return publicationUncertain()
	}
	if sendErr != nil {
		return sendErr
	}
	return nil
}

func dynamicJournalKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Inspect only the retained original attempt. Restored intent is evidence and
// cannot invoke ReplyDynamicUnavailable or create another attempt directory.
func inspectDynamicReplyJournal(path string, expected dynamicReplyJournal) (dynamicReplyJournal, error) {
	raw, err := security.ReadPrivate(path, 32<<10)
	if err != nil {
		return dynamicReplyJournal{}, publicationUncertain()
	}
	var saved dynamicReplyJournal
	if domain.Decode(raw, &saved) != nil || saved.Version != 1 || saved.Backend != expected.Backend || (saved.Backend != "" && saved.Backend != openRouterImageDynamicBackend) || !bytesEqualDynamicWire(saved.WireID, expected.WireID) || !dynamicWireMatchesRequest(saved.WireID, saved.Request) || saved.ServerID != expected.ServerID || saved.DeviceID != expected.DeviceID || saved.MachineID != expected.MachineID || saved.InstanceID != expected.InstanceID || saved.JobID != expected.JobID || saved.Revision != expected.Revision || saved.AssignmentDigest != expected.AssignmentDigest || saved.SessionID != expected.SessionID || saved.ExecutionID != expected.ExecutionID || saved.ThreadID != expected.ThreadID || saved.TurnID != expected.TurnID || saved.CallID != expected.CallID || saved.ClaimID != expected.ClaimID || saved.Request.Token != expected.Request.Token || saved.Request.IDKind != expected.Request.IDKind || saved.Request.IDValue != expected.Request.IDValue || !reflect.DeepEqual(saved.Request.Identity, expected.Request.Identity) || saved.Request.Validate() != nil || saved.ClaimID.Validate() != nil {
		return dynamicReplyJournal{}, publicationUncertain()
	}
	if saved.State != responsePrepared && saved.State != responseSendIntent && saved.State != responseObserved {
		return dynamicReplyJournal{}, publicationUncertain()
	}
	if saved.State == responseObserved && saved.Delivery != domain.DynamicNotSent && saved.Delivery != domain.DynamicTransmitted && saved.Delivery != domain.DynamicUncertain || saved.State != responseObserved && saved.Delivery != "" {
		return dynamicReplyJournal{}, publicationUncertain()
	}
	return saved, nil
}

// claimDynamicReplyOwner atomically publishes durable metadata without replacing
// an earlier owner. An unpublished temporary document grants no send authority.
func claimDynamicReplyOwner(path string, journal dynamicReplyJournal) error {
	temporary := filepath.Join(filepath.Dir(path), ".prepared-"+string(domain.NewID())+".json")
	defer os.Remove(temporary)
	if err := writeJSON(temporary, journal); err != nil {
		return err
	}
	return security.PublishImmutable(temporary, path)
}
func bytesEqualDynamicWire(a, b json.RawMessage) bool {
	return len(a) != 0 && string(a) == string(b)
}

func dynamicWireMatchesRequest(raw json.RawMessage, request domain.DynamicToolRequest) bool {
	if request.IDKind == domain.InteractionTextID {
		var id string
		return domain.DecodeBounded(raw, &id, 256) == nil && id == request.IDValue
	}
	var id json.Number
	if domain.DecodeBounded(raw, &id, 256) != nil {
		return false
	}
	value, err := strconv.ParseInt(string(id), 10, 64)
	return err == nil && strconv.FormatInt(value, 10) == request.IDValue
}
