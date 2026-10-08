// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func desktopProofFixture(t *testing.T) (string, Credential, domain.ID, Lifecycle) {
	t.Helper()
	root, c := lifecycleFixture(t)
	client := c
	client.Type, client.DeviceID, client.MachineID = domain.ClientDevice, domain.NewID(), ""
	clientRoot := filepath.Join(filepath.Dir(root), "desktop-client")
	if err := security.PrivateDir(clientRoot); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(credentialPath(clientRoot), client); err != nil {
		t.Fatal(err)
	}
	status, _, err := PrepareDesktopStart(root, true, "")
	if err != nil {
		t.Fatal(err)
	}
	return root, c, client.DeviceID, status.Lifecycle
}
func recordedControllerFixture(t *testing.T, exited bool) (string, Credential, Lifecycle, []byte) {
	t.Helper()
	root, c, clientID, lifecycle := desktopProofFixture(t)
	if err := publishControllerEvidence(root, c, lifecycle, clientID); err != nil {
		t.Fatal(err)
	}
	if _, err := enterLifecycle(root, c, lifecycle.Generation); err != nil {
		t.Fatal(err)
	}
	if err := setPhase(root, c, lifecycle.Generation, RuntimeReady); err != nil {
		t.Fatal(err)
	}
	if exited {
		value, err := readControllerEvidence(root, c, lifecycle)
		if err != nil {
			t.Fatal(err)
		}
		// Synthetic PID reuse: the stored original birth differs from the fixture
		// process, which must never be terminated or adopted during admission.
		switch runtime.GOOS {
		case "linux":
			value.Original.Birth = "11111111-1111-1111-1111-111111111111:1"
		case "darwin":
			value.Original.Birth = "1.000001"
		default:
			value.Original.Birth = "1:1"
		}
		if err := writeJSON(evidencePath(value.Scope, lifecycle.Generation), value); err != nil {
			t.Fatal(err)
		}
	}
	scope, err := controllerScope(root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(evidencePath(scope, lifecycle.Generation))
	if err != nil {
		t.Fatal(err)
	}
	return root, c, lifecycle, raw
}
func TestDesktopControllerProofAdmitsOneReplacementAndPreservesHistory(t *testing.T) {
	root, _, original, proof := recordedControllerFixture(t, true)
	journal := filepath.Join(root, "synthetic-native-journal")
	if err := os.WriteFile(journal, []byte("accepted-input-remains-uncertain"), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, launch, err := PrepareDesktopStart(root, true, "")
			if err != nil {
				t.Error(err)
			}
			results <- launch
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for launch := range results {
		if launch {
			count++
		}
	}
	if count != 1 {
		t.Fatal("replacement count", count)
	}
	status, err := Status(root)
	if err != nil || status.Lifecycle.Generation == original.Generation || status.Lifecycle.Phase != RuntimeReserved {
		t.Fatal(status, err)
	}
	saved, err := os.ReadFile(evidencePath(root, original.Generation))
	if err != nil || string(saved) != string(proof) {
		t.Fatal("original proof changed", err)
	}
	if _, err := os.Stat(filepath.Join(root, "worker-lifecycle-history", string(original.Generation)+".json")); err != nil {
		t.Fatal(err)
	}
	saved, err = os.ReadFile(journal)
	if err != nil || string(saved) != "accepted-input-remains-uncertain" {
		t.Fatal("native journal changed", err)
	}
}
func TestDesktopControllerProofPreservesAlivePendingAndStop(t *testing.T) {
	root, c, original, _ := recordedControllerFixture(t, false)
	if _, launch, err := PrepareDesktopStart(root, true, ""); err == nil || launch {
		t.Fatal("live original replaced")
	}
	if err := RequestStop(root, original.Generation); err != nil {
		t.Fatal(err)
	}
	if _, launch, err := PrepareDesktopStart(root, false, original.Generation); err != nil || launch {
		t.Fatal("Ensure reopened Stop", err)
	}
	if _, launch, err := PrepareDesktopStart(root, true, ""); err == nil || launch {
		t.Fatal("uncertain Stop reopened without exit")
	}
	root, c, clientID, pending := desktopProofFixture(t)
	if err := publishControllerEvidence(root, c, pending, clientID); err != nil {
		t.Fatal(err)
	}
	if status, launch, err := PrepareDesktopStart(root, true, ""); err != nil || launch || status.Lifecycle.Generation != pending.Generation {
		t.Fatal("pending publication replaced", status, err)
	}
}
func TestDesktopControllerProofLegacyAndInvalidRemainBlocked(t *testing.T) {
	for _, kind := range []string{"legacy", "missing", "malformed", "foreign-generation", "foreign-client", "foreign-scope", "foreign-machine", "foreign-device", "foreign-server", "version", "invalid-birth", "public-file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root, c, original, _ := recordedControllerFixture(t, true)
			path := evidencePath(root, original.Generation)
			switch kind {
			case "legacy", "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "public-file":
				if runtime.GOOS == "windows" {
					t.Skip("Unix mode-bit fixture")
				}
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(path, []byte(`{"version":1,"unexpected":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if runtime.GOOS == "windows" {
					t.Skip("symlink privilege fixture is Unix-only")
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "worker-lifecycle.json"), path); err != nil {
					t.Fatal(err)
				}
			default:
				value, err := readControllerEvidence(root, c, original)
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "foreign-generation":
					value.Generation = domain.NewID()
				case "foreign-client":
					value.DesktopClientID = domain.NewID()
				case "foreign-scope":
					value.Scope = filepath.Dir(root)
				case "foreign-machine":
					value.MachineID = domain.NewID()
				case "foreign-device":
					value.DeviceID = domain.NewID()
				case "foreign-server":
					value.ServerID = domain.NewID()
				case "version":
					value.Version = 2
				case "invalid-birth":
					value.Original.Birth = "malformed"
				}
				if err := writeJSON(path, value); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(filepath.Join(root, "worker-lifecycle.json"))
			if err != nil {
				t.Fatal(err)
			}
			if _, launch, err := PrepareDesktopStart(root, true, ""); err == nil || launch {
				t.Fatal("invalid proof admitted replacement")
			}
			after, err := os.ReadFile(filepath.Join(root, "worker-lifecycle.json"))
			if err != nil || string(before) != string(after) {
				t.Fatal("blocked lifecycle changed", err)
			}
		})
	}
}
func TestDesktopControllerProofPublicationPrecedesAdmission(t *testing.T) {
	root, c, clientID, reserved := desktopProofFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admitted := false
	_ = Run(ctx, Config{Root: root, StartupID: reserved.Generation, DesktopClientID: clientID, Admitted: func(current Lifecycle) {
		admitted = true
		value, err := readControllerEvidence(root, c, current)
		if err != nil || value.Generation != reserved.Generation {
			t.Fatal("admitted before proof", err)
		}
		if current.Phase != RuntimeStarting {
			t.Fatal("admission phase", current.Phase)
		}
		cancel()
	}})
	if !admitted {
		t.Fatal("not admitted")
	}
}
func TestDesktopControllerProofPublicationFailureKeepsReservation(t *testing.T) {
	root, _, clientID, reserved := desktopProofFixture(t)
	if err := os.WriteFile(filepath.Join(root, "worker-controller-evidence"), []byte("foreign-entry"), 0600); err != nil {
		t.Fatal(err)
	}
	admitted := false
	err := Run(context.Background(), Config{Root: root, StartupID: reserved.Generation, DesktopClientID: clientID, Admitted: func(Lifecycle) { admitted = true }})
	if err == nil || admitted {
		t.Fatal("publication failure admitted work", err)
	}
	status, err := Status(root)
	if err != nil || status.ControllerActive || status.Lifecycle.Generation != reserved.Generation || status.Lifecycle.Phase != RuntimeReserved {
		t.Fatal("failed publication changed reservation", status, err)
	}
}

func TestDesktopControllerProofDoesNotRewriteOriginalAdmission(t *testing.T) {
	root, c, clientID, reserved := desktopProofFixture(t)
	if err := publishControllerEvidence(root, c, reserved, clientID); err != nil {
		t.Fatal(err)
	}
	original, err := readControllerEvidence(root, c, reserved)
	if err != nil {
		t.Fatal(err)
	}
	original.DesktopClientID = domain.NewID()
	path := evidencePath(original.Scope, reserved.Generation)
	if err := writeJSON(path, original); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := publishControllerEvidence(root, c, reserved, clientID); err == nil {
		t.Fatal("foreign publication overwritten")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("original evidence changed", err)
	}
}
