package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestDiagnosticStorageReportsSeparateMeasurementsWithoutEvents(t *testing.T) {
	s, root := openTest(t)
	create(t, s, domain.NewID(), "fixture")
	_, before, err := s.Snapshot(context.Background(), Filter{Kind: domain.ProjectKind, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.DiagnosticStorage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Result.State != domain.DiagnosticObserved || result.DatabaseBytes == nil || *result.DatabaseBytes == 0 || result.LogicalDatabaseBytes == nil || *result.LogicalDatabaseBytes == 0 || result.WALBytes == nil || result.VolumeAvailableBytes == nil || result.VolumeCapacityBytes == nil || *result.VolumeAvailableBytes > *result.VolumeCapacityBytes {
		t.Fatalf("incomplete measurement: %+v", result)
	}
	found := false
	for _, count := range result.Resources {
		if count.Kind == domain.ProjectKind {
			found = count.Count == 1
		}
	}
	if !found {
		t.Fatal("retained resource count missing")
	}
	large := uint64(9007199254740993)
	result.DatabaseBytes = &large
	raw, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), `"database_bytes":"9007199254740993"`) {
		t.Fatal("large capacity lost decimal precision")
	}
	_, after, err := s.Snapshot(context.Background(), Filter{Kind: domain.ProjectKind, Limit: 10})
	if err != nil || before != after {
		t.Fatal("diagnostic changed database events")
	}
	if runtime.GOOS != "windows" {
		path := filepath.Join(root, "state.sqlite")
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0600)
		partial, err := s.DiagnosticStorage(context.Background())
		if err != nil || partial.LogicalDatabaseBytes == nil || partial.DatabaseBytes == nil || partial.VolumeAvailableBytes == nil {
			t.Fatal("shared permissions blocked actual storage observation", err)
		}
	}
}
