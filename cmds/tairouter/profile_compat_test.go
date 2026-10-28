// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAsideAllAccountsSyncPreservesSeparateBackups(t *testing.T) {
	r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
	root := filepath.Join(r.Home, ".aside")
	_ = os.MkdirAll(root, 0700)
	_ = os.WriteFile(filepath.Join(root, "accounts.json"), []byte(`{"currentAccountId":2,"accounts":[{"id":1},{"id":2}]}`), 0600)
	for _, id := range []string{"1", "2"} {
		dir := filepath.Join(root, "u", id)
		_ = os.MkdirAll(dir, 0700)
		_ = os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"user":"`+id+`"}`), 0600)
	}
	d := testDisk(t)
	s, _ := d.Load()
	changes, e := PlanSettings(r, s, "aside", testProfile(), "key", []ClientModel{{ID: "v/m", Name: "M"}})
	if e != nil || len(changes) != 2 {
		t.Fatalf("accounts not enumerated: %v", e)
	}
	if e = d.Apply(&s, "aside", "test", changes, answer(true)); e != nil {
		t.Fatal(e)
	}
	if e = d.Restore(&s, "aside"); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"1", "2"} {
		b, _ := os.ReadFile(filepath.Join(root, "u", id, "models.json"))
		if string(b) != `{"user":"`+id+`"}` {
			t.Fatal("account backup mixed")
		}
	}
}
func TestHermesEnumeratesLiveNamedProfiles(t *testing.T) {
	r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
	root := filepath.Join(r.Home, ".hermes")
	for _, name := range []string{"work", "retired", "cache"} {
		dir := filepath.Join(root, "profiles", name)
		_ = os.MkdirAll(dir, 0700)
		if name != "cache" {
			_ = os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("identity"), 0600)
		}
	}
	_ = os.MkdirAll(filepath.Join(root, "profiles", ".deleted", "retired"), 0700)
	profiles, e := r.DiscoverProfiles("hermes")
	if e != nil || len(profiles) != 2 || profiles[1].ID != "work" {
		t.Fatalf("wrong Hermes profiles: %#v %v", profiles, e)
	}
	changes, e := PlanSettings(r, emptyState(), "hermes", testProfile(), "KEY", []ClientModel{{ID: "v/m", Name: "M"}})
	if e != nil || len(changes) != 2 {
		t.Fatalf("named profiles not planned: %v", e)
	}
	r.Env["HERMES_HOME"] = filepath.Join(root, "profiles", "work")
	profiles, e = r.DiscoverProfiles("hermes")
	if e != nil || len(profiles) != 1 {
		t.Fatal("selected named profile duplicated")
	}
}
func TestKiloRebindsOwnedCandidateAfterPriorityChange(t *testing.T) {
	r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
	dir := filepath.Join(r.Home, ".config", "kilo")
	_ = os.MkdirAll(dir, 0700)
	old := filepath.Join(dir, "config.json")
	original := []byte(`{"user":true}`)
	_ = os.WriteFile(old, original, 0600)
	d := testDisk(t)
	s, _ := d.Load()
	models := []ClientModel{{ID: "v/m", Name: "M"}}
	changes, e := PlanSettings(r, s, "kilo", testProfile(), "KEY", models)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Apply(&s, "kilo", "test", changes, answer(true)); e != nil {
		t.Fatal(e)
	}
	newPath := filepath.Join(dir, "kilo.jsonc")
	_ = os.WriteFile(newPath, []byte("{ // newly created\n}\n"), 0600)
	next, e := PlanSettings(r, s, "kilo", testProfile(), "KEY", models)
	if e != nil || next[0].Path != old {
		t.Fatalf("owned path not rebound: %v", e)
	}
	if e = d.Restore(&s, "kilo"); e != nil {
		t.Fatal(e)
	}
	content, _ := os.ReadFile(newPath)
	if !bytes.Contains(content, []byte("newly created")) {
		t.Fatal("competing candidate modified")
	}
	fresh, e := PlanSettings(r, s, "kilo", testProfile(), "KEY", models)
	if e != nil || fresh[0].Path != newPath {
		t.Fatal("priority discovery not resumed")
	}
}
func TestDroidDerivedIDsAllowRestoreButProtectEndpoint(t *testing.T) {
	for _, editEndpoint := range []bool{false, true} {
		r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
		d := testDisk(t)
		s, _ := d.Load()
		changes, e := PlanSettings(r, s, "droid", testProfile(), "key", []ClientModel{{ID: "v/m", Name: "M"}})
		if e != nil {
			t.Fatal(e)
		}
		if e = d.Apply(&s, "droid", "test", changes, answer(true)); e != nil {
			t.Fatal(e)
		}
		doc, _ := parseDocument(changes[0].After, JSON)
		row := doc["customModels"].([]any)[0].(map[string]any)
		row["id"] = "factory-generated"
		row["index"] = float64(3)
		if editEndpoint {
			row["baseUrl"] = "https://foreign.test/v1"
		}
		b, _ := json.MarshalIndent(doc, "", "    ")
		_ = os.WriteFile(changes[0].Path, b, 0600)
		e = d.Restore(&s, "droid")
		if editEndpoint {
			if e != ErrConflict || !exists(changes[0].Path) {
				t.Fatal("protected endpoint overwritten")
			}
		} else if e != nil || exists(changes[0].Path) {
			t.Fatalf("derived metadata blocked restore: %v", e)
		}
	}
}
func TestClineSelectionSurvivesRefreshAndForeignDataSurvivesRestore(t *testing.T) {
	r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
	d := testDisk(t)
	s, _ := d.Load()
	models := []ClientModel{{ID: "v/m", Name: "M"}, {ID: "v/other", Name: "Other"}}
	changes, e := PlanSettings(r, s, "cline", testProfile(), "key", models)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Apply(&s, "cline", "test", changes, answer(true)); e != nil {
		t.Fatal(e)
	}
	doc, _ := parseDocument(changes[0].After, JSON)
	row := mapAt(doc, "providers", providerID)
	row["updatedAt"] = "2026-10-27T00:00:00Z"
	mapAt(row, "settings")["model"] = "v/other"
	doc["userAdded"] = object{"secret": "keep"}
	b, _ := json.MarshalIndent(doc, "", "  ")
	_ = os.WriteFile(changes[0].Path, b, 0600)
	next, e := PlanSettings(r, s, "cline", testProfile(), "key", models)
	if e != nil {
		t.Fatal(e)
	}
	nextDoc, _ := parseDocument(next[0].After, JSON)
	if mapAt(nextDoc, "providers", providerID, "settings")["model"] != "v/other" {
		t.Fatal("valid selection lost")
	}
	if e = d.Apply(&s, "cline", "test", next, answer(true)); e != nil {
		t.Fatal(e)
	}
	if e = d.Restore(&s, "cline"); e != nil {
		t.Fatal(e)
	}
	restored, _, e := readConfig(changes[0].Path)
	if e != nil {
		t.Fatal(e)
	}
	result, _ := parseDocument(restored, JSON)
	if mapAt(result, "userAdded")["secret"] != "keep" || mapAt(result, "providers", providerID) != nil {
		t.Fatal("foreign data lost or owned provider retained")
	}
}
func TestRefreshRetainsImmutableOriginalAndForeignRestorationBaseline(t *testing.T) {
	r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
	d := testDisk(t)
	s, _ := d.Load()
	path, _ := r.Resolve("pi")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	original := []byte("{\"user\":1}\n")
	_ = os.WriteFile(path, original, 0600)
	models := []ClientModel{{ID: "v/m", Name: "M"}}
	first, e := PlanSettings(r, s, "pi", testProfile(), "key", models)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Apply(&s, "pi", "test", first, answer(true)); e != nil {
		t.Fatal(e)
	}
	edited := bytes.Replace(first[0].After, []byte(`"user":1`), []byte(`"user":2`), 1)
	_ = os.WriteFile(path, edited, 0600)
	next, e := PlanSettings(r, s, "pi", testProfile(), "key", models)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Apply(&s, "pi", "test", next, answer(true)); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(s.Records["pi"].Originals[path].Before, original) {
		t.Fatal("original backup replaced by refresh")
	}
	if e = d.Restore(&s, "pi"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{\"user\":2}\n" {
		t.Fatalf("foreign edit lost: %s", b)
	}
}
func TestHermesWindowsDefaultUsesLocalAppData(t *testing.T) {
	home := realTemp(t)
	local := filepath.Join(realTemp(t), "Local")
	r := Resolver{Home: home, OS: "windows", Env: map[string]string{"LOCALAPPDATA": local}}
	p, e := r.Resolve("hermes")
	if e != nil || p != filepath.Join(local, "hermes", "config.yaml") {
		t.Fatalf("wrong Windows Hermes root: %q %v", p, e)
	}
}
func TestClineCatalogUnknownFieldsStayProtected(t *testing.T) {
	r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
	d := testDisk(t)
	s, _ := d.Load()
	changes, e := PlanSettings(r, s, "cline", testProfile(), "key", []ClientModel{{ID: "v/m", Name: "M"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Apply(&s, "cline", "test", changes, answer(true)); e != nil {
		t.Fatal(e)
	}
	doc, _ := parseDocument(changes[1].After, JSON)
	mapAt(doc, "providers", providerID)["updatedAt"] = "not a catalog-derived field"
	b, _ := json.Marshal(doc)
	_ = os.WriteFile(changes[1].Path, b, 0600)
	if e = d.Restore(&s, "cline"); e != ErrConflict {
		t.Fatalf("catalog unknown field bypassed protection: %v", e)
	}
}
