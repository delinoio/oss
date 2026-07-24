package migrations

import (
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrationsAreOrdered(t *testing.T) {
	t.Parallel()
	ordered, err := load(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 6 {
		t.Fatalf("migration count = %d, want 6", len(ordered))
	}
	for index, item := range ordered {
		want := int64(index + 1)
		if item.version != want {
			t.Fatalf("migration %d version = %d, want %d", index, item.version, want)
		}
	}
}

func TestMigrationNamesAndVersionsFailClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source fstest.MapFS
	}{
		{
			name: "invalid filename",
			source: fstest.MapFS{
				"migration.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			},
		},
		{
			name: "duplicate version",
			source: fstest.MapFS{
				"000001_first.sql":  &fstest.MapFile{Data: []byte("SELECT 1;")},
				"000001_second.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			},
		},
		{
			name: "version gap",
			source: fstest.MapFS{
				"000002_second.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
			},
		},
		{
			name:   "empty set",
			source: fstest.MapFS{},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := load(test.source); err == nil {
				t.Fatal("load() succeeded")
			}
		})
	}
}
