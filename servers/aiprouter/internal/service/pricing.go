// SPDX-License-Identifier: Apache-2.0
package service

import (
	"encoding/json"
	"github.com/delinoio/oss/packages/aiprouter/pricing"
)

func priceOverrides(raw json.RawMessage) ([]pricing.Override, error) {
	return managedPriceOverrides(raw)
}

func (s *server) priceSnapshot(raw json.RawMessage) (*pricing.Snapshot, error) {
	s.priceMu.Lock()
	defer s.priceMu.Unlock()
	if s.priceCache != nil && s.priceJSON == string(raw) {
		return s.priceCache, nil
	}
	overrides, e := priceOverrides(raw)
	if e != nil {
		return nil, e
	}
	snapshot, e := pricing.NewSnapshot(overrides)
	if e != nil {
		return nil, e
	}
	s.priceCache = snapshot
	s.priceJSON = string(raw)
	return snapshot, nil
}
