// SPDX-License-Identifier: Apache-2.0
package service

import (
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"testing"
	"time"
)

func TestKeyMetadataMapping(t *testing.T) {
	now := time.Now().UTC()
	used := now.Add(time.Minute)
	row := keyMessage(store.Key{ID: "key", Name: "fixture", CreatedAt: now, Prefix: "aip_fixture", LastUsedAt: &used, Device: true}, "org")
	if row.CreatedAt == nil || !row.CreatedAt.AsTime().Equal(now) || row.LastUsedAt == nil || !row.LastUsedAt.AsTime().Equal(used) || row.Prefix != "aip_fixture" || row.Kind != v1.KeyKind_KEY_KIND_CLI {
		t.Fatalf("metadata omitted: %v", row)
	}
	legacy := keyMessage(store.Key{ID: "old", CreatedAt: now}, "org")
	if legacy.Prefix != "" || legacy.LastUsedAt != nil || legacy.Kind != v1.KeyKind_KEY_KIND_PERSONAL {
		t.Fatalf("legacy metadata fabricated: %v", legacy)
	}
}
