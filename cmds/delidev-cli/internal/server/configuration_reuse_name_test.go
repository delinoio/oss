package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// reuseNameFixture stores legacy rows with direct SQL, bypassing the name
// admission in Tx.Put, so databases that predate name uniqueness can be
// represented.
type reuseNameFixture struct {
	t   *testing.T
	s   *Service
	raw *sql.DB
	// backing maps each stored Project to the Repository its portable document
	// references, which a reuse bundle must carry for graph closure.
	backing map[domain.ID]domain.ID
}

func newReuseNameFixture(t *testing.T) reuseNameFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	state, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(root, "state.sqlite")+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		raw.Close()
		state.Close()
	})
	service := &Service{Store: state, Identity: security.Identity{ServerID: domain.NewID()}, logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	return reuseNameFixture{t: t, s: service, raw: raw, backing: map[domain.ID]domain.ID{}}
}

func (f reuseNameFixture) put(kind domain.Kind, value any) domain.ID {
	f.t.Helper()
	id := domain.NewID()
	body, err := json.Marshal(value)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err = f.raw.Exec("INSERT INTO entities(kind,id,revision,body,created_at,updated_at) VALUES(?,?,1,?,0,0)", string(kind), string(id), body); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f reuseNameFixture) repository(name string) domain.ID {
	return f.put(domain.RepositoryKind, domain.Repository{Name: name, RemoteURL: "https://example.test/" + string(domain.NewID()) + ".git", Checkouts: []domain.Checkout{}, AutoFetch: true})
}

func (f reuseNameFixture) project(name string) domain.ID {
	repository := f.repository("Backing " + string(domain.NewID()))
	project := f.put(domain.ProjectKind, domain.Project{Name: name, Repositories: []domain.ID{repository}, PrimaryRepository: repository})
	f.backing[project] = repository
	return project
}

// reuse exports the stored document of each target and binds it as an explicit
// reuse at its current revision.
func (f reuseNameFixture) reuse(ids ...domain.ID) domain.ConfigurationImportSelection {
	f.t.Helper()
	selection := domain.ConfigurationImportSelection{Bundle: domain.ConfigurationBundle{Version: domain.ConfigurationBundleVersion, Entries: []domain.ConfigurationEntry{}, Machines: []domain.ConfigurationMachine{}}, Bindings: []domain.ConfigurationBinding{}, Machines: []domain.ConfigurationMachineBinding{}, Checkouts: []domain.ConfigurationCheckoutBinding{}}
	response, err := f.s.ExportConfiguration(transferOwner(), connect.NewRequest(&pb.ExportConfigurationRequest{}))
	if err != nil {
		f.t.Fatal(err)
	}
	var exported domain.ConfigurationBundle
	if err = domain.Decode(response.Msg.DocumentJson, &exported); err != nil {
		f.t.Fatal(err)
	}
	requested := []domain.ID{}
	for _, id := range ids {
		requested = append(requested, id)
		if backing, ok := f.backing[id]; ok && !slices.Contains(ids, backing) {
			requested = append(requested, backing)
		}
	}
	for _, id := range requested {
		var found bool
		for _, entry := range exported.Entries {
			if entry.ID != id {
				continue
			}
			found = true
			selection.Bundle.Entries = append(selection.Bundle.Entries, entry)
			row, err := f.s.Store.Get(context.Background(), entry.Kind, id)
			if err != nil {
				f.t.Fatal(err)
			}
			selection.Bindings = append(selection.Bindings, domain.ConfigurationBinding{SourceID: id, Action: domain.ConfigurationReuse, TargetID: id, ExpectedRevision: row.Revision})
		}
		if !found {
			f.t.Fatal("reused entry was not exported", id)
		}
	}
	return selection
}

func (f reuseNameFixture) preview(selection domain.ConfigurationImportSelection) error {
	raw, _ := json.Marshal(selection)
	_, err := f.s.PreviewConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: raw}))
	return err
}

func (f reuseNameFixture) assertNameConflict(err error, message string) {
	f.t.Helper()
	if connect.CodeOf(err) != connect.CodeAborted {
		f.t.Fatal(message, err)
	}
	var connectErr *connect.Error
	if !asConnectError(err, &connectErr) || connectErr.Message() == "" {
		f.t.Fatal("conflict lost its message", err)
	}
}

func asConnectError(err error, target **connect.Error) bool {
	if e, ok := err.(*connect.Error); ok {
		*target = e
		return true
	}
	return false
}

func (f reuseNameFixture) count(kind domain.Kind) int {
	f.t.Helper()
	rows, err := f.s.Store.List(context.Background(), store.Filter{Kind: kind, Limit: store.MaxPage})
	if err != nil {
		f.t.Fatal(err)
	}
	return len(rows)
}

func TestConfigurationImportReuseRejectsEquivalentNames(t *testing.T) {
	kinds := []struct {
		name string
		kind domain.Kind
		make func(reuseNameFixture, string) domain.ID
	}{
		{"repository", domain.RepositoryKind, reuseNameFixture.repository},
		{"project", domain.ProjectKind, reuseNameFixture.project},
	}
	for _, tc := range kinds {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("two reused entries collide", func(t *testing.T) {
				f := newReuseNameFixture(t)
				first, second := tc.make(f, "Straße"), tc.make(f, " STRASSE ")
				f.assertNameConflict(f.preview(f.reuse(first, second)), "bundled reuse collision admitted")
				f.assertNameConflict(f.preview(f.reuse(first)), "reuse collision with an unbundled live peer admitted")
				f.assertNameConflict(f.preview(f.reuse(second)), "reverse reuse collision with an unbundled live peer admitted")
			})
			t.Run("noncolliding reuse remains valid", func(t *testing.T) {
				f := newReuseNameFixture(t)
				s := f.s
				first, peer := tc.make(f, "Alpha"), tc.make(f, "Beta")
				selection := f.reuse(first)
				if err := f.preview(selection); err != nil {
					t.Fatal("self-excluded reuse rejected", err)
				}
				result := transferApply(t, s, transferPreview(t, s, selection), domain.NewID())
				var reused bool
				for _, resource := range result.Resources {
					if resource.Action != domain.ConfigurationReuse || resource.Revision != 1 || resource.ID != resource.SourceID {
						t.Fatal("reuse changed identity or revision", result.Resources)
					}
					reused = reused || resource.ID == first
				}
				if !reused {
					t.Fatal("reused entry missing from the result", result.Resources)
				}
				if row, err := s.Store.Get(context.Background(), tc.kind, peer); err != nil || row.Revision != 1 {
					t.Fatal("peer changed", err)
				}
			})
			t.Run("late collision publishes nothing", func(t *testing.T) {
				f := newReuseNameFixture(t)
				s := f.s
				reused := tc.make(f, "Alpha")
				selection := f.reuse(reused)
				template := transferEntry(domain.TemplateKind, domain.Template{Name: "Atomic", Contents: "must not publish"})
				selection.Bundle.Entries = append(selection.Bundle.Entries, template)
				preview := transferPreview(t, s, selection)
				tc.make(f, " alpha")
				_, err := s.ApplyConfigurationImport(transferOwner(), connect.NewRequest(&pb.ApplyConfigurationImportRequest{RequestId: string(domain.NewID()), PreviewJson: preview}))
				f.assertNameConflict(err, "late reuse collision admitted")
				if f.count(domain.TemplateKind) != 0 {
					t.Fatal("late collision partially published")
				}
				if row, err := s.Store.Get(context.Background(), tc.kind, reused); err != nil || row.Revision != 1 {
					t.Fatal("reused entry changed", err)
				}
			})
		})
	}
}

func TestConfigurationImportReuseNameNamespacesStaySeparate(t *testing.T) {
	f := newReuseNameFixture(t)
	repository, project := f.repository("Shared name"), f.project("shared name")
	if err := f.preview(f.reuse(repository, project)); err != nil {
		t.Fatal("Project and Repository names share a namespace", err)
	}
	// Reusing unrelated kinds is unaffected by legacy equivalent peers.
	f.repository("Legacy")
	f.repository(" legacy ")
	template := f.put(domain.TemplateKind, domain.Template{Name: "Legacy", Contents: "same display name is irrelevant"})
	if err := f.preview(f.reuse(template)); err != nil {
		t.Fatal("unrelated reusable kind was name-validated", err)
	}
}

func TestConfigurationImportDeferredCompletionRechecksReusedNames(t *testing.T) {
	f := newReuseNameFixture(t)
	s := f.s
	reused := f.repository("Alpha")
	selection := f.reuse(reused)
	sourceMachine, targetMachine := domain.NewID(), domain.NewID()
	doctorPut(t, s, domain.MachineKind, targetMachine, 0, domain.Machine{Name: "target", OS: "linux", Architecture: "amd64", WorkerCapabilities: []domain.WorkerCapability{domain.RepositoryInspectionMetadataV1}})
	selection.Bundle.Machines = append(selection.Bundle.Machines, domain.ConfigurationMachine{ID: sourceMachine, Name: "source", OS: "linux", Architecture: "amd64"})
	selection.Machines = append(selection.Machines, domain.ConfigurationMachineBinding{SourceID: sourceMachine, TargetID: targetMachine})
	created := transferEntry(domain.RepositoryKind, domain.Repository{Name: "Created", RemoteURL: "https://generic.invalid/team/created", AutoFetch: true, Checkouts: []domain.Checkout{{MachineID: sourceMachine, Path: "/untrusted/old"}}})
	selection.Bundle.Entries = append(selection.Bundle.Entries, created)
	selection.Checkouts = append(selection.Checkouts, domain.ConfigurationCheckoutBinding{RepositoryID: created.ID, MachineID: sourceMachine, Path: "/target/checkout"})
	report := transferApply(t, s, transferPreview(t, s, selection), domain.NewID())
	if report.State != domain.JobQueued {
		t.Fatal("import was not deferred", report.State)
	}
	// A concurrent publication takes the equivalent name while inspection runs.
	f.repository("ALPHA")
	var children []store.Record
	if err := s.Store.Read(context.Background(), func(tx *store.Tx) error {
		var err error
		children, err = tx.Jobs("", report.JobID, "", "", 10)
		return err
	}); err != nil || len(children) != 1 {
		t.Fatal("inspection children", err, len(children))
	}
	_, err := s.Store.Mutate(context.Background(), domain.NewID(), "import.fixture.finish", nil, func(tx *store.Tx) (any, error) {
		job, err := store.Decode[domain.Job](children[0])
		if err != nil {
			return nil, err
		}
		job.State = domain.JobSucceeded
		job.Output, _ = json.Marshal(workspace.Inspection{Root: "/target/checkout", Name: "repository"})
		if _, err = tx.PutJob(children[0].ID, children[0].Revision, "", "", job); err != nil {
			return nil, err
		}
		return nil, finishRepositorySave(tx, report.JobID)
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.Store.Get(context.Background(), domain.JobKind, report.JobID)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := store.Decode[domain.Job](row)
	if job.State != domain.JobFailed || job.Problem == nil || job.Problem.Cause != domain.ConfigurationNameConflictCause {
		t.Fatal("deferred completion did not settle the late collision", job.State, job.Problem)
	}
	if f.count(domain.RepositoryKind) != 2 {
		t.Fatal("deferred completion partially published")
	}
}
