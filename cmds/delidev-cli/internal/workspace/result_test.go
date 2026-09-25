package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestRemoteManifestValidationBindsEveryPreparationSelection(t *testing.T) {
	request, _ := requestFor("/checkout/one")
	request.Repositories = append(request.Repositories, RepositorySpec{ID: domain.NewID(), Checkout: "/checkout/two", Starting: domain.Reference{Type: domain.LocalBranch, Name: "second"}})
	request.PrimaryRepository = request.Repositories[1].ID
	raw, _ := json.Marshal(request)
	digest := sha256.Sum256(raw)
	manifest := Manifest{Version: 1, SessionID: request.SessionID, MachineID: request.MachineID, Type: request.Type, State: Ready, InputDigest: hex.EncodeToString(digest[:]), CreatedAt: time.Now().UTC()}
	for _, repo := range request.Repositories {
		manifest.Repositories = append(manifest.Repositories, PreparedRepository{ID: repo.ID, Source: repo.Checkout, Path: "/worker/workspaces/" + string(request.SessionID) + "/" + string(repo.ID), Starting: repo.Starting, Base: repo.Starting, BaseCommit: strings.Repeat("a", 40), StartingCommit: strings.Repeat("a", 40), Owned: true})
	}
	manifest.PrimaryPath = manifest.Repositories[1].Path
	if err := ValidateResult(request, manifest, "linux"); err != nil {
		t.Fatal(err)
	}
	variants := map[string]func(*Manifest){
		"session":            func(m *Manifest) { m.SessionID = domain.NewID() },
		"machine":            func(m *Manifest) { m.MachineID = domain.NewID() },
		"digest":             func(m *Manifest) { m.InputDigest = strings.Repeat("0", 64) },
		"partial":            func(m *Manifest) { m.State = Preparing },
		"missing repository": func(m *Manifest) { m.Repositories = m.Repositories[:1] },
		"order":              func(m *Manifest) { m.Repositories[0], m.Repositories[1] = m.Repositories[1], m.Repositories[0] },
		"source":             func(m *Manifest) { m.Repositories[0].Source = "/another/checkout" },
		"reference":          func(m *Manifest) { m.Repositories[0].Starting.Name = "other" },
		"commit":             func(m *Manifest) { m.Repositories[0].StartingCommit = strings.Repeat("A", 40) },
		"base commit":        func(m *Manifest) { m.Repositories[0].BaseCommit = strings.Repeat("b", 40) },
		"foreign path":       func(m *Manifest) { m.Repositories[0].Path = "/unowned" },
		"other root": func(m *Manifest) {
			m.Repositories[0].Path = "/other/workspaces/" + string(request.SessionID) + "/" + string(request.Repositories[0].ID)
		},
		"primary":   func(m *Manifest) { m.PrimaryPath = m.Repositories[0].Path },
		"ownership": func(m *Manifest) { m.Repositories[0].Owned = false },
	}
	for name, change := range variants {
		t.Run(name, func(t *testing.T) {
			copy := manifest
			copy.Repositories = append([]PreparedRepository{}, manifest.Repositories...)
			change(&copy)
			if err := ValidateResult(request, copy, "linux"); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("unproven result did not retain uncertainty")
			}
		})
	}
	// Cross-platform publication uses the Worker's path grammar, not runtime.GOOS.
	windows := manifest
	windows.Repositories = append([]PreparedRepository{}, manifest.Repositories...)
	windowsRequest := request
	windowsRequest.Repositories = append([]RepositorySpec{}, request.Repositories...)
	for i := range windows.Repositories {
		windows.Repositories[i].Path = "C:" + strings.ReplaceAll(windows.Repositories[i].Path, "/", "\\")
		windows.Repositories[i].Source = "C:" + strings.ReplaceAll(windows.Repositories[i].Source, "/", "\\")
		windowsRequest.Repositories[i].Checkout = windows.Repositories[i].Source
	}
	windows.PrimaryPath = windows.Repositories[1].Path
	raw, _ = json.Marshal(windowsRequest)
	digest = sha256.Sum256(raw)
	windows.InputDigest = hex.EncodeToString(digest[:])
	if err := ValidateResult(windowsRequest, windows, "windows"); err != nil {
		t.Fatalf("Windows manifest rejected on %s: %v", runtime.GOOS, err)
	}
}
