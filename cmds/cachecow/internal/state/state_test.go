package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsVersionAndIsolation(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "private"))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetSource("one", "/source"); e != nil {
		t.Fatal(e)
	}
	v, e := s.Settings("one")
	if e != nil || v.Source != "/source" {
		t.Fatal(v, e)
	}
	v, e = s.Settings("two")
	if e != nil || v.Source != "" {
		t.Fatal(v, e)
	}
	p := s.settingsPath("one")
	if e = Write(p, Settings{SchemaVersion: 2, Source: "keep"}); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(p)
	if e = s.SetSource("one", ""); e == nil {
		t.Fatal("rewrote unknown schema")
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("modified unsupported settings")
	}
}
