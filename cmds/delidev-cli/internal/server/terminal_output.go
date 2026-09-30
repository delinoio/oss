// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const (
	terminalOutputBytes  = 512 << 10
	terminalOutputFrames = 1024
)

type terminalOutputChunk struct {
	sequence uint64
	data     []byte
}
type terminalOutputRing struct {
	epoch          domain.ID
	workerEpoch    domain.ID
	workerSequence uint64
	lastData       []byte
	sequence       uint64
	bytes          int
	chunks         []terminalOutputChunk
	touched        time.Time
}

// A service-wide 128-ring limit bounds all retained output to 64 MiB. Eviction
// and server restart change the epoch; clients must display an explicit gap.
func (s *Service) terminalRing(id domain.ID) *terminalOutputRing {
	if s.terminalOutputs == nil {
		s.terminalOutputs = map[domain.ID]*terminalOutputRing{}
	}
	if ring := s.terminalOutputs[id]; ring != nil {
		ring.touched = time.Now()
		return ring
	}
	if len(s.terminalOutputs) >= 128 {
		var oldest domain.ID
		for key, ring := range s.terminalOutputs {
			if oldest == "" || ring.touched.Before(s.terminalOutputs[oldest].touched) {
				oldest = key
			}
		}
		delete(s.terminalOutputs, oldest)
	}
	ring := &terminalOutputRing{epoch: domain.NewID(), touched: time.Now()}
	s.terminalOutputs[id] = ring
	return ring
}

func (s *Service) PublishTerminalOutput(ctx context.Context, req *connect.Request[pb.PublishTerminalOutputRequest]) (*connect.Response[pb.PublishTerminalOutputResponse], error) {
	fail := func(err error) (*connect.Response[pb.PublishTerminalOutputResponse], error) {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return fail(err)
	}
	if domain.ID(req.Msg.TerminalId).Validate() != nil || domain.ID(req.Msg.Epoch).Validate() != nil || req.Msg.Sequence == 0 || len(req.Msg.Data) == 0 || len(req.Msg.Data) > 32768 {
		return fail(domain.Fail(domain.InvalidArgument, "Invalid terminal output frame.", "Publish bounded, ordered original bytes."))
	}
	actor, _ := domain.PrincipalFrom(ctx)
	// Serialize authorization with publication, including concurrent retries.
	s.terminalOutputMu.Lock()
	defer s.terminalOutputMu.Unlock()
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := terminalMachine(tx, domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)); err != nil {
			return err
		}
		_, value, err := terminalRecord(tx, domain.ID(req.Msg.TerminalId))
		if err != nil {
			return err
		}
		if value.MachineID != domain.ID(req.Msg.MachineId) || value.InstanceID != domain.ID(req.Msg.InstanceId) || value.DeviceID != actor.DeviceID || !value.Live() {
			return domain.TerminalUnavailable()
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	ring := s.terminalRing(domain.ID(req.Msg.TerminalId))
	if ring.workerEpoch != "" && ring.workerEpoch != domain.ID(req.Msg.Epoch) {
		return fail(domain.Fail(domain.RecoveryRequired, "The native terminal output identity changed.", "Preserve the original shell and inspect its ownership."))
	}
	if ring.workerSequence == req.Msg.Sequence {
		if !bytes.Equal(ring.lastData, req.Msg.Data) {
			return fail(domain.Fail(domain.Conflict, "Terminal output retry bytes changed.", "Retry only the identical frame."))
		}
		return connect.NewResponse(&pb.PublishTerminalOutputResponse{}), nil
	}
	if ring.workerSequence != 0 && req.Msg.Sequence != ring.workerSequence+1 {
		return fail(domain.Fail(domain.Conflict, "Terminal output is out of order.", "Retry the original unacknowledged frame."))
	}
	if ring.sequence == math.MaxUint64 {
		return fail(domain.Fail(domain.ResourceExhausted, "Terminal output sequence capacity is exhausted.", "Close the original terminal."))
	}
	ring.workerEpoch, ring.workerSequence = domain.ID(req.Msg.Epoch), req.Msg.Sequence
	ring.lastData = slices.Clone(req.Msg.Data)
	// A fresh ring accepting a later native frame exposes that missing prefix.
	if ring.sequence == 0 && req.Msg.Sequence > 1 {
		ring.sequence = req.Msg.Sequence - 1
	}
	ring.sequence++
	ring.chunks = append(ring.chunks, terminalOutputChunk{ring.sequence, ring.lastData})
	ring.bytes += len(req.Msg.Data)
	// The independent frame count also bounds metadata and tiny allocations:
	// a valid one-byte producer cannot turn a byte budget into millions of nodes.
	for ring.bytes > terminalOutputBytes || len(ring.chunks) > terminalOutputFrames {
		ring.bytes -= len(ring.chunks[0].data)
		ring.chunks[0] = terminalOutputChunk{}
		ring.chunks = ring.chunks[1:]
	}
	return connect.NewResponse(&pb.PublishTerminalOutputResponse{}), nil
}

func (s *Service) WatchTerminalOutput(ctx context.Context, req *connect.Request[pb.WatchTerminalOutputRequest], stream *connect.ServerStream[pb.WatchTerminalOutputResponse]) error {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := terminalClient(ctx); err != nil {
		return rpc.Error(err, correlation)
	}
	id := domain.ID(req.Msg.TerminalId)
	if id.Validate() != nil || (req.Msg.Epoch != "" && domain.ID(req.Msg.Epoch).Validate() != nil) || (req.Msg.Epoch == "" && req.Msg.AfterSequence != 0) {
		return rpc.Error(domain.Fail(domain.InvalidArgument, "Invalid terminal output cursor.", "Reattach with the last observed epoch and sequence."), correlation)
	}
	controller, ok := ctx.Value(writeControllerKey{}).(*http.ResponseController)
	if !ok {
		return rpc.Error(domain.TerminalUnavailable(), correlation)
	}
	send := func(message *pb.WatchTerminalOutputResponse) error {
		if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return err
		}
		return stream.Send(message)
	}
	after, epoch, revision := req.Msg.AfterSequence, domain.ID(req.Msg.Epoch), uint64(0)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	nextHeartbeat := time.Time{}
	for {
		var record store.Record
		var value domain.Terminal
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			var err error
			record, value, err = terminalRecord(tx, id)
			if err == nil && value.Live() {
				err = terminalMachine(tx, value.MachineID, value.InstanceID)
			}
			return err
		})
		if err != nil {
			return rpc.Error(err, correlation)
		}
		s.terminalOutputMu.Lock()
		ring := s.terminalRing(id)
		first := ring.sequence + 1
		if len(ring.chunks) != 0 {
			first = ring.chunks[0].sequence
		}
		cursorGap := (epoch != "" && epoch != ring.epoch) || after > ring.sequence || (first > 1 && after < first-1)
		gap := (value.OutputLost && revision != record.Revision) || cursorGap
		// Losing unpublished bytes changes completeness, not acknowledgment of
		// retained bytes. Only an invalid cursor may replay the retained suffix.
		if epoch != ring.epoch || cursorGap {
			after = first - 1
		}
		epoch = ring.epoch
		chunks := make([]terminalOutputChunk, 0, len(ring.chunks))
		for _, c := range ring.chunks {
			if c.sequence > after {
				chunks = append(chunks, c)
			}
		}
		s.terminalOutputMu.Unlock()
		if gap || revision != record.Revision || time.Now().After(nextHeartbeat) {
			if err := send(&pb.WatchTerminalOutputResponse{Epoch: string(epoch), Sequence: after, Gap: gap, Heartbeat: true, Terminal: rpc.Resource(record)}); err != nil {
				return err
			}
			revision = record.Revision
			nextHeartbeat = time.Now().Add(10 * time.Second)
		}
		for _, c := range chunks {
			if err := send(&pb.WatchTerminalOutputResponse{Epoch: string(epoch), Sequence: c.sequence, Data: c.data}); err != nil {
				return err
			}
			after = c.sequence
		}
		if value.CleanupVerified {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
