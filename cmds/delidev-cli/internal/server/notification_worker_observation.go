// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"time"
)

func (s *Service) runWorkerNotificationObservation(parent context.Context) {
	epoch := domain.NewID()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var after domain.ID
	for {
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		for {
			next, err := s.Store.ObserveWorkerNotifications(ctx, epoch, after)
			if err != nil {
				s.logger.Warn("notification_worker_observation_failed", "error_code", domain.SafeError(err).Code)
				break
			}
			after = next
			if next == "" {
				break
			}
		}
		cancel()
		select {
		case <-parent.Done():
			s.logger.Info("notification_worker_observation_joined")
			return
		case <-ticker.C:
		}
	}
}
