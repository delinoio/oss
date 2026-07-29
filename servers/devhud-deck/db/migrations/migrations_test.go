package migrations

import (
	"testing"
	"testing/fstest"
)

func TestLoadRequiresContiguousOrderedMigrations(t *testing.T) {
	t.Parallel()
	ordered, err := load(fstest.MapFS{
		"000001_first.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000002_next.sql":  &fstest.MapFile{Data: []byte("SELECT 2;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 2 || ordered[0].version != 1 || ordered[1].version != 2 {
		t.Fatalf("unexpected order: %#v", ordered)
	}
	if _, err := load(fstest.MapFS{
		"000001_first.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000003_gap.sql":   &fstest.MapFile{Data: []byte("SELECT 3;")},
	}); err == nil {
		t.Fatal("expected migration gap rejection")
	}
}
