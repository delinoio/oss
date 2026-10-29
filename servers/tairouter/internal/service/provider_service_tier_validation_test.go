// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

func serviceTierConfig(models map[string]bool, fast *bool) *v1.ProviderConfiguration {
	yes := true
	return &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{
		SupportsServiceTier:      &yes,
		ModelSupportsServiceTier: models,
		FastEnabled:              fast,
	}}
}

func TestProviderServiceTierValidation(t *testing.T) {
	for _, models := range []map[string]bool{
		{"WIRE-MODEL": false, "wire-model": true},
		{"WIRE-MODEL": true, "wire-model": true},
		{"WIRE-MODEL": false, "wire-model": false},
		{"K-MODEL": true, "K-MODEL": false},
		{"S-MODEL": false, "ſ-model": false},
		{"Σ-MODEL": true, "ς-model": false},
	} {
		if err := validateProviderConfiguration("custom", serviceTierConfig(models, nil)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("folded duplicate accepted: %v, %v", models, err)
		}
	}
	for _, config := range []*v1.ProviderConfiguration{
		nil,
		{},
		{Policy: &v1.ProviderPolicyOverrides{}},
		serviceTierConfig(map[string]bool{"WiRe-MoDeL": false}, ptr(false)),
		serviceTierConfig(map[string]bool{"ß-model": true, "ss-model": false}, nil),
		serviceTierConfig(map[string]bool{"é-model": true, "e\u0301-model": false}, nil),
	} {
		if err := validateProviderConfiguration("custom", config); err != nil {
			t.Fatalf("valid configuration rejected: %v", err)
		}
	}
}

func TestProviderServiceTierValidationLargeMap(t *testing.T) {
	models := make(map[string]bool, 16385)
	for i := 0; i < 16384; i++ {
		models["wire-model-"+strconv.Itoa(i)] = false
	}
	models["MiXeD-CAse"] = false
	if err := validateProviderConfiguration("custom", serviceTierConfig(models, nil)); err != nil {
		t.Fatal(err)
	}
	if value, exists := models["MiXeD-CAse"]; !exists || value || len(models) != 16385 {
		t.Fatal("validation changed the configured keys or values")
	}
}

func BenchmarkProviderServiceTierValidation(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			models := make(map[string]bool, count)
			for i := 0; i < count; i++ {
				models["wire-model-"+strconv.Itoa(i)] = false
			}
			config := serviceTierConfig(models, nil)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := validateProviderConfiguration("custom", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestProviderServiceTierRejectsFoldedDuplicateKeys(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	cases := []struct {
		name   string
		models map[string]bool
	}{
		{"conflicting values", map[string]bool{"WIRE-MODEL": false, "wire-model": true}},
		{"both true", map[string]bool{"WIRE-MODEL": true, "wire-model": true}},
		{"both false", map[string]bool{"WIRE-MODEL": false, "wire-model": false}},
		{"Unicode simple fold", map[string]bool{"K-MODEL": true, "K-MODEL": false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.CreateProviderConnection(ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{
				OrganizationId: sc.OrganizationID, ProviderId: "custom", Name: "custom",
				BaseUrl: "https://custom.example", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY,
				ApiKey: ptr("key"), Configuration: serviceTierConfig(tc.models, nil),
			}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("create error = %v, want invalid argument", err)
			}
			ids, err := m.store.ListPrivate(ctx, sc, string(providerKind))
			if err != nil || len(ids) != 0 {
				t.Fatalf("rejected create changed inventory: %v, %v", ids, err)
			}
		})
	}

	created, err := m.CreateProviderConnection(ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{
		OrganizationId: sc.OrganizationID, ProviderId: "custom", Name: "original",
		BaseUrl: "https://custom.example", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY,
		ApiKey: ptr("key"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Msg.Connection.Id
	for _, tc := range cases {
		t.Run("update "+tc.name, func(t *testing.T) {
			before, version, err := m.store.GetPrivateVersionForManagement(ctx, sc, string(providerKind), id)
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{
				OrganizationId: sc.OrganizationID, ConnectionId: id, Name: ptr("changed"),
				Configuration: serviceTierConfig(tc.models, nil),
			}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("update error = %v, want invalid argument", err)
			}
			after, afterVersion, err := m.store.GetPrivateVersionForManagement(ctx, sc, string(providerKind), id)
			if err != nil || !bytes.Equal(before, after) || version != afterVersion {
				t.Fatalf("rejected update changed bytes/version: %v, %q -> %q", err, version, afterVersion)
			}
			var saved providerData
			if err := json.Unmarshal(after, &saved); err != nil || saved.Metadata.Name != "original" {
				t.Fatalf("rejected update changed name: %v, %v", err, saved.Metadata)
			}
		})
	}
}

func TestProviderServiceTierPreservesMixedCaseFalse(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	no := false
	config := serviceTierConfig(map[string]bool{"WiRe-MoDeL": false}, &no)
	created, err := m.CreateProviderConnection(ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{
		OrganizationId: sc.OrganizationID, ProviderId: "custom", Name: "custom",
		BaseUrl: "https://custom.example", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY,
		ApiKey: ptr("key"), Configuration: config,
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Msg.Connection.Id
	check := func(label string, connection *v1.ProviderConnection) {
		t.Helper()
		policy := connection.GetConfiguration().GetPolicy()
		value, ok := policy.ModelSupportsServiceTier["WiRe-MoDeL"]
		if len(policy.GetModelSupportsServiceTier()) != 1 || !ok || value || !policy.GetSupportsServiceTier() || policy.FastEnabled == nil || *policy.FastEnabled {
			t.Fatalf("%s lost policy presence or spelling: %v", label, policy)
		}
	}
	check("create", created.Msg.Connection)
	updated, err := m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{
		OrganizationId: sc.OrganizationID, ConnectionId: id, Configuration: config,
	}))
	if err != nil {
		t.Fatal(err)
	}
	check("update", updated.Msg.Connection)
	listed, err := m.ListProviderConnections(ctx, connect.NewRequest(&v1.ListProviderConnectionsRequest{OrganizationId: sc.OrganizationID}))
	if err != nil || len(listed.Msg.Connections) != 1 {
		t.Fatalf("list: %v, %v", err, listed)
	}
	check("list", listed.Msg.Connections[0])
	var saved providerData
	if err := m.get(ctx, sc, providerKind, id, &saved); err != nil {
		t.Fatal(err)
	}
	check("storage", saved.Metadata)
	raw, err := providerPolicyOverrides(saved)
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Models map[string]bool `json:"modelSupportsServiceTier"`
		Fast   *bool           `json:"fastEnabled"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	value, ok := policy.Models["WiRe-MoDeL"]
	if len(policy.Models) != 1 || !ok || value || policy.Fast == nil || *policy.Fast {
		t.Fatalf("ProtoJSON lost explicit false or spelling: %s", raw)
	}
	assertModelFalse := func(label string) {
		t.Helper()
		got := fastModelEvidence(saved, "wire-model", time.Now())
		if got == nil || *got {
			t.Fatalf("%s lost folded model false: %v", label, got)
		}
	}
	assertModelFalse("Fast disabled")
	// A model-level false must still win when the optional Fast toggle is enabled or omitted.
	for _, fast := range []*bool{ptr(true), nil} {
		saved.Metadata.Configuration.Policy.FastEnabled = fast
		assertModelFalse("Fast toggle changed")
	}
	// Configuration omission keeps the stored policy; reset clears it.
	if _, err := m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: id, Name: ptr("renamed")})); err != nil {
		t.Fatal(err)
	}
	if err := m.get(ctx, sc, providerKind, id, &saved); err != nil {
		t.Fatal(err)
	}
	check("omitted configuration", saved.Metadata)
	if _, err := m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: id, ResetConfiguration: true})); err != nil {
		t.Fatal(err)
	}
	saved = providerData{}
	if err := m.get(ctx, sc, providerKind, id, &saved); err != nil || saved.Metadata.Configuration != nil {
		t.Fatalf("reset did not clear configuration: %v", err)
	}
	if err := validateProviderConfiguration("custom", nil); err != nil {
		t.Fatalf("nil configuration rejected: %v", err)
	}
	if err := validateProviderConfiguration("custom", &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{}}); err != nil {
		t.Fatalf("empty policy rejected: %v", err)
	}
}
