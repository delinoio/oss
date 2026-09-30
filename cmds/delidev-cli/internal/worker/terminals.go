// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// A maximum-sized report is nested inside bounded UUID/digest/phase metadata.
// All journal readers and synchronized writes reserve the same additional room.
const terminalOperationJournalMaxBytes = terminal.MaxResultBytes + (4 << 10)

type terminalOperationPhase string

const (
	terminalPrepared terminalOperationPhase = "prepared"
	terminalClaimed  terminalOperationPhase = "claimed"
	terminalStarted  terminalOperationPhase = "started"
	terminalFinished terminalOperationPhase = "finished"
)

type terminalOperationJournal struct {
	TerminalID  domain.ID              `json:"terminal_id"`
	OperationID domain.ID              `json:"operation_id"`
	InstanceID  domain.ID              `json:"instance_id"`
	Digest      string                 `json:"digest"`
	ClaimID     domain.ID              `json:"claim_id"`
	ReportID    domain.ID              `json:"report_id"`
	Phase       terminalOperationPhase `json:"phase"`
	Result      *terminal.Result       `json:"result,omitempty"`
}

type nativeTerminal struct {
	handle          *process.Handle
	result          terminal.Result
	cancel          context.CancelFunc
	outputDone      chan struct{}
	writer          *terminalWriter
	outputLost      bool
	finished        bool
	naturalReportID domain.ID
}

type terminalManager struct {
	ctx        context.Context
	config     Config
	client     delidevv1connect.WorkerServiceClient
	credential Credential
	instance   domain.ID
	mu         sync.Mutex
	live       map[domain.ID]*nativeTerminal
}

func newTerminalManager(ctx context.Context, config Config, client delidevv1connect.WorkerServiceClient, credential Credential, instance domain.ID) *terminalManager {
	return &terminalManager{ctx: ctx, config: config, client: client, credential: credential, instance: instance, live: map[domain.ID]*nativeTerminal{}}
}
func (m *terminalManager) processRoot() string {
	return filepath.Join(m.config.Root, "terminal-processes")
}

func terminalOperationDigest(a terminal.Assignment) string {
	operation := a.Operation
	operation.Claimed = false
	// Close/input/resize identity binds its immutable operation, not mutable
	// display facts. A pending resize may finish while Archive's close intent is
	// in transit; reconnect must still reconcile that same close journal.
	shell, rows, columns := "", uint16(0), uint16(0)
	if operation.Action == domain.TerminalCreate {
		shell, rows, columns = a.Terminal.ShellOverride, a.Terminal.Rows, a.Terminal.Columns
	}
	value := struct {
		ID, Session, Machine domain.ID
		Operation            domain.TerminalOperation
		Shell                string
		Rows, Columns        uint16
	}{a.ID, a.SessionID, a.Terminal.MachineID, operation, shell, rows, columns}
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (m *terminalManager) journalPath(operation domain.ID) string {
	return filepath.Join(m.config.Root, "terminal-operations", string(operation)+".json")
}
func (m *terminalManager) saveJournal(j terminalOperationJournal) error {
	raw, err := json.Marshal(j)
	if err != nil {
		return domain.SafeError(err)
	}
	if len(raw) > terminalOperationJournalMaxBytes {
		return domain.Fail(domain.ResourceExhausted, "The terminal operation journal exceeds its size limit.", "Preserve original ownership and reconcile before retrying.")
	}
	return security.WriteAtomic(m.journalPath(j.OperationID), raw)
}

func (m *terminalManager) loadJournal(a terminal.Assignment) (terminalOperationJournal, error) {
	var j terminalOperationJournal
	if err := security.PrivateDir(filepath.Join(m.config.Root, "terminal-operations")); err != nil {
		return j, err
	}
	raw, err := security.ReadPrivate(m.journalPath(a.Operation.ID), terminalOperationJournalMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		j = terminalOperationJournal{TerminalID: a.ID, OperationID: a.Operation.ID, InstanceID: m.instance, Digest: terminalOperationDigest(a), ClaimID: domain.NewID(), ReportID: domain.NewID(), Phase: terminalPrepared}
		return j, m.saveJournal(j)
	}
	if err != nil || domain.Decode(raw, &j) != nil || j.TerminalID != a.ID || j.OperationID != a.Operation.ID || j.InstanceID != m.instance || j.Digest != terminalOperationDigest(a) || j.ClaimID.Validate() != nil || j.ReportID.Validate() != nil {
		return j, domain.Fail(domain.RecoveryRequired, "The original terminal operation journal is unavailable or changed.", "Close and reconcile original process ownership; never replay uncertain input.")
	}
	switch j.Phase {
	case terminalPrepared, terminalClaimed, terminalStarted, terminalFinished:
	default:
		return j, domain.TerminalUnavailable()
	}
	return j, nil
}

func (m *terminalManager) report(ctx context.Context, id, operation, requestID domain.ID, result terminal.Result) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return domain.SafeError(err)
	}
	if len(raw) > terminal.MaxResultBytes {
		return domain.Fail(domain.ResourceExhausted, "The terminal report exceeds its size limit.", "Preserve the original result and reconcile its ownership before retrying.")
	}
	_, err = m.client.ReportTerminal(ctx, authenticated(m.credential, &pb.ReportTerminalRequest{RequestId: string(requestID), MachineId: string(m.credential.MachineID), InstanceId: string(m.instance), TerminalId: string(id), OperationId: string(operation), ResultJson: raw}))
	if err != nil {
		return rpc.ClientError(err)
	}
	return nil
}

func (m *terminalManager) apply(ctx context.Context, assignment terminal.Assignment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if assignment.ID.Validate() != nil || assignment.SessionID.Validate() != nil || assignment.Operation.ID.Validate() != nil || assignment.Terminal.MachineID != m.credential.MachineID {
		return domain.TerminalUnavailable()
	}
	switch assignment.Operation.Action {
	case domain.TerminalCreate, domain.TerminalInput, domain.TerminalResize, domain.TerminalClose:
	default:
		return domain.TerminalUnavailable()
	}
	j, err := m.loadJournal(assignment)
	if err != nil {
		return err
	}
	if j.Phase == terminalPrepared {
		claimed, err := m.client.ClaimTerminal(ctx, authenticated(m.credential, &pb.ClaimTerminalRequest{RequestId: string(j.ClaimID), MachineId: string(m.credential.MachineID), InstanceId: string(m.instance), TerminalId: string(assignment.ID), OperationId: string(assignment.Operation.ID)}))
		if err != nil {
			return rpc.ClientError(err)
		}
		var original terminal.Assignment
		if domain.Decode(claimed.Msg.AssignmentJson, &original) != nil || terminalOperationDigest(original) != j.Digest || original.Terminal.InstanceID != m.instance {
			return domain.TerminalUnavailable()
		}
		assignment = original
		j.Phase = terminalClaimed
		if err := m.saveJournal(j); err != nil {
			return err
		}
	}
	if j.Phase == terminalStarted {
		// The synchronized side-effect claim survived, but its outcome did not.
		// Missing completion is never permission to start a second shell or resend.
		result := terminal.Result{State: domain.TerminalUncertain, Rows: assignment.Terminal.Rows, Columns: assignment.Terminal.Columns, Shell: assignment.Terminal.Shell, Cwd: assignment.Terminal.Cwd, Problem: domain.Fail(domain.RecoveryRequired, "The terminal operation has an unconfirmed native outcome.", "Close the original terminal to reconcile exact ownership; do not resend its input.")}
		j.Result, j.Phase = &result, terminalFinished
		if err := m.saveJournal(j); err != nil {
			return err
		}
	}
	if j.Phase == terminalClaimed {
		j.Phase = terminalStarted
		if err := m.saveJournal(j); err != nil {
			return err
		}
		result := m.execute(assignment)
		j.Result, j.Phase = &result, terminalFinished
		if err := m.saveJournal(j); err != nil {
			return err
		}
	}
	if j.Result == nil || j.Result.Validate() != nil {
		return domain.TerminalUnavailable()
	}
	if err := m.report(ctx, assignment.ID, assignment.Operation.ID, j.ReportID, *j.Result); err != nil {
		return err
	}
	// After confirmed report the server will never claim this operation again.
	// Retain only unconfirmed journals; terminal keystrokes cannot grow private
	// metadata indefinitely. Replayed product receipts remain server-owned.
	if err := os.Remove(m.journalPath(j.OperationID)); err != nil {
		return err
	}
	return security.SyncParent(m.journalPath(j.OperationID))
}

func (m *terminalManager) execute(a terminal.Assignment) terminal.Result {
	result := terminal.Result{State: a.Terminal.State, Shell: a.Terminal.Shell, Cwd: a.Terminal.Cwd, Rows: a.Terminal.Rows, Columns: a.Terminal.Columns}
	native := m.live[a.ID]
	switch a.Operation.Action {
	case domain.TerminalCreate:
		if native != nil {
			result.State, result.Problem = domain.TerminalUncertain, domain.Fail(domain.RecoveryRequired, "The original terminal already owns a shell.", "Reattach to it; never create a replacement for this request.")
			return result
		}
		if a.Preparation == nil || a.Manifest == nil || a.Preparation.SessionID != a.SessionID || a.Preparation.MachineID != m.credential.MachineID {
			result.State, result.Problem = domain.TerminalUncertain, workspace.ResultUncertain()
			return result
		}
		ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
		shell, err := terminal.ResolveShell(ctx, m.config.Root, a.ID, a.Terminal.ShellOverride, m.config.Logger)
		cancel()
		if err != nil {
			result.State, result.CleanupVerified, result.Problem = domain.TerminalExited, domain.SafeError(err).Code != domain.RecoveryRequired, domain.SafeError(err)
			if !result.CleanupVerified {
				result.State = domain.TerminalUncertain
			}
			return result
		}
		liveCtx, cancel := context.WithCancel(m.ctx)
		native = &nativeTerminal{cancel: cancel, outputDone: make(chan struct{}), naturalReportID: domain.NewID()}
		output := &terminalWriter{ctx: liveCtx, chunks: make(chan []byte, 64)}
		go func() {
			defer close(native.outputDone)
			native.outputLost = m.publishOutput(liveCtx, a.ID, domain.NewID(), output.chunks)
		}()
		native.writer = output
		args := []string{"-i"}
		if runtime.GOOS == "windows" {
			args = nil
		}
		manager := workspace.Manager{Root: m.config.Root, Logger: m.config.Logger}
		attempted := false
		err = manager.WithTerminalDirectory(m.ctx, *a.Preparation, *a.Manifest, workspace.ReadRequest{ID: domain.NewID()}, func(cwd string) error {
			env, err := terminalEnvironment()
			if err != nil {
				return err
			}
			attempted = true
			native.handle, err = process.Start(liveCtx, process.Config{Directory: m.processRoot(), OwnerID: a.ID, Executable: shell, Args: args, Env: env, Cwd: cwd, Terminal: &process.TerminalSize{Rows: a.Terminal.Rows, Columns: a.Terminal.Columns}, Stdout: output, Stderr: io.Discard, Logger: m.config.Logger})
			if err != nil {
				return err
			}
			result.Shell, result.Cwd = shell, cwd
			return native.handle.Resume()
		})
		if err != nil {
			cancel()
			if native.handle != nil {
				_ = native.handle.Close()
				result.CleanupVerified = m.reconcile(a.ID) == nil
			} else {
				result.CleanupVerified = !attempted && domain.SafeError(err).Code != domain.RecoveryRequired
				if attempted {
					result.CleanupVerified = m.reconcile(a.ID) == nil
				}
			}
			<-native.outputDone
			result.OutputLost = native.outputLost
			result.State, result.Problem = domain.TerminalExited, domain.SafeError(err)
			if !result.CleanupVerified {
				result.State = domain.TerminalUncertain
			}
			return result
		}
		result.State = domain.TerminalRunning
		native.result = result
		m.live[a.ID] = native
		return result
	case domain.TerminalClose:
		if native != nil {
			native.cancel()
			_ = native.handle.Close()
			<-native.outputDone
			result = native.result
			result.OutputLost = native.outputLost
			delete(m.live, a.ID)
		} else if a.Terminal.Pending != nil && a.Terminal.Pending.Action == domain.TerminalCreate && !a.Terminal.Pending.Claimed {
			// Archive won before the durable create claim. The server now rejects
			// that claim, independently proving that no shell could have started.
			result.State, result.CleanupVerified = domain.TerminalClosed, true
			return result
		}
		if native == nil && a.Terminal.Pending != nil && a.Terminal.Pending.Action == domain.TerminalCreate {
			raw, err := security.ReadPrivate(m.journalPath(a.Terminal.Pending.ID), terminalOperationJournalMaxBytes)
			var create terminalOperationJournal
			if err == nil && domain.Decode(raw, &create) == nil && create.TerminalID == a.ID && create.OperationID == a.Terminal.Pending.ID && create.InstanceID == a.Terminal.OwnerInstanceID && (create.Phase == terminalPrepared || create.Phase == terminalClaimed || (create.Phase == terminalFinished && create.Result != nil && create.Result.CleanupVerified)) {
				result.State, result.CleanupVerified = domain.TerminalClosed, true
				return result
			}
		}
		if err := m.reconcile(a.ID); err != nil {
			result.State, result.Problem = domain.TerminalUncertain, domain.SafeError(err)
			return result
		}
		result.State, result.CleanupVerified = domain.TerminalClosed, true
		return result
	case domain.TerminalInput, domain.TerminalResize:
		if native == nil {
			result.State, result.Problem = domain.TerminalUncertain, domain.Fail(domain.RecoveryRequired, "The original shell is not attached to this Worker process.", "Close and reconcile it before creating a new terminal.")
			return result
		}
		select {
		case <-native.handle.Done():
			return m.finishNative(a.ID, native)
		default:
		}
		done := make(chan error, 1)
		go func() {
			if a.Operation.Action == domain.TerminalInput {
				_, err := native.handle.Write(a.Operation.Input)
				done <- err
			} else {
				done <- native.handle.Resize(process.TerminalSize{Rows: a.Operation.Rows, Columns: a.Operation.Columns})
			}
		}()
		select {
		case err := <-done:
			if err != nil {
				native.cancel()
				_ = native.handle.Close()
				result = m.finishNative(a.ID, native)
				// Ownership recovery takes precedence over the triggering I/O
				// failure whenever process-tree cleanup remains unconfirmed.
				if result.CleanupVerified {
					result.Problem = domain.SafeError(err)
				}
				return result
			}
		case <-time.After(5 * time.Second):
			native.cancel()
			_ = native.handle.Close()
			<-done
			result = m.finishNative(a.ID, native)
			if result.CleanupVerified {
				result.Problem = domain.Fail(domain.RecoveryRequired, "Terminal input or resize has an unconfirmed native outcome.", "Inspect the original terminal; do not resend the operation.")
			}
			return result
		case <-m.ctx.Done():
			native.cancel()
			_ = native.handle.Close()
			<-done
			return m.finishNative(a.ID, native)
		}
		result = native.result
		if a.Operation.Action == domain.TerminalResize {
			result.Rows, result.Columns = a.Operation.Rows, a.Operation.Columns
			native.result = result
		}
		return result
	}
	result.State, result.Problem = domain.TerminalUncertain, domain.TerminalUnavailable()
	return result
}

func (m *terminalManager) reconcile(id domain.ID) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(m.ctx), 15*time.Second)
	defer cancel()
	return process.ReconcileOwnerContext(ctx, m.processRoot(), id)
}

func (m *terminalManager) finishNative(id domain.ID, native *nativeTerminal) terminal.Result {
	if native.finished {
		delete(m.live, id)
		return native.result
	}
	result := native.result
	err := native.handle.Wait()
	_ = native.handle.Close()
	close(native.writer.chunks)
	select {
	case <-native.outputDone:
	case <-time.After(15 * time.Second):
		native.cancel()
		<-native.outputDone
	}
	native.cancel()
	result.OutputLost = native.outputLost
	delete(m.live, id)
	if err := m.reconcile(id); err != nil {
		result.State, result.Problem = domain.TerminalUncertain, domain.SafeError(err)
		native.result, native.finished = result, true
		return result
	}
	result.State, result.CleanupVerified = domain.TerminalExited, true
	code := 0
	var exited interface{ ExitCode() int }
	if errors.As(err, &exited) {
		code = exited.ExitCode()
	} else if err != nil {
		result.OutputLost = true
		result.Problem = domain.SafeError(err)
	}
	result.ExitCode = &code
	native.result, native.finished = result, true
	return result
}

func (m *terminalManager) observeExits(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, native := range m.live {
		select {
		case <-native.handle.Done():
			result := m.finishNative(id, native)
			j := terminalOperationJournal{TerminalID: id, OperationID: native.naturalReportID, InstanceID: m.instance, Phase: terminalFinished, ReportID: native.naturalReportID, Result: &result}
			// Preserve the exact spontaneous-exit report through response loss.
			if err := m.saveJournal(j); err != nil {
				// Retain the already joined outcome in memory until its exact
				// report can be synchronized; never close the pumps a second time.
				m.live[id] = native
				m.config.Logger.WarnContext(ctx, "terminal_exit_journal_unavailable", "terminal_id", id, "code", domain.SafeError(err).Code)
				continue
			}
		default:
		}
	}
	directory, err := os.Open(filepath.Join(m.config.Root, "terminal-operations"))
	if err != nil {
		return
	}
	entries, err := directory.ReadDir(4097)
	_ = directory.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return
	}
	if len(entries) > 4096 {
		return
	}
	for _, entry := range entries {
		raw, err := security.ReadPrivate(filepath.Join(m.config.Root, "terminal-operations", entry.Name()), terminalOperationJournalMaxBytes)
		var j terminalOperationJournal
		if err != nil || domain.Decode(raw, &j) != nil || j.InstanceID != m.instance || j.Result == nil || j.Phase != terminalFinished || j.ReportID.Validate() != nil || j.TerminalID.Validate() != nil {
			continue
		}
		operation := j.OperationID
		if j.Digest == "" {
			operation = ""
		}
		if m.report(ctx, j.TerminalID, operation, j.ReportID, *j.Result) == nil {
			_ = os.Remove(m.journalPath(j.OperationID))
			_ = security.SyncParent(m.journalPath(j.OperationID))
		}
	}
}

func (m *terminalManager) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, native := range m.live {
		native.cancel()
		_ = native.handle.Close()
		<-native.outputDone
		if err := m.reconcile(id); err != nil {
			m.config.Logger.Warn("terminal_shutdown_cleanup_unconfirmed", "terminal_id", id, "code", domain.SafeError(err).Code)
		}
		delete(m.live, id)
	}
}

type terminalWriter struct {
	ctx    context.Context
	chunks chan []byte
}

func (w *terminalWriter) Write(raw []byte) (int, error) {
	total := len(raw)
	for len(raw) > 0 {
		n := min(len(raw), 32768)
		select {
		case w.chunks <- slices.Clone(raw[:n]):
			raw = raw[n:]
		case <-w.ctx.Done():
			return total - len(raw), w.ctx.Err()
		}
	}
	return total, nil
}

func (m *terminalManager) publishOutput(ctx context.Context, id, epoch domain.ID, chunks <-chan []byte) bool {
	var sequence uint64
	for {
		var raw []byte
		select {
		case <-ctx.Done():
			return true
		case value, ok := <-chunks:
			if !ok {
				return false
			}
			raw = value
		}
		sequence++
		for ctx.Err() == nil {
			attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
			_, err := m.client.PublishTerminalOutput(attempt, authenticated(m.credential, &pb.PublishTerminalOutputRequest{MachineId: string(m.credential.MachineID), InstanceId: string(m.instance), TerminalId: string(id), Epoch: string(epoch), Sequence: sequence, Data: raw}))
			cancel()
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return true
			case <-time.After(time.Second):
			}
		}
	}
}

func (m *terminalManager) watch(ctx context.Context) {
	for ctx.Err() == nil {
		err := m.watchOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		m.config.Logger.WarnContext(ctx, "terminal_channel_interrupted", "machine_id", m.credential.MachineID, "code", domain.SafeError(err).Code)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (m *terminalManager) watchOnce(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watchdog := time.AfterFunc(domain.WorkerConnectionTimeout, cancel)
	defer watchdog.Stop()
	stream, err := m.client.WatchTerminals(ctx, authenticated(m.credential, &pb.WatchTerminalsRequest{MachineId: string(m.credential.MachineID), InstanceId: string(m.instance)}))
	if err != nil {
		return rpc.ClientError(err)
	}
	defer stream.Close()
	for stream.Receive() {
		watchdog.Reset(domain.WorkerConnectionTimeout)
		message := stream.Msg()
		if message.Heartbeat {
			if len(message.AssignmentJson) != 0 {
				return domain.TerminalUnavailable()
			}
			m.observeExits(ctx)
			continue
		}
		var assignment terminal.Assignment
		if len(message.AssignmentJson) > 1<<20 || domain.Decode(message.AssignmentJson, &assignment) != nil {
			return domain.TerminalUnavailable()
		}
		attempt, stop := context.WithTimeout(ctx, 30*time.Second)
		err := m.apply(attempt, assignment)
		stop()
		if err != nil {
			return err
		}
	}
	return rpc.ClientError(stream.Err())
}
