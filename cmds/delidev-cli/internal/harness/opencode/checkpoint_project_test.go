package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func projectAdoptionFixture(t *testing.T, mode string) (*sessionAPI, nativeCheckpoint, *bytes.Buffer) {
	t.Helper()
	source := snapshotMetadataFixture(t)
	source.Project = "global"
	raw, _ := json.Marshal(source)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"data", "data/opencode"} {
		if err := security.PrivateDir(filepath.Join(home, filepath.FromSlash(path))); err != nil {
			t.Fatal(err)
		}
	}
	if err := stageCheckpointSnapshot(context.Background(), source, home); err != nil {
		t.Fatal(err)
	}
	project := strings.Repeat("ab", 20)
	if mode == "linked-destination" {
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(home, "data/opencode/snapshot", project)); err != nil {
			t.Skip("fixture needs symbolic links")
		}
		t.Cleanup(func() {
			files, err := os.ReadDir(outside)
			if err != nil || len(files) != 0 {
				t.Error("adoption wrote through an unowned link")
			}
		})
	}
	projectReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("x-opencode-directory") != source.Workspace {
			t.Error("project adoption escaped read-only original scope")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.RequestURI() == "/session?limit=2" {
			session := fixtureSession(source.Workspace, source.Reference.CreationRequestID, fixtureSettings())
			session["id"], session["slug"], session["projectID"] = source.Reference.SessionID, source.Slug, project
			session["time"] = map[string]any{"created": source.Created, "updated": source.Created + 1}
			sessions := []any{session}
			switch mode {
			case "unchanged":
				session["projectID"] = "global"
			case "foreign-marker":
				session["metadata"] = sessionMetadata{sessionMarker{domain.NewID()}}
			case "foreign-session":
				session["id"] = nativeFixtureOtherSessionID
			case "foreign-slug":
				session["slug"] = "different"
			case "foreign-created":
				session["time"] = map[string]any{"created": source.Created + 1, "updated": source.Created + 2}
			case "extra-session":
				sessions = append(sessions, session)
			case "missing-session":
				sessions = []any{}
			}
			_ = json.NewEncoder(w).Encode(sessions)
			return
		}
		if r.URL.Path != "/project/current" {
			t.Error("unowned native route")
			w.WriteHeader(400)
			return
		}
		projectReads++
		value := map[string]any{"id": project, "worktree": source.NativeRoot, "vcs": "git", "time": map[string]any{"created": 1, "updated": 2}, "sandboxes": []string{}}
		switch mode {
		case "foreign-project":
			value["id"] = "different"
		case "foreign-root":
			value["worktree"] = filepath.Dir(source.NativeRoot)
		case "missing-git":
			delete(value, "vcs")
		case "extra-sandbox":
			value["sandboxes"] = []string{source.NativeRoot}
		case "extra-command":
			value["commands"] = map[string]any{"start": "private unexpected command"}
		case "changed-project":
			if projectReads > 1 {
				value["time"] = map[string]any{"created": 1, "updated": 3}
			}
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	t.Cleanup(server.Close)
	var logs bytes.Buffer
	s := &sessionAPI{client: server.Client(), origin: server.URL, password: "private-adoption-password", cwd: source.Workspace, runtimeRoot: source.NativeRoot, runtimeHome: home, owner: domain.NewID(), gate: make(chan struct{}, 1), alive: func() error { return nil }, projectProfile: true, apiVerified: true, apiProfile: &nativeAPIProfile{}, projectSourceDigest: mutationDigest(raw), logger: slog.New(slog.NewJSONHandler(&logs, nil)), creation: &sessionCreation{request: source.Reference.CreationRequestID, settings: fixtureSettings(), identity: sessionIdentity{source.Reference.SessionID, "global", source.Slug, source.Created}}}
	return s, source, &logs
}

const nativeFixtureOtherSessionID = "ses_01960dcbe1faabcdefghijklmz"

func TestCheckpointProjectAdoptionRequiresOriginalNativeIdentityAndClosedGitEvidence(t *testing.T) {
	for _, mode := range []string{"valid", "unchanged", "foreign-marker", "foreign-session", "foreign-slug", "foreign-created", "extra-session", "missing-session", "foreign-project", "foreign-root", "missing-git", "extra-sandbox", "extra-command", "linked-destination", "changed-project"} {
		t.Run(mode, func(t *testing.T) {
			s, source, logs := projectAdoptionFixture(t, mode)
			err := s.adoptCheckpointProject(context.Background(), source)
			accepted := mode == "valid" || mode == "unchanged" || mode == "changed-project"
			if (err == nil) != accepted || s.projectRead {
				t.Fatal("incorrect adoption authority", err)
			}
			if mode == "valid" || mode == "changed-project" {
				if s.projectAdoption == nil || s.creation.identity != s.checkpointIdentity(source) {
					t.Fatal("original identity lost")
				}
				err = s.recheckCheckpointProject(context.Background())
				if (err == nil) != (mode == "valid") || s.projectRead {
					t.Fatal("changed native project accepted", err)
				}
			} else if s.projectAdoption != nil {
				t.Fatal("invalid adoption acquired proof")
			}
			files, err := checkpointFiles(context.Background(), source.RuntimeHome)
			if err != nil || !sameCheckpointFiles(files, source.Files) {
				t.Fatal("adoption rewrote original checkpoint")
			}
			if s.input != nil || s.events != nil || s.observer != nil {
				t.Fatal("adoption executed a native input")
			}
			for _, secret := range []string{source.Workspace, source.RuntimeHome, s.password, "private unexpected command"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("private adoption metadata logged")
				}
			}
			if _, _, err := s.request(context.Background(), http.MethodGet, "/project/current", nil, http.StatusOK); err == nil {
				t.Fatal("project inspection route remained open")
			}
		})
	}
}

func TestProjectAdoptionProofCannotRewriteItsLineageBoundary(t *testing.T) {
	source := snapshotMetadataFixture(t)
	source.Previous = []HistoryObservation{source.History}
	source.PredecessorSHA256 = strings.Repeat("12", 32)
	source.Project = "adopted"
	// This validator is independent of full history validation, exercised by
	// existing checkpoint fixtures. Here the complete snapshot part count grows.
	source.Snapshot.Parts = append(source.Snapshot.Parts, source.Snapshot.Parts...)
	proof := checkpointProjectAdoption{Version: 1, From: "global", To: source.Project, AfterInputs: 1, SourceSHA256: source.PredecessorSHA256, ProjectSHA256: strings.Repeat("34", 32)}
	source.ProjectAdoption = &proof
	if !validCheckpointProjectAdoption(source) {
		t.Fatal("valid original boundary rejected")
	}
	for _, mode := range []string{"version", "source", "target", "boundary", "missing", "foreign-predecessor", "project-proof"} {
		value := source
		changed := proof
		value.ProjectAdoption = &changed
		switch mode {
		case "version":
			changed.Version = 2
		case "source":
			changed.From = "foreign"
		case "target":
			changed.To = "global"
		case "boundary":
			changed.AfterInputs = 2
		case "missing":
			value.Snapshot = nil
		case "foreign-predecessor":
			changed.SourceSHA256 = strings.Repeat("56", 32)
		case "project-proof":
			changed.ProjectSHA256 = ""
		}
		if validCheckpointProjectAdoption(value) {
			t.Fatal("invalid adoption accepted", mode)
		}
		if checkpointReplacementProfile(value) == nil {
			t.Fatal("invalid adoption admitted to replacement", mode)
		}
	}
}
