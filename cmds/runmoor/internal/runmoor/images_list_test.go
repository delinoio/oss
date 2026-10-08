package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestOfflineImageListPreservesCommittedImagesAndArtifacts(t *testing.T) {
	c, store := fixtureStore(t)
	id, stagedID := newID(), newID()
	installation := store.View().Installation
	name := "rm-image-" + stagedID
	if err := claimVM(c, name, installation, stagedID); err != nil {
		t.Fatal(err)
	}
	home, lock, err := prepareTartCreationHome(c, stagedID)
	if err != nil {
		t.Fatal(err)
	}
	createFixtureTartVM(t, c, home, creationVMName(stagedID))
	stage := tartCreationVMPath(c, stagedID)
	if err = publishVMOwnerMarkerAt(c, stage, name, installation, stagedID); err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	stageInfo, err := os.Stat(stage)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Update(func(s *Snapshot) error {
		s.Images[id] = &Image{ID: id, VM: "rm-image-" + id, Phase: ImageSealed, Problem: problem(ErrPreparation, "Pending preparation diagnosis.", "Inspect manager status.")}
		s.Images[stagedID] = &Image{ID: stagedID, VM: name, Phase: ImagePreparing}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	archive := importArchivePath(c, id)
	download := filepath.Join(artifactDirectory(c, id), "download")
	if err = os.MkdirAll(filepath.Dir(download), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{archive, download} {
		if err = os.WriteFile(path, []byte("interrupted image artifact"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := store.View()
	live := (&Manager{Store: store}).Control(context.Background(), ControlRequest{Action: "images"})
	configPath := filepath.Join(filepath.Dir(c.Storage.State), "config.toml")
	configBytes, err := toml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, configBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		var out, errs bytes.Buffer
		if code := Execute([]string{"--config", configPath, "image", "list"}, &out, &errs); code != 0 {
			t.Fatalf("list exit %d: %s", code, errs.String())
		}
		var response ControlResponse
		if err = json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(response, live) {
			t.Fatalf("offline output differs from live snapshot: %s", out.String())
		}
		if !sort.SliceIsSorted(response.Images, func(i, j int) bool { return response.Images[i].ID < response.Images[j].ID }) {
			t.Fatal("unordered images")
		}
	}
	for _, path := range []string{archive, download} {
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != "interrupted image artifact" {
			t.Fatalf("listing changed artifact: %q %v", data, readErr)
		}
	}
	afterInfo, err := os.Stat(stage)
	if err != nil || !os.SameFile(stageInfo, afterInfo) {
		t.Fatalf("listing changed staged directory: %v", err)
	}
	if _, err = os.Stat(vmPath(c, name)); !os.IsNotExist(err) {
		t.Fatalf("listing promoted staged VM: %v", err)
	}
	reopened, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reflect.DeepEqual(before, reopened.View()) {
		t.Fatal("listing changed committed lifecycle state")
	}
	driver, _ := fakeTart(c)
	images := &ImageManager{Store: reopened, Tart: driver}
	if err = images.Reconcile(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{archive, filepath.Dir(download), stage} {
		if _, err = os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("manager did not clean or promote artifact: %v", err)
		}
	}
	promotedInfo, err := os.Stat(vmPath(c, name))
	if err != nil || !os.SameFile(stageInfo, promotedInfo) {
		t.Fatalf("manager did not promote original directory: %v", err)
	}
	if err = verifyVMOwner(c, name, installation, stagedID); err != nil {
		t.Fatal(err)
	}
}
