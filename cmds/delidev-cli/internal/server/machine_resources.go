// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Heartbeats are independent observations, not entity mutations or event-cursor
// authority. Keep the original document/revision for every business operation.
func (s *Service) resourceProjection(ctx context.Context, record store.Record) (*pb.Resource, error) {
	if record.Kind == domain.MachineKind {
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			instance, seen, err := tx.WorkerInstance(record.ID)
			if err != nil {
				return err
			}
			record, err = machineHeartbeatProjection(record, instance, seen, time.Now().UTC())
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	return resourceProjection(record)
}

func machineHeartbeatProjection(record store.Record, instance domain.ID, seen, now time.Time) (store.Record, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(record.Data, &fields); err != nil || fields == nil {
		return record, domain.Fail(domain.RecoveryRequired, "Machine metadata is invalid.", "Preserve the original Machine record.")
	}
	// The attachment-time timestamp cannot stand in for a missing or malformed
	// current lease. Omission stays unknown to earlier clients without new fields.
	delete(fields, "last_seen")
	if instance.Validate() == nil && seen.UnixMilli() > 0 && !seen.After(now.Add(time.Second)) {
		raw, err := json.Marshal(seen)
		if err != nil {
			return record, err
		}
		fields["last_seen"] = raw
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return record, err
	}
	record.Data = raw
	return record, nil
}
