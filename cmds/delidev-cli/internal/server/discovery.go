package server

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type discoveryAccepted struct {
	Machine store.Record `json:"machine"`
	Job     store.Record `json:"job"`
}

func (s *Service) DiscoverHarnesses(ctx context.Context, req *connect.Request[pb.DiscoverHarnessesRequest]) (*connect.Response[pb.DiscoverHarnessesResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	if meta == nil || meta.ExpectedRevision == 0 {
		return nil, rpc.Error(domain.Fail(domain.MissingInput, "A machine and its current revision are required.", "Read the machine before refreshing its executable selections."), correlation)
	}
	var selections *domain.ExecutableSelections
	if len(req.Msg.SelectionsJson) > 0 {
		var value domain.ExecutableSelections
		if err := domain.Decode(req.Msg.SelectionsJson, &value); err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if err := value.Validate(); err != nil {
			return nil, rpc.Error(err, correlation)
		}
		selections = &value
	}
	input := struct {
		Machine    domain.ID
		Revision   uint64
		Selections *domain.ExecutableSelections
		// Omission preserves receipts issued before protocol probes were added.
		VerifyProtocol bool `json:"VerifyProtocol,omitempty"`
	}{domain.ID(meta.Id), meta.ExpectedRevision, selections, req.Msg.VerifyProtocol}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "machine.discover", input, func(tx *store.Tx) (any, error) {
		record, machine, err := activeMachine(tx, input.Machine)
		if err != nil {
			return nil, err
		}
		if machine.DiscoveryRevision == math.MaxUint64 {
			return nil, domain.Fail(domain.ResourceExhausted, "The discovery revision is exhausted.", "Pair a new machine identity.")
		}
		selected := domain.ExecutableSelections{Executables: []domain.ExecutableSelection{}}
		if selections != nil {
			selected = *selections
		} else {
			for _, installation := range machine.Installations {
				selected.Executables = append(selected.Executables, domain.ExecutableSelection{Harness: installation.Harness, Path: installation.ExplicitPath})
			}
		}
		if err := selected.Validate(); err != nil {
			return nil, err
		}
		machine.DiscoveryRevision++
		machine.Installations = selected.Installations()
		saved, err := tx.Put(domain.MachineKind, record.ID, meta.ExpectedRevision, "", "", machine)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(domain.HarnessDiscoveryInput{Revision: machine.DiscoveryRevision, Selections: selected, VerifyProtocol: req.Msg.VerifyProtocol})
		if err != nil {
			return nil, err
		}
		job, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.HarnessDiscoveryJob, State: domain.JobQueued, MachineID: record.ID, Input: raw, AcceptedAt: time.Now().UTC()})
		if err != nil {
			return nil, err
		}
		return discoveryAccepted{Machine: saved, Job: job}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var accepted discoveryAccepted
	if err := domain.Decode(result.Data, &accepted); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "harness discovery accepted", "machine_id", meta.Id, "job_id", accepted.Job.ID, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.DiscoverHarnessesResponse{Machine: rpc.Resource(accepted.Machine), Job: rpc.Resource(accepted.Job), RequestId: meta.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func finishDiscovery(tx *store.Tx, job domain.Job, raw []byte) (json.RawMessage, *domain.Error, error) {
	var input domain.HarnessDiscoveryInput
	if err := domain.Decode(job.Input, &input); err != nil {
		return nil, nil, err
	}
	var output domain.HarnessDiscoveryOutput
	if err := domain.Decode(raw, &output); err != nil {
		return nil, nil, err
	}
	if err := output.ValidateDiscovery(input); err != nil {
		return nil, nil, err
	}
	record, machine, err := activeMachine(tx, job.MachineID)
	if err != nil {
		return nil, nil, err
	}
	if machine.DiscoveryRevision != input.Revision {
		return nil, domain.Fail(domain.Conflict, "A newer discovery request superseded this result.", "Inspect the latest machine discovery job; its selected paths and observations were preserved."), nil
	}
	now := time.Now().UTC()
	for index := range output.Installations {
		output.Installations[index].ObservedAt = &now
	}
	machine.Installations = output.Installations
	if _, err := tx.Put(domain.MachineKind, record.ID, record.Revision, "", "", machine); err != nil {
		return nil, nil, err
	}
	canonical, err := json.Marshal(output)
	return canonical, nil, err
}
