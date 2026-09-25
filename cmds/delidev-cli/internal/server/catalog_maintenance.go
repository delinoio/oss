package server

import (
	"context"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const catalogRefreshInterval = 15 * time.Minute

// A small bounded pool makes progress independently of a stalled provider. Due
// observations persist across restart; undispatched reads need no catch-up queue.
// Parent cancellation is joined before the vault/database/scope can be released.
func (s *Service) runCatalogMaintenance(parent context.Context) {
	ctx, cancel := context.WithCancel(domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice}))
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	completed := make(chan domain.ID, 4)
	active := map[domain.ID]bool{}
	cooldown := map[domain.ID]time.Time{}
	var after domain.ID
	var nextScan time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		changed := s.Store.Changed()
		now := time.Now()
		for id, until := range cooldown {
			if !until.After(now) {
				delete(cooldown, id)
			}
		}
		if len(active) < 4 && !now.Before(nextScan) {
			// Unrelated high-volume events must not turn this background task
			// into a hot SQLite scan. The ticker also guarantees eventual work.
			nextScan = now.Add(100 * time.Millisecond)
			page, err := s.Store.CatalogCandidates(ctx, after, store.MaxPage, now, catalogRefreshInterval)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				s.logger.Warn("catalog_maintenance_scan_failed", "error_code", domain.SafeError(err).Code)
			} else {
				for _, record := range page {
					after = record.ID
					if active[record.ID] || cooldown[record.ID].After(now) {
						continue
					}
					active[record.ID] = true
					// Bounded cooldown also handles unaccepted revision/native-state
					// races without a hot loop or an unbounded failed-job backlog.
					cooldown[record.ID] = now.Add(30 * time.Second)
					workers.Add(1)
					go func(record store.Record) {
						defer workers.Done()
						requestID := domain.NewID()
						_, err := s.discoverModels(ctx, &pb.Mutation{Id: string(record.ID), ExpectedRevision: record.Revision, RequestId: string(requestID)}, string(requestID))
						if err != nil && ctx.Err() == nil {
							s.logger.Warn("catalog_maintenance_unpublished", "account_id", record.ID, "request_id", requestID, "error_code", domain.SafeError(err).Code)
						}
						select {
						case completed <- record.ID:
						case <-ctx.Done():
						}
					}(record)
					if len(active) == 4 {
						break
					}
				}
				if len(page) < store.MaxPage && len(active) < 4 {
					after = ""
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case id := <-completed:
			delete(active, id)
		case <-changed:
		case <-tick.C:
		}
	}
}
