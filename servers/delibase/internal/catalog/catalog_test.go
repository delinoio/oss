package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validCatalog = `{
  "version": 1,
  "apps": [
    {"id":"0198a000-0000-7000-8000-000000000001","slug":"app-one","name":"One","summary":"","description":"","icon_url":"","enabled":true},
    {"id":"0198a000-0000-7000-8000-000000000002","slug":"app-two","name":"Two","summary":"","description":"","icon_url":"","enabled":true}
  ],
  "meters": [
    {"id":"0198a000-0000-7000-8000-000000000011","app_id":"0198a000-0000-7000-8000-000000000001","key":"requests","name":"Requests","description":"","unit_name":"request","unit_precision":0,"reservation_ttl_seconds":60,"enabled":true},
    {"id":"0198a000-0000-7000-8000-000000000012","app_id":"0198a000-0000-7000-8000-000000000002","key":"tokens","name":"Tokens","description":"","unit_name":"token","unit_precision":0,"reservation_ttl_seconds":120,"enabled":true}
  ],
  "prices": [
    {"id":"0198a000-0000-7000-8000-000000000021","meter_id":"0198a000-0000-7000-8000-000000000011","usd_micros_per_unit":10,"effective_from":"2026-01-01T00:00:00Z","effective_until":null},
    {"id":"0198a000-0000-7000-8000-000000000022","meter_id":"0198a000-0000-7000-8000-000000000012","usd_micros_per_unit":20,"effective_from":"2026-01-01T00:00:00Z","effective_until":null}
  ],
  "services": [
    {"id":"0198a000-0000-7000-8000-000000000031","logto_client_id":"fixture-service","name":"Fixture","enabled":true,"allowed_meter_ids":["0198a000-0000-7000-8000-000000000011","0198a000-0000-7000-8000-000000000012"]}
  ],
  "polar_meters": [
    {"meter_id":"0198a000-0000-7000-8000-000000000011","polar_meter_id":"polar-one"},
    {"meter_id":"0198a000-0000-7000-8000-000000000012","polar_meter_id":"polar-two"}
  ]
}`

func TestParseValidCatalogWithTwoAppsAndMeters(t *testing.T) {
	t.Parallel()
	specification, err := parse([]byte(validCatalog))
	if err != nil {
		t.Fatal(err)
	}
	if len(specification.Apps) != 2 || len(specification.Meters) != 2 {
		t.Fatalf("catalog sizes = %d apps, %d meters", len(specification.Apps), len(specification.Meters))
	}
}

func TestParseRejectsIncompleteOrUnsafeCatalogs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		document string
	}{
		{name: "malformed", document: `{`},
		{name: "unknown field", document: strings.Replace(validCatalog, `"version": 1`, `"version": 1, "secret": "value"`, 1)},
		{name: "missing price", document: strings.Replace(validCatalog, `"prices": [`, `"prices_removed": [`, 1)},
		{name: "invalid precision", document: strings.Replace(validCatalog, `"unit_precision":0`, `"unit_precision":2`, 1)},
		{name: "negative TTL", document: strings.Replace(validCatalog, `"reservation_ttl_seconds":60`, `"reservation_ttl_seconds":-1`, 1)},
		{name: "negative price", document: strings.Replace(validCatalog, `"usd_micros_per_unit":10`, `"usd_micros_per_unit":-1`, 1)},
		{name: "price overflow", document: strings.Replace(validCatalog, `"usd_micros_per_unit":10`, `"usd_micros_per_unit":9223372036854775808`, 1)},
		{name: "duplicate app ID", document: strings.Replace(validCatalog, `000000000002","slug":"app-two`, `000000000001","slug":"app-two`, 1)},
		{name: "overlapping price dates", document: strings.Replace(strings.Replace(validCatalog, `"effective_from":"2026-01-01T00:00:00Z","effective_until":null}`, `"effective_from":"2026-01-01T00:00:00Z","effective_until":"2027-01-01T00:00:00Z"}`, 1), `"meter_id":"0198a000-0000-7000-8000-000000000012"`, `"meter_id":"0198a000-0000-7000-8000-000000000011"`, 1)},
		{name: "missing service mapping", document: strings.Replace(validCatalog, `"allowed_meter_ids":["0198a000-0000-7000-8000-000000000011","0198a000-0000-7000-8000-000000000012"]`, `"allowed_meter_ids":[]`, 1)},
		{name: "duplicate Polar mapping", document: strings.Replace(validCatalog, `"polar_meter_id":"polar-two"`, `"polar_meter_id":"polar-one"`, 1)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parse([]byte(test.document)); err == nil {
				t.Fatal("parse() succeeded")
			}
		})
	}
}

func TestLoadRejectsMissingAndOversizedFiles(t *testing.T) {
	t.Parallel()
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("Load() accepted a missing file")
	}
	path := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxDocumentSize+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted an oversized file")
	}
}
