package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"testing"
)

func TestImageSealRejectsInvalidAutomaticPathsBeforeSideEffects(t *testing.T) {
	for _, version := range []string{"", LatestRunner} {
		for _, path := range []string{"/", "/runner", "/Users//runner/actions-runner", "/Users/runner/./actions-runner", "/Users/runner/actions-runner/"} {
			for _, open := range []bool{false, true} {
				phase := ImagePreparing
				if open {
					phase = ImageOpen
				}
				t.Run(version+"/"+path+"/"+string(phase), func(t *testing.T) {
					c, store := fixtureStore(t)
					driver, fixture := fakeTart(c)
					images := &ImageManager{Store: store, Tart: driver, Client: &http.Client{Transport: runnerReleaseTransport(func(*http.Request) (*http.Response, error) {
						t.Fatal("invalid automatic path requested runner release metadata")
						return nil, nil
					})}}
					im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
					if err != nil {
						t.Fatal(err)
					}
					if open {
						if _, err = images.Operate(context.Background(), c, ImageRequest{Action: "open", ID: im.ID}); err != nil {
							t.Fatal(err)
						}
					}
					before := store.View()
					resources, count, vms := usage(before)
					firstCommand := len(fixture.commands)
					_, err = images.Operate(context.Background(), c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: version, RunnerPath: path})
					requireCode(t, err, ErrConfig)
					after := store.View()
					if fingerprint(after) != fingerprint(before) {
						t.Fatal("invalid automatic path changed image state, process identity or reservations")
					}
					if gotResources, gotCount, gotVMs := usage(after); gotResources != resources || gotCount != count || gotVMs != vms {
						t.Fatalf("invalid automatic path changed capacity: resources=%+v count=%d vms=%d", gotResources, gotCount, gotVMs)
					}
					for _, args := range fixture.commands[firstCommand:] {
						if args[0] != "--version" {
							t.Fatalf("invalid automatic path reached a VM command: %v", args)
						}
					}
					if fixture.running[im.VM] != open {
						t.Fatal("invalid automatic path changed the VM running state")
					}
				})
			}
		}
	}
}

func TestImageSealAutomaticPathsPreserveDefaultAndDedicatedDirectories(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("archive-backed Tart sealing fixture requires Darwin or Linux filesystem semantics")
	}
	for _, version := range []string{"", LatestRunner} {
		for _, path := range []string{"", "/Users/runner/actions-runner", "/opt/actions-runner", "/Users/runner/actions runner"} {
			t.Run(version+"/"+path, func(t *testing.T) {
				c, store := fixtureStore(t)
				driver, fixture := fakeTart(c)
				release, archive := fixtureRelease(t, Tart, "arm64")
				metadata, err := json.Marshal([]RunnerRelease{release})
				if err != nil {
					t.Fatal(err)
				}
				requests := 0
				client := &http.Client{Transport: runnerReleaseTransport(func(req *http.Request) (*http.Response, error) {
					requests++
					var body []byte
					switch req.URL.String() {
					case "https://api.github.com/repos/actions/runner/releases?per_page=100":
						body = metadata
					case release.Assets[0].URL:
						body = archive
					default:
						t.Fatalf("unexpected runner release request: %s", req.URL)
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
				})}
				images := &ImageManager{Store: store, Tart: driver, Client: client}
				im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
				if err != nil {
					t.Fatal(err)
				}
				im, err = images.Operate(context.Background(), c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: version, RunnerPath: path})
				if err != nil {
					t.Fatal(err)
				}
				want := path
				if want == "" {
					want = "/Users/runner/actions-runner"
				}
				if im.Phase != ImageSealed || im.RunnerPath != want || im.RunnerVersion != release.Version() || im.Digest == "" || im.Problem != nil {
					t.Fatalf("automatic sealing lost its path, release or sealed metadata: %+v", im)
				}
				snapshot := store.View()
				if resources, count, vms := usage(snapshot); resources != (Resources{}) || count != 0 || vms != 0 || snapshot.ImageTartPIDs[im.ID] != 0 || snapshot.ImageTartStarts[im.ID] != "" || fixture.running[im.VM] {
					t.Fatal("automatic sealing retained VM capacity or process identity")
				}
				if requests != 2 {
					t.Fatalf("automatic sealing made %d requests instead of metadata and archive requests", requests)
				}
			})
		}
	}
}

func TestImageSealRejectsInvalidRequestBeforePromotingCreatedVM(t *testing.T) {
	c, store := fixtureStore(t)
	driver, fixture := fakeTart(c)
	images := &ImageManager{Store: store, Tart: driver}
	im, err := images.Operate(context.Background(), c, ImageRequest{Action: "create", Name: "setup", IPSW: "/fixture.ipsw", Resources: Resources{1, 512}})
	if err != nil {
		t.Fatal(err)
	}

	_, lock, err := prepareTartCreationHome(c, im.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(vmPath(c, im.VM), tartCreationVMPath(c, im.ID)); err != nil {
		_ = lock.Close()
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupTartCreationHome(c, im.ID) })

	before := store.View()
	firstCommand := len(fixture.commands)
	_, err = images.Operate(context.Background(), c, ImageRequest{Action: "seal", ID: im.ID, RunnerVersion: LatestRunner, RunnerPath: "/"})
	requireCode(t, err, ErrConfig)
	if fingerprint(store.View()) != fingerprint(before) {
		t.Fatal("invalid automatic path changed image state or reservations")
	}
	if _, statErr := os.Lstat(vmPath(c, im.VM)); !os.IsNotExist(statErr) {
		t.Fatalf("invalid automatic path promoted the staged VM: %v", statErr)
	}
	if _, statErr := os.Lstat(tartCreationVMPath(c, im.ID)); statErr != nil {
		t.Fatalf("invalid automatic path lost the staged VM: %v", statErr)
	}
	for _, args := range fixture.commands[firstCommand:] {
		if len(args) == 0 || args[0] != "--version" {
			t.Fatalf("invalid automatic path reached VM preparation: %v", args)
		}
	}
}
