// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type appsControlIdentity struct {
	JobID       domain.ID `json:"job_id"`
	OperationID domain.ID `json:"operation_id"`
	Revision    uint64    `json:"revision"`
}

func appsControl(control *pb.CodexAppsControl) (appsControlIdentity, error) {
	if control == nil {
		return appsControlIdentity{}, publicationUncertain()
	}
	value := appsControlIdentity{domain.ID(control.ExecutionJobId), domain.ID(control.AppsOperationId), control.Revision}
	if value.JobID.Validate() != nil || value.OperationID.Validate() != nil || value.Revision == 0 {
		return value, publicationUncertain()
	}
	return value, nil
}

type appsControlJournal struct {
	Version     int                  `json:"version"`
	Control     appsControlIdentity  `json:"control"`
	ServerID    domain.ID            `json:"server_id"`
	DeviceID    domain.ID            `json:"device_id"`
	InstanceID  domain.ID            `json:"instance_id"`
	ExecutionID domain.ID            `json:"execution_id"`
	AccountID   domain.ID            `json:"account_id"`
	ThreadID    domain.ID            `json:"thread_id"`
	ClaimID     domain.ID            `json:"claim_id"`
	ReportID    domain.ID            `json:"report_id"`
	State       responseJournalState `json:"state"`
	// A retained report can be reconciled as metadata without invoking native work.
	Inventory *domain.CodexAppsInventory `json:"inventory,omitempty"`
	Problem   *domain.Error              `json:"problem,omitempty"`
}

type nativeAppsController interface {
	ReadCodexApps(context.Context, string) ([]domain.CodexApp, error)
	RevokeCodexApps(context.Context, domain.ID, domain.CodexAppConfiguration, string) error
}

func startCodexAppsController(ctx, nativeCtx context.Context, cancelNative context.CancelFunc, controls <-chan *pb.CodexAppsControl, mapper *CodexEventPublisher, client nativeAppsController, cwd string) func() error {
	owned, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case <-owned.Done():
				done <- nil
				return
			case control, ok := <-controls:
				if !ok {
					done <- publicationUncertain()
					cancelNative()
					return
				}
				if err := mapper.deliverCodexAppsControl(owned, nativeCtx, control, client, cwd); err != nil {
					if owned.Err() != nil {
						done <- nil
					} else {
						done <- err
						cancelNative()
					}
					return
				}
			}
		}
	}()
	return func() error { stop(); return <-done }
}

func (c *CodexEventPublisher) deliverCodexAppsControl(ctx, publicationCtx context.Context, control *pb.CodexAppsControl, client nativeAppsController, cwd string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil || c.finished {
		return nil
	}
	identity, err := appsControl(control)
	if err != nil {
		return err
	}
	if c.publisher == nil || c.blocked || c.thread == "" || identity.JobID != c.publisher.job || c.publisher.input.CodexApps == nil || !filepath.IsAbs(cwd) {
		return publicationUncertain()
	}
	config := c.publisher.config
	original := *c.publisher.input.CodexApps
	directory := filepath.Join(config.Root, "jobs", string(identity.JobID), "codex-apps")
	if security.PrivateDir(directory) != nil {
		return publicationUncertain()
	}
	path := filepath.Join(directory, string(identity.OperationID)+".json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return publicationUncertain()
	}
	journal := appsControlJournal{Version: 1, Control: identity, ServerID: config.Credential.ServerID, DeviceID: config.Credential.DeviceID, InstanceID: config.Instance, ExecutionID: c.publisher.execution, AccountID: original.AccountID, ThreadID: c.thread, ClaimID: domain.NewID(), ReportID: domain.NewID(), State: responsePrepared}
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	bounded, stop := context.WithTimeout(ctx, 15*time.Second)
	claimed, err := config.Client.ClaimCodexAppsControl(bounded, authenticated(config.Credential, &pb.ClaimCodexAppsControlRequest{Mutation: &pb.Mutation{RequestId: string(journal.ClaimID), Id: string(identity.OperationID), ExpectedRevision: identity.Revision}, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), ExecutionJobId: string(identity.JobID)}))
	stop()
	if err != nil {
		return rpc.ClientError(err)
	}
	if claimed == nil || claimed.Msg == nil || claimed.Msg.Operation == nil || claimed.Msg.Replayed {
		return publicationUncertain()
	}
	resource := claimed.Msg.Operation
	var operation domain.CodexAppsOperation
	if resource.Kind != pb.EntityKind_ENTITY_KIND_UNSPECIFIED || resource.SchemaVersion != 1 || resource.Id != string(identity.OperationID) || resource.SessionId != string(original.SessionID) || resource.Revision <= identity.Revision || domain.DecodeBounded(claimed.Msg.InputJson, &operation, 1<<20) != nil || operation.Validate() != nil || operation.State != domain.CodexAppsClaimed || operation.ClaimID != journal.ClaimID || operation.Revision != resource.Revision || operation.ID != identity.OperationID || operation.ExecutionJobID != identity.JobID || operation.ExecutionID != c.publisher.execution || operation.MachineID != config.Credential.MachineID || operation.InstanceID != config.Instance || operation.NativeThreadID != domain.NativeIdentity(c.thread) || operation.Original.SessionID != original.SessionID || operation.Original.AccountID != original.AccountID || !(reflect.DeepEqual(original, operation.Original) || original.RemovalOnly(operation.Original)) {
		return publicationUncertain()
	}
	var envelope domain.CodexAppsOperation
	if domain.DecodeBounded(resource.DocumentJson, &envelope, 1<<20) != nil || !reflect.DeepEqual(envelope, operation) {
		return publicationUncertain()
	}
	if ctx.Err() != nil {
		return domain.SafeError(ctx.Err())
	}
	journal.State = responseSendIntent
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	bounded, stop = context.WithTimeout(ctx, 30*time.Second)
	next := operation.Original
	var nativeErr error
	if operation.Action == domain.CodexAppsRevoke {
		next = *operation.Next
		nativeErr = client.RevokeCodexApps(bounded, operation.RequestID, next, cwd)
	}
	var rows []domain.CodexApp
	if nativeErr == nil {
		rows, nativeErr = client.ReadCodexApps(bounded, cwd)
	}
	stop()
	journal.State = responseObserved
	if nativeErr != nil {
		journal.Problem = domain.Fail(domain.RecoveryRequired, "The original Codex app operation needs reconciliation.", "Retain its original account, native controller and once-only operation; do not replay native work.")
	} else {
		inventory := domain.CodexAppsInventory{Version: 1, OperationID: operation.ID, ClaimID: operation.ClaimID, NativeCatalogRefreshVerified: true, SessionID: next.SessionID, AccountID: next.AccountID, ConfigurationGeneration: next.Generation, ExecutionID: c.publisher.execution, ExecutionJobID: identity.JobID, MachineID: config.Credential.MachineID, InstanceID: config.Instance, NativeThreadID: domain.NativeIdentity(c.thread), ObservedAt: time.Now().UTC(), Apps: rows}
		if inventory.Validate() != nil {
			journal.Problem = domain.Fail(domain.RecoveryRequired, "The original Codex app catalog is incomplete.", "Retain the original controller and operation instead of inferring callability or cleanup.")
		} else {
			journal.Inventory = &inventory
		}
	}
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	report := &pb.ReportCodexAppsControlResultRequest{Mutation: &pb.Mutation{RequestId: string(journal.ReportID), Id: string(identity.OperationID), ExpectedRevision: resource.Revision}, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), ExecutionJobId: string(identity.JobID)}
	if journal.Problem != nil {
		report.Problem = &pb.ErrorDetail{Code: string(journal.Problem.Code)}
	} else {
		report.OutputJson, _ = json.Marshal(journal.Inventory)
	}
	bounded, stop = context.WithTimeout(publicationCtx, 15*time.Second)
	reported, err := config.Client.ReportCodexAppsControlResult(bounded, authenticated(config.Credential, report))
	stop()
	if err != nil {
		return rpc.ClientError(err)
	}
	if reported == nil || reported.Msg == nil || reported.Msg.Operation == nil {
		return publicationUncertain()
	}
	var receipt domain.CodexAppsOperation
	rr := reported.Msg.Operation
	if rr.Kind != pb.EntityKind_ENTITY_KIND_UNSPECIFIED || rr.Id != resource.Id || rr.SessionId != resource.SessionId || rr.SchemaVersion != 1 || rr.Revision <= resource.Revision || domain.DecodeBounded(rr.DocumentJson, &receipt, 1<<20) != nil || receipt.Validate() != nil || receipt.ID != operation.ID || receipt.RequestID != operation.RequestID || receipt.ClaimID != journal.ClaimID || receipt.Revision != rr.Revision || receipt.ExecutionJobID != identity.JobID || receipt.ExecutionID != operation.ExecutionID || receipt.MachineID != operation.MachineID || receipt.InstanceID != operation.InstanceID || receipt.NativeThreadID != operation.NativeThreadID || !reflect.DeepEqual(receipt.Original, operation.Original) || !reflect.DeepEqual(receipt.Next, operation.Next) || !reflect.DeepEqual(receipt.Inventory, journal.Inventory) || journal.Problem == nil && receipt.State != domain.CodexAppsSucceeded || journal.Problem != nil && receipt.State != domain.CodexAppsUncertain {
		return publicationUncertain()
	}
	if config.Logger != nil {
		config.Logger.InfoContext(ctx, "codex_apps_control_observed", "job_id", identity.JobID, "operation_id", identity.OperationID, "state", receipt.State)
	}
	if journal.Problem != nil {
		return journal.Problem
	}
	return nil
}
