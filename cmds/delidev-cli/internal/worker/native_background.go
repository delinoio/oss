// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type backgroundNative interface {
	InspectBackground(context.Context) ([]domain.NativeBackgroundTerminal, error)
	ControlBackground(context.Context, domain.NativeBackgroundAction) (domain.NativeBackgroundObservation, error)
}
type nativeBackgroundController struct {
	mu        sync.Mutex
	config    *PublicationConfig
	input     domain.ExecutionJobInput
	native    backgroundNative
	inventory domain.NativeBackgroundInventory
	refreshes chan struct{}
}
type nativeBackgroundSend struct {
	Version    uint32                        `json:"version"`
	ServerID   domain.ID                     `json:"server_id"`
	DeviceID   domain.ID                     `json:"device_id"`
	InstanceID domain.ID                     `json:"instance_id"`
	Action     domain.NativeBackgroundAction `json:"action"`
	// Presence is a permanent once-only send fence, never retry permission.
	SendIntent  bool                                `json:"send_intent"`
	Observation *domain.NativeBackgroundObservation `json:"observation,omitempty"`
}

func (b *nativeBackgroundController) refresh(ctx context.Context, action *domain.NativeBackgroundObservation) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.refreshLocked(ctx, action)
}
func (b *nativeBackgroundController) refreshLocked(ctx context.Context, action *domain.NativeBackgroundObservation) error {
	bounded, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	terminals, e := b.native.InspectBackground(bounded)
	if e != nil {
		return e
	}
	i := domain.NativeBackgroundInventory{Version: 1, JobID: b.inventory.JobID, ExecutionID: b.input.ExecutionID, NativeThreadID: b.inventory.NativeThreadID, Sequence: b.inventory.Sequence + 1, Terminals: terminals, NativeEmpty: true}
	for _, t := range terminals {
		if t.Present {
			i.NativeEmpty = false
		}
	}
	if action != nil {
		action.NativeEmpty = i.NativeEmpty
	}
	p := domain.NativeBackgroundPublication{Inventory: i, Action: action}
	raw, e := json.Marshal(p)
	if e != nil {
		return e
	}
	reply, e := b.config.Client.PublishNativeBackground(bounded, authenticated(b.config.Credential, &pb.PublishNativeBackgroundRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(b.input.SessionID), ExpectedRevision: b.inventory.Sequence}, MachineId: string(b.input.MachineID), InstanceId: string(b.config.Instance), JobId: string(i.JobID), ObservationJson: raw}))
	if e != nil || reply == nil || reply.Msg == nil || reply.Msg.Session == nil || reply.Msg.Session.Id != string(b.input.SessionID) || reply.Msg.Session.Kind != pb.EntityKind_ENTITY_KIND_SESSION {
		return domain.NativeBackgroundRecovery()
	}
	var session domain.Session
	if domain.Decode(reply.Msg.Session.DocumentJson, &session) != nil || session.NativeBackground == nil || session.NativeBackground.Sequence != i.Sequence || session.NativeBackground.ExecutionID != i.ExecutionID || session.NativeBackground.Validate() != nil {
		return domain.NativeBackgroundRecovery()
	}
	b.inventory = *session.NativeBackground

	return nil
}
func (b *nativeBackgroundController) deliver(ctx context.Context, control *pb.NativeBackgroundControl) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if control == nil || control.JobId != string(b.inventory.JobID) || domain.ID(control.ActionId).Validate() != nil || control.Revision == 0 {
		return domain.NativeBackgroundRecovery()
	}
	directory := filepath.Join(b.config.Root, "jobs", control.JobId, "native-background")
	if security.PrivateDir(directory) != nil {
		return domain.NativeBackgroundRecovery()
	}
	path := filepath.Join(directory, control.ActionId+".json")
	if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
		return domain.NativeBackgroundRecovery()
	}
	claimID := domain.NewID()
	bounded, stop := context.WithTimeout(ctx, 15*time.Second)
	claimed, e := b.config.Client.ClaimNativeBackground(bounded, authenticated(b.config.Credential, &pb.ClaimNativeBackgroundRequest{Mutation: &pb.Mutation{RequestId: string(claimID), Id: control.ActionId, ExpectedRevision: control.Revision}, MachineId: string(b.input.MachineID), InstanceId: string(b.config.Instance), JobId: control.JobId}))
	stop()
	if e != nil {
		return domain.NativeBackgroundRecovery()
	}
	var a domain.NativeBackgroundAction
	if claimed == nil || claimed.Msg == nil || claimed.Msg.Action == nil || claimed.Msg.Action.Kind != pb.EntityKind_ENTITY_KIND_NATIVE_ACTION || claimed.Msg.Action.Id != control.ActionId || claimed.Msg.Action.Revision <= control.Revision || domain.Decode(claimed.Msg.Action.DocumentJson, &a) != nil || a.Validate() != nil || a.State != domain.NativeBackgroundClaimed || a.ActionID != domain.ID(control.ActionId) || a.SessionID != b.input.SessionID || a.ExecutionID != b.input.ExecutionID || a.JobID != b.inventory.JobID || a.NativeThreadID != b.inventory.NativeThreadID || a.Claim == nil || a.Claim.RequestID != claimID || a.Claim.MachineID != b.input.MachineID || a.Claim.InstanceID != b.config.Instance || a.Claim.DeviceID != b.config.Credential.DeviceID {
		return domain.NativeBackgroundRecovery()
	}
	journal := nativeBackgroundSend{Version: 1, ServerID: b.config.Credential.ServerID, DeviceID: b.config.Credential.DeviceID, InstanceID: b.config.Instance, Action: a, SendIntent: true}
	if writeJSON(path, journal) != nil {
		return domain.NativeBackgroundRecovery()
	}
	bounded, stop = context.WithTimeout(ctx, 15*time.Second)
	observed, sendErr := b.native.ControlBackground(bounded, a)
	stop()
	if sendErr != nil {
		observed.ProblemCode = domain.SafeError(sendErr).Code
		if observed.ProblemCode == domain.Canceled || observed.ProblemCode == domain.Unavailable {
			observed.ProblemCode = domain.RecoveryRequired
		}
	}
	journal.Observation = &observed
	if writeJSON(path, journal) != nil {
		return domain.NativeBackgroundRecovery()
	}
	// Reconcile by reading this original owner only. Never repeat terminate/clean.
	reportCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer stop()
	if e := b.refreshLocked(reportCtx, &observed); e != nil {
		return e
	}
	if b.config.Logger != nil {
		b.config.Logger.InfoContext(reportCtx, "native_background_control_observed", "action_id", a.ActionID, "operation", a.Operation, "acknowledged", observed.Acknowledged, "native_empty", observed.NativeEmpty, "problem_code", observed.ProblemCode)
	}
	if observed.ProblemCode != "" && observed.Delivery != domain.NativeBackgroundNotSent {
		return domain.NativeBackgroundRecovery()
	}
	return nil
}

// requestRefresh coalesces output bursts without blocking the native event reader.
// The controller alone serializes reads, delivery and retained publication.
func (b *nativeBackgroundController) requestRefresh() {
	select {
	case b.refreshes <- struct{}{}:
	default:
	}
}
func startNativeBackgroundController(ctx context.Context, controls <-chan *pb.NativeBackgroundControl, b *nativeBackgroundController) func() error {
	owned, stop := context.WithCancel(ctx)
	b.refreshes = make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var problem error
		defer func() {
			// Close only optional delivery authority. Never cancel the active turn.
			bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if e := b.closeInventory(bounded); e != nil && b.config.Logger != nil {
				b.config.Logger.WarnContext(bounded, "native_background_closure_publication_failed", "code", domain.SafeError(e).Code)
			}
			done <- problem
		}()
		// Optional discovery starts after input acceptance; it must not hold up the
		// active turn, and failure retires only this observer.
		if e := b.refresh(owned, nil); e != nil {
			problem = e
			return
		}
		for {
			select {
			case <-owned.Done():
				return
			case c, ok := <-controls:
				if !ok {
					problem = domain.NativeBackgroundRecovery()
					return
				}
				if e := b.deliver(owned, c); e != nil {
					problem = e
					return
				}
			case <-b.refreshes:
				// The next periodic read covers all accumulated output/item changes.
				// Avoid one RPC per output chunk even when native replies are fast.
			case <-ticker.C:
				if e := b.refresh(owned, nil); e != nil {
					problem = e
					return
				}
			}
		}
	}()
	var once sync.Once
	var result error
	return func() error { once.Do(func() { stop(); result = <-done }); return result }
}

func (b *nativeBackgroundController) closeInventory(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inventory.Sequence == 0 {
		return nil
	}
	i := b.inventory
	i.Sequence++
	i.OwnerLost = true
	i.PendingActionID = ""
	i.Actions = 0
	raw, _ := json.Marshal(domain.NativeBackgroundPublication{Inventory: i})
	bounded, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	_, e := b.config.Client.PublishNativeBackground(bounded, authenticated(b.config.Credential, &pb.PublishNativeBackgroundRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(b.input.SessionID), ExpectedRevision: b.inventory.Sequence}, MachineId: string(b.input.MachineID), InstanceId: string(b.config.Instance), JobId: string(i.JobID), ObservationJson: raw}))
	return e
}
