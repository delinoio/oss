// SPDX-License-Identifier: Apache-2.0
package knownmodels

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const SuccessInterval = 24 * time.Hour
const FailureInterval = time.Hour
const RequestTimeout = 15 * time.Second

type diskCache struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Catalog   json.RawMessage `json:"catalog"`
}
type Snapshot struct {
	Version   string
	UpdatedAt string
	Source    Source
	Models    []Model
}
type Manager struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex
	catalog   Catalog
	source    Source
	fetchedAt time.Time
	path      string
	client    *http.Client
	logger    *slog.Logger
	now       func() time.Time
}

// New restores only validated private cache bytes. The transport must be the
// server's explicit outbound route; this package has no ambient network path.
func New(root string, transport http.RoundTripper, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	manager := &Manager{catalog: BundledCatalog(), source: Bundled, path: filepath.Join(root, "known-subscription-models.json"), logger: logger, now: time.Now}
	manager.client = &http.Client{Transport: transport, Timeout: RequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	raw, err := security.ReadPrivate(manager.path, MaxBytes)
	if err == nil {
		var cached diskCache
		if strictJSON(raw, &cached) == nil && !cached.FetchedAt.IsZero() && !cached.FetchedAt.After(manager.now()) {
			catalog, err := Decode(cached.Catalog)
			// A date-only catalog timestamp cannot order two different reviewed
			// revisions published on the same day. Keep an exact-version cache, but
			// prefer the bundled catalog for an equal-date mismatch and refresh it
			// immediately instead of extending an ambiguous cache deadline.
			cacheMatchesBundle := catalog.UpdatedAt > manager.catalog.UpdatedAt ||
				catalog.UpdatedAt == manager.catalog.UpdatedAt && catalog.CatalogVersion == manager.catalog.CatalogVersion
			if err == nil && cacheMatchesBundle {
				manager.catalog, manager.source, manager.fetchedAt = catalog, Cache, cached.FetchedAt
				return manager
			}
		}
		manager.logger.Warn("known_model_cache_rejected", "code", "invalid_cache")
	} else if !errors.Is(err, os.ErrNotExist) {
		manager.logger.Warn("known_model_cache_unavailable", "code", "cache_read_failed")
	}
	return manager
}
func (m *Manager) List(service string) Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := Snapshot{Version: m.catalog.CatalogVersion, UpdatedAt: m.catalog.UpdatedAt, Source: m.source}
	today := m.now().UTC().Format(time.DateOnly)
	for _, entry := range m.catalog.Services {
		if entry.Service != service {
			continue
		}
		for _, model := range entry.Models {
			if model.RetirementDate == "" || model.RetirementDate > today {
				model.SourceKeys = append([]string(nil), model.SourceKeys...)
				result.Models = append(result.Models, model)
			}
		}
	}
	return result
}
func (m *Manager) Refresh(ctx context.Context) error {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	err := m.refresh(ctx)
	if err != nil {
		m.mu.Lock()
		if m.source == Online {
			m.source = Cache
		}
		m.mu.Unlock()
		// Never log response content, transport errors, proxy URLs or credentials.
		m.logger.Warn("known_model_refresh_failed", "code", "catalog_unavailable")
	}
	return err
}
func (m *Manager) refresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	if m.client.Transport == nil {
		return errors.New("explicit outbound transport required")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, CatalogURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > MaxBytes {
		return ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxBytes+1))
	if err != nil {
		return err
	}
	catalog, err := Decode(raw)
	if err != nil {
		return err
	}
	m.mu.RLock()
	older := catalog.UpdatedAt < m.catalog.UpdatedAt
	m.mu.RUnlock()
	if older {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fetched := m.now().UTC()
	cached, err := json.Marshal(diskCache{FetchedAt: fetched, Catalog: raw})
	if err != nil || len(cached) > MaxBytes {
		return ErrInvalid
	}
	if err := security.WriteAtomic(m.path, cached); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	m.catalog, m.source, m.fetchedAt = catalog, Online, fetched
	m.mu.Unlock()
	m.logger.Info("known_model_catalog_refreshed", "version", catalog.CatalogVersion, "updated_at", catalog.UpdatedAt)
	return nil
}

// Run has one joined owner in server startup. A successful cached read retains
// its original refresh deadline across restarts; failures never busy-loop.
func (m *Manager) Run(ctx context.Context) {
	m.mu.RLock()
	delay := m.fetchedAt.Add(SuccessInterval).Sub(m.now())
	m.mu.RUnlock()
	if delay < 0 {
		delay = 0
	}
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := m.Refresh(ctx); err != nil {
			delay = FailureInterval
		} else {
			delay = SuccessInterval
		}
		if ctx.Err() != nil {
			return
		}
	}
}
