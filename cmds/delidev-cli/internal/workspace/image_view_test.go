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

func localImageViewInput(t *testing.T, roots ...string) domain.ExecutionJobInput {
	t.Helper()
	input := imageViewInput(t)
	p := PrepareRequest{SessionID: input.SessionID, MachineID: input.MachineID, OriginMachineID: input.MachineID, Type: domain.Local}
	m := Manifest{Version: 1, SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.Local, State: Ready, CreatedAt: time.Now().UTC(), PrimaryPath: roots[0]}
	for _, root := range roots {
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
	return input
}

func TestImageViewUNCRoundTripPreservesOriginalAuthority(t *testing.T) {
	for _, root := range []string{`\\server\share\repo`, "//server/share/repo", `\\Server\Share\Repo`} {
		t.Run(root, func(t *testing.T) {
			input := localImageViewInput(t, root)
			for _, location := range []string{root + `\images\original.png`, normalizeWorkerPath(root, "windows") + "/images/original.png"} {
				ref, err := ObserveImageViewLocation(input, "windows", location, domain.NewID())
				if err != nil || ref.Location != "images/original.png" || ValidateImageViewReference(input, "windows", ref) != nil {
					t.Fatal("valid original UNC observation rejected", err)
				}
			}
		})
	}
	input := localImageViewInput(t, `\\server\share\repo`, `\\server\share\repo\nested`)
	ref, err := ObserveImageViewLocation(input, "windows", `\\server\share\repo\nested\image.png`, domain.NewID())
	var m Manifest
	_ = json.Unmarshal(input.Manifest, &m)
	if err != nil || ref.Location != "image.png" || ref.RepositoryID != m.Repositories[1].ID || ValidateImageViewReference(input, "windows", ref) != nil {
		t.Fatal("longest original UNC root was not retained", err)
	}
	for _, bad := range []string{`\\other\share\repo\image.png`, `\\server\other\repo\image.png`, `\\Server\share\repo\image.png`, `\\server\share\repo-other\image.png`, `\\server\\repo\image.png`, `\\server`, `\\server\share`, `\\?\UNC\server\share\repo\image.png`, `\\.\C:\repo\image.png`, `\\server\share\repo\..\image.png`, `\\server\share\repo\.\image.png`, `\\server\share\repo\\image.png`, `\\server\share\repo\image.png:stream`, `C:repo\image.png`} {
		if _, err := ObserveImageViewLocation(input, "windows", bad, domain.NewID()); err == nil {
			t.Fatalf("unscoped or malformed Windows observation accepted: %q", bad)
		}
	}
	for _, malformedRoot := range []string{`\\server`, `\\server\\repo`, `\\?\UNC\server\share\repo`, `\\.\C:\repo`, `\\server\share\..\repo`, `C:repo`, `C:\repo:stream`} {
		invalid := localImageViewInput(t, malformedRoot)
		if _, err := ObserveImageViewLocation(invalid, "windows", malformedRoot+`\image.png`, domain.NewID()); err == nil {
			t.Fatalf("invalid prepared root accepted: %q", malformedRoot)
		}
	}
}

func TestImageViewAcceptsUNCShareRootRepository(t *testing.T) {
	for _, root := range []string{`\\server\share`, "//server/share"} {
		t.Run(root, func(t *testing.T) {
			input := localImageViewInput(t, root)
			locations := []string{root + `\images\original.png`, normalizeWorkerPath(root, "windows") + "/images/original.png"}
			for _, location := range locations {
				ref, err := ObserveImageViewLocation(input, "windows", location, domain.NewID())
				if err != nil || ref.Location != "images/original.png" || ValidateImageViewReference(input, "windows", ref) != nil {
					t.Fatalf("valid UNC share-root observation rejected: %v", err)
				}
			}
		})
	}
}

func TestImageViewPOSIXLiteralCharactersRoundTrip(t *testing.T) {
	for _, workerOS := range []string{"linux", "darwin"} {
		for _, relative := range []string{"screens/frame:01.png", `screens/frame\01.png`, `screens\archive/frame:01.png`, "frame:01.png"} {
			t.Run(workerOS+"/"+relative, func(t *testing.T) {
				input := localImageViewInput(t, "/repo")
				ref, err := ObserveImageViewLocation(input, workerOS, "/repo/"+relative, domain.NewID())
				if err != nil || ref.Location != relative || ref.Validate() != nil || ValidateImageViewReference(input, workerOS, ref) != nil {
					t.Fatal("POSIX filename changed or rejected", err)
				}
			})
		}
	}
	input := localImageViewInput(t, `C:\repo`)
	ref, err := ObserveImageViewLocation(input, "windows", `C:\repo\screens\image.png`, domain.NewID())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`screens\image.png`, "screens/image.png:stream", `C:\repo\image.png`, "../image.png", "/image.png"} {
		changed := ref
		changed.Location = bad
		if ValidateImageViewReference(input, "windows", changed) == nil {
			t.Fatalf("malformed Windows relative metadata accepted: %q", bad)
		}
	}
}
