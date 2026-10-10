// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func imageViewInput(t *testing.T) domain.ExecutionJobInput {
	t.Helper()
	session, machine := domain.NewID(), domain.NewID()
	p := PrepareRequest{SessionID: session, MachineID: machine, Type: domain.GeneralChat, Repositories: []RepositorySpec{}}
	raw, _ := json.Marshal(p)
	digest := sha256.Sum256(raw)
	m := Manifest{Version: 1, SessionID: session, MachineID: machine, Type: domain.GeneralChat, State: Ready, InputDigest: hex.EncodeToString(digest[:]), PrimaryPath: "/original/workspaces/" + string(session) + "/chat", Repositories: []PreparedRepository{}, CreatedAt: time.Now().UTC()}
	manifest, _ := json.Marshal(m)
	return domain.ExecutionJobInput{SessionID: session, MachineID: machine, Preparation: raw, Manifest: manifest}
}

func TestImageViewReferenceUsesOriginalLocalRepositoryAndWorkerPathGrammar(t *testing.T) {
	for _, workerOS := range []string{"linux", "darwin", "windows"} {
		t.Run(workerOS, func(t *testing.T) {
			input := imageViewInput(t)
			paths := []string{"/source/one", "/source/two"}
			if workerOS == "windows" {
				paths = []string{`C:\source\one`, `C:\source\two`}
			}
			p := PrepareRequest{SessionID: input.SessionID, MachineID: input.MachineID, OriginMachineID: input.MachineID, Type: domain.Local}
			m := Manifest{Version: 1, SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.Local, State: Ready, CreatedAt: time.Now().UTC(), PrimaryPath: paths[0]}
			for _, root := range paths {
				id := domain.NewID()
				commit := strings.Repeat("b", 40)
				p.Repositories = append(p.Repositories, RepositorySpec{ID: id, Checkout: root})
				m.Repositories = append(m.Repositories, PreparedRepository{ID: id, Source: root, Path: root, LocalIdentityDigest: strings.Repeat("a", 64), Starting: domain.Reference{Type: domain.CommitReference, Name: commit}, StartingCommit: commit, BaseCommit: commit})
			}
			p.PrimaryRepository = p.Repositories[0].ID
			input.Preparation, _ = json.Marshal(p)
			digest := sha256.Sum256(input.Preparation)
			m.InputDigest = hex.EncodeToString(digest[:])
			input.Manifest, _ = json.Marshal(m)
			location := paths[1] + "/images/original.png"
			if workerOS == "windows" {
				location = paths[1] + `\images\original.png`
			}
			ref, err := ObserveImageViewLocation(input, workerOS, location, domain.NewID())
			if err != nil || ref.RepositoryID != p.Repositories[1].ID || ref.Location != "images/original.png" || ValidateImageViewReference(input, workerOS, ref) != nil {
				t.Fatalf("original repository/path grammar lost: %v", err)
			}

			if workerOS == "windows" {
				for _, bad := range []string{paths[1] + `\images\frame:01.png`, paths[1] + `\images\C:foreign.png`, `C:source\two\image.png`, `D:\source\two\image.png`, `\\server\share\image.png`, paths[1] + `\..\foreign.png`, paths[1] + `-sibling\image.png`, paths[1] + `\images\\image.png`} {
					if _, err := ObserveImageViewLocation(input, workerOS, bad, domain.NewID()); err == nil {
						t.Fatal("Windows malformed or foreign location admitted", bad)
					}
				}
				forged := ref
				forged.Location = `images\original.png`
				if ValidateImageViewReference(input, workerOS, forged) == nil {
					t.Fatal("unnormalized Windows metadata admitted")
				}
			}
		})
	}
}

func TestImageViewReferenceRejectsForeignScopeAndOwnership(t *testing.T) {
	input := imageViewInput(t)
	var manifest Manifest
	_ = json.Unmarshal(input.Manifest, &manifest)
	original := manifest.PrimaryPath + "/images/original.png"
	ref, err := ObserveImageViewLocation(input, "linux", original, domain.NewID())
	if err != nil || ref.Location != "images/original.png" || ValidateImageViewReference(input, "linux", ref) != nil {
		t.Fatalf("original missing-file metadata failed: %v", err)
	}
	for _, bad := range []string{"/outside/foreign.png", manifest.PrimaryPath + "/../foreign.png", manifest.PrimaryPath + "/images//original.png", "https://example.com/image.png", "file://" + original, "relative.png", manifest.PrimaryPath} {
		if _, err := ObserveImageViewLocation(input, "linux", bad, domain.NewID()); err == nil {
			t.Fatalf("unscoped image observation: %s", bad)
		}
	}
	for _, change := range []func(*domain.ImageViewObservation){func(v *domain.ImageViewObservation) { v.MachineID = domain.NewID() }, func(v *domain.ImageViewObservation) { v.RepositoryID = domain.NewID() }, func(v *domain.ImageViewObservation) { v.ManifestDigest = hex.EncodeToString(make([]byte, 32)) }, func(v *domain.ImageViewObservation) { v.Location = "../foreign" }} {
		next := ref
		change(&next)
		if ValidateImageViewReference(input, "linux", next) == nil {
			t.Fatal("changed original reference accepted")
		}
	}
	input.Manifest = append(input.Manifest, ' ')
	if ValidateImageViewReference(input, "linux", ref) == nil {
		t.Fatal("different manifest generation adopted original reference")
	}
}

func TestImageViewReferencePreservesPOSIXFilenameCharacters(t *testing.T) {
	for _, workerOS := range []string{"linux", "darwin"} {
		for _, location := range []string{"screens/frame:01.png", `screens/frame\01.png`, `screens/frame:01\raw.png`} {
			input := imageViewInput(t)
			var manifest Manifest
			_ = json.Unmarshal(input.Manifest, &manifest)
			ref, err := ObserveImageViewLocation(input, workerOS, manifest.PrimaryPath+"/"+location, domain.NewID())
			if err != nil || ref.Location != location || ValidateImageViewReference(input, workerOS, ref) != nil {
				t.Fatal("original POSIX location rejected or altered", workerOS, location, err)
			}
			raw, _ := json.Marshal(ref)
			var retained domain.ImageViewObservation
			if domain.Decode(raw, &retained) != nil || retained != ref || ValidateImageViewReference(input, workerOS, retained) != nil {
				t.Fatal("server metadata round trip lost original POSIX reference")
			}
			if _, err := ObserveImageViewLocation(input, workerOS, manifest.PrimaryPath+"-sibling/"+location, domain.NewID()); err == nil {
				t.Fatal("sibling root admitted")
			}
		}
	}
}
