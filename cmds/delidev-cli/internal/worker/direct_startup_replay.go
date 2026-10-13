package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

const (
	startupReportReady   = "ready"
	startupReportFailure = "failure"

	// A startup report carries one closed observation; the cap only bounds a
	// corrupt or hostile private journal.
	maxStartupReportJournalBytes = 64 << 10
	startupReplayTimeout         = 30 * time.Second
	startupReplayAttemptTimeout  = 10 * time.Second
	startupReplayAttempts        = 3
)

// startupReplayOutcome is the closed result of reconciling a retained startup
// report before terminal job publication.
type startupReplayOutcome int

const (
	// No retained, unacknowledged failure report needs publication.
	startupReplayNotNeeded startupReplayOutcome = iota
	// The server holds the exact original request (first acceptance or replay).
	startupReplayAcknowledged
	// The journal, ownership or server answer cannot prove the original report.
	// No proof is fabricated and no retry authority is granted.
	startupReplayUncertain
	// Transport loss or shutdown left the exact journal for a later attempt.
	startupReplayRetry
)

func (o startupReplayOutcome) String() string {
	switch o {
	case startupReplayNotNeeded:
		return "not_needed"
	case startupReplayAcknowledged:
		return "acknowledged"
	case startupReplayUncertain:
		return "uncertain"
	case startupReplayRetry:
		return "retry"
	}
	return "unknown"
}

type startupReportJournal struct {
	Request []byte `json:"request"`
}

func startupReportPath(root string, job domain.ID, name string) string {
	return filepath.Join(root, "jobs", string(job), "startup-"+name+".json")
}

// The acknowledgement is stored beside, never inside, the exact request journal.
func startupReportAckPath(root string, job domain.ID, name string) string {
	return filepath.Join(root, "jobs", string(job), "startup-"+name+".ack.json")
}

func startupReportDigest(request []byte) []byte {
	digest := sha256.Sum256(request)
	return []byte(hex.EncodeToString(digest[:]))
}

// readStartupReport decodes one retained request and validates it against the
// live claim. It never repairs, rewrites or reconstructs a journal.
func readStartupReport(root string, c *PublicationConfig, job domain.Job, name string) (*pb.ReportExecutionStartupRequest, []byte, error) {
	raw, err := security.ReadPrivate(startupReportPath(root, domain.ID(c.Assignment.Id), name), maxStartupReportJournalBytes)
	if err != nil {
		return nil, nil, err
	}
	var entry startupReportJournal
	if domain.DecodeWithLimit(raw, &entry, maxStartupReportJournalBytes) != nil || len(entry.Request) == 0 {
		return nil, nil, publicationUncertain()
	}
	request := &pb.ReportExecutionStartupRequest{}
	if proto.Unmarshal(entry.Request, request) != nil {
		return nil, nil, publicationUncertain()
	}
	var input domain.ExecutionJobInput
	meta := request.Mutation
	if meta == nil || domain.Decode(job.Input, &input) != nil || input.Version != 4 || meta.Id != c.Assignment.Id || meta.ExpectedRevision != c.Assignment.Revision || domain.ID(meta.RequestId).Validate() != nil || request.MachineId != string(input.MachineID) || request.MachineId != string(c.Credential.MachineID) || request.InstanceId != string(c.Instance) {
		return nil, nil, publicationUncertain()
	}
	observation, err := rpc.StartupObservation(request.Observation)
	if err != nil || observation.CorrelationID != domain.ID(c.Assignment.Id) || observation.Harness != input.Configuration.Harness {
		return nil, nil, publicationUncertain()
	}
	if name == startupReportReady && observation.State != domain.StartupReady || name == startupReportFailure && observation.State == domain.StartupReady {
		return nil, nil, publicationUncertain()
	}
	return request, entry.Request, nil
}

// reconcileExecutionStartupReport republishes the exact retained startup-failure
// request before the terminal job report, so original no-send and confirmed
// cleanup proof survives a transport failure. It only reads private journals
// and calls ReportExecutionStartup: it never launches a process, re-runs
// initialization, sends input or replays the ready report.
func reconcileExecutionStartupReport(ctx context.Context, config Config, c *PublicationConfig, job domain.Job, terminalProblem bool) startupReplayOutcome {
	if c == nil || c.Assignment == nil || c.Client == nil || job.Type != domain.ExecuteSessionJob || domain.ID(c.Assignment.Id).Validate() != nil {
		return startupReplayNotNeeded
	}
	jobID := domain.ID(c.Assignment.Id)
	log := func(outcome startupReplayOutcome, reason string, attrs ...any) {
		if config.Logger == nil {
			return
		}
		attrs = append([]any{"job_id", jobID, "outcome", outcome.String(), "reason", reason}, attrs...)
		config.Logger.WarnContext(ctx, "execution_startup_report_reconciled", attrs...)
	}
	path := startupReportPath(c.Root, jobID, startupReportFailure)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return startupReplayNotNeeded
	} else if err != nil {
		log(startupReplayUncertain, "journal_unreadable")
		return startupReplayUncertain
	}
	request, raw, err := readStartupReport(c.Root, c, job, startupReportFailure)
	if err != nil {
		log(startupReplayUncertain, "journal_invalid", "code", domain.SafeError(err).Code)
		return startupReplayUncertain
	}
	// A failure report beside a successful terminal result is contradictory.
	if !terminalProblem {
		log(startupReplayUncertain, "contradictory_terminal_result")
		return startupReplayUncertain
	}
	// The ready journal is never replayed: an uncertain ready acknowledgement
	// cannot implicitly resume the original process. A corrupt one is contradictory.
	if _, err := os.Lstat(startupReportPath(c.Root, jobID, startupReportReady)); err == nil {
		if _, _, err := readStartupReport(c.Root, c, job, startupReportReady); err != nil {
			log(startupReplayUncertain, "ready_journal_invalid", "code", domain.SafeError(err).Code)
			return startupReplayUncertain
		}
	} else if !os.IsNotExist(err) {
		log(startupReplayUncertain, "ready_journal_unreadable")
		return startupReplayUncertain
	}
	ackPath := startupReportAckPath(c.Root, jobID, startupReportFailure)
	expected := startupReportDigest(raw)
	if retained, err := security.ReadPrivate(ackPath, 64); err == nil {
		if !bytes.Equal(retained, expected) {
			log(startupReplayUncertain, "acknowledgement_mismatch")
			return startupReplayUncertain
		}
		return startupReplayAcknowledged
	} else if !os.IsNotExist(err) {
		log(startupReplayUncertain, "acknowledgement_unreadable")
		return startupReplayUncertain
	}
	// Cancellation interrupts the wait, but the exact journal stays for the next
	// attempt; the bound keeps Worker shutdown from waiting on a dead transport.
	bounded, cancel := context.WithTimeout(ctx, startupReplayTimeout)
	defer cancel()
	for attempt := 1; attempt <= startupReplayAttempts; attempt++ {
		call, stop := context.WithTimeout(bounded, startupReplayAttemptTimeout)
		response, err := c.Client.ReportExecutionStartup(call, authenticated(c.Credential, request))
		stop()
		if err == nil {
			if response == nil || response.Msg == nil || !proto.Equal(response.Msg.Observation, request.Observation) {
				log(startupReplayUncertain, "response_mismatch")
				return startupReplayUncertain
			}
			if err := security.WriteAtomic(ackPath, expected); err != nil {
				// The server receipt is durable and idempotent; a missing local
				// acknowledgement only repeats this exact replay later.
				log(startupReplayAcknowledged, "acknowledgement_not_persisted", "replayed", response.Msg.Replayed)
				return startupReplayAcknowledged
			}
			if config.Logger != nil {
				config.Logger.InfoContext(ctx, "execution_startup_report_reconciled", "job_id", jobID, "outcome", startupReplayAcknowledged.String(), "attempt", attempt, "replayed", response.Msg.Replayed, "state", request.Observation.State, "code", request.Observation.ProblemCode)
			}
			return startupReplayAcknowledged
		}
		if ctx.Err() != nil {
			log(startupReplayRetry, "interrupted", "attempt", attempt)
			return startupReplayRetry
		}
		if code := connect.CodeOf(err); code != connect.CodeUnavailable && code != connect.CodeDeadlineExceeded {
			// Changed epoch, stale revision or terminal ownership: no proof exists.
			log(startupReplayUncertain, "server_rejected", "attempt", attempt, "code", domain.SafeError(rpc.ClientError(err)).Code)
			return startupReplayUncertain
		}
		log(startupReplayRetry, "transport_unavailable", "attempt", attempt)
		if bounded.Err() != nil {
			break
		}
	}
	return startupReplayRetry
}
