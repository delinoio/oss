// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/hex"
	"time"
)

type PricingMode string

const (
	AutomaticPricing PricingMode = "automatic"
	ManualPricing    PricingMode = "manual"
)

type PricingPolicy struct {
	Version  int           `json:"version"`
	Model    ModelIdentity `json:"model"`
	Mode     PricingMode   `json:"mode"`
	Revision uint64        `json:"revision"`
}

func (p PricingPolicy) Validate() error {
	if p.Version != 1 || p.Model.Validate() != nil || (p.Mode != AutomaticPricing && p.Mode != ManualPricing) || p.Revision > 1<<63-1 {
		return invalidPricing()
	}
	return nil
}

type PriceProvenance struct {
	ProviderKey    string    `json:"provider_key"`
	ModelKey       string    `json:"model_key"`
	SnapshotSHA256 string    `json:"snapshot_sha256"`
	RetrievedAt    time.Time `json:"retrieved_at"`
}

func (p PriceProvenance) Validate() error {
	b, e := hex.DecodeString(p.SnapshotSHA256)
	if e != nil || len(b) != 32 || p.RetrievedAt.IsZero() || Text(p.ProviderKey, "upstream provider key", 128, true) != nil || Text(p.ModelKey, "upstream native model key", 256, true) != nil {
		return invalidPricing()
	}
	return nil
}
