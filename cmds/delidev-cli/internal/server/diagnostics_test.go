package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type doctorSecrets struct {
	*accountTestSecrets
	refs      []credentials.Ref
	returned  [][]byte
	afterRead func()
	failure   error
}

func (v *doctorSecrets) Get(ctx context.Context, ref credentials.Ref) ([]byte, error) {
	v.refs = append(v.refs, ref)
	value, err := v.accountTestSecrets.Get(ctx, ref)
	v.returned = append(v.returned, value)
	if v.afterRead != nil {
		v.afterRead()
	}
	if v.failure != nil {
		return value, v.failure
	}
	return value, err
}
func newDoctorFixture(t *testing.T) (*Service, *doctorSecrets) {
	t.Helper()
	state, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { state.Close() })
	secrets := &doctorSecrets{accountTestSecrets: &accountTestSecrets{values: map[credentials.Ref][]byte{}, removed: map[credentials.Ref]bool{}}}
	service := &Service{Store: state, Identity: security.Identity{ServerID: domain.NewID()}, logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), accountSecrets: secrets}
	return service, secrets
}
func doctorPut(t *testing.T, s *Service, kind domain.Kind, id domain.ID, revision uint64, value any) {
	t.Helper()
	_, err := s.Store.Mutate(context.Background(), domain.NewID(), "doctor.fixture", id, func(tx *store.Tx) (any, error) {
		return tx.Put(kind, id, revision, "", "", value)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func doctorAccount(t *testing.T, s *Service, kind domain.AccountType, auth domain.Authentication, connected bool) (domain.ID, domain.Account) {
	t.Helper()
	id := domain.NewID()
	account := domain.Account{Alias: "fixture", Type: kind, ProviderID: domain.NewID()}
	if connected {
		account.Connection = &domain.AccountConnection{ID: domain.NewID(), Authentication: auth}
	}
	doctorPut(t, s, domain.AccountKind, id, 0, account)
	return id, account
}
func TestDoctorInspectsExactReferencesWithoutMutationOrSecretLeak(t *testing.T) {
	s, secrets := newDoctorFixture(t)
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	id, account := doctorAccount(t, s, domain.APIAccount, domain.BearerAuth, true)
	ref := credentials.Ref{Owner: id, ID: account.Connection.ID, Purpose: credentials.AccountAPI}
	key := "diagnostic-fixture-secret-must-not-leak"
	secrets.values[ref] = []byte(key)
	missing, _ := doctorAccount(t, s, domain.APIAccount, domain.BearerAuth, true)
	keyless, _ := doctorAccount(t, s, domain.APIAccount, domain.KeylessAuth, true)
	disconnected, _ := doctorAccount(t, s, domain.APIAccount, domain.BearerAuth, false)
	subscription, _ := doctorAccount(t, s, domain.SubscriptionAccount, domain.SubscriptionAuth, false)
	machineID := domain.NewID()
	now := time.Now().UTC()
	doctorPut(t, s, domain.MachineKind, machineID, 0, domain.Machine{Name: "Observed Worker", OS: "darwin", Architecture: "arm64", Version: "0.1.0", LastSeen: now, Installations: []domain.Installation{{Harness: domain.Codex, State: domain.InstallationDetected, Version: domain.CodexProtocolVersion, ResolvedPath: "/private/fixture-executable-path", ExplicitPath: "/private/fixture-selected-path", ObservedAt: &now, Capabilities: []domain.Capability{}, Problem: domain.Fail(domain.Internal, key, key), Protocol: &domain.ProtocolObservation{Protocol: domain.CodexAppServer, State: domain.ProtocolFailed, Problem: domain.Fail(domain.Internal, key, key)}}}})
	s.workerStreams = map[domain.ID]workerStream{machineID: {ID: domain.NewID()}}
	_, before, err := s.Store.Snapshot(context.Background(), store.Filter{Kind: domain.AccountKind, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	response, err := s.GetDoctor(context.Background(), connect.NewRequest(&pb.GetDoctorRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var report domain.DoctorReport
	if err := json.Unmarshal(response.Msg.ReportJson, &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 2 || report.Storage.Result.State != domain.DiagnosticObserved || report.Storage.LogicalDatabaseBytes == nil || *report.Storage.LogicalDatabaseBytes == 0 || report.Storage.VolumeAvailableBytes == nil {
		t.Fatalf("missing storage observations: %+v", report.Storage)
	}
	expected := map[domain.ID]domain.DiagnosticState{id: domain.DiagnosticObserved, missing: domain.DiagnosticFailed, keyless: domain.DiagnosticNotApplicable, disconnected: domain.DiagnosticUnconfigured, subscription: domain.DiagnosticUnavailable}
	for _, result := range report.Credentials {
		if result.Result.State != expected[result.AccountID] {
			t.Fatalf("wrong credential classification: %+v", result)
		}
		delete(expected, result.AccountID)
	}
	if len(expected) != 0 || len(secrets.refs) != 2 || (secrets.refs[0] != ref && secrets.refs[1] != ref) {
		t.Fatal("inspection did not use only exact connected credential references")
	}
	for _, value := range secrets.returned {
		if !bytes.Equal(value, make([]byte, len(value))) {
			t.Fatal("decrypted temporary bytes retained")
		}
	}
	if secrets.puts != 0 || secrets.deletes != 0 || secrets.enumerations != 0 {
		t.Fatal("doctor mutated or enumerated credentials")
	}
	if len(report.Machines) != 1 || !report.Machines[0].ActiveStream || len(report.Machines[0].Installations) != 4 || report.Machines[0].Installations[0].ProtocolState != domain.ProtocolFailed || report.Machines[0].Installations[1].State != domain.InstallationUnchecked {
		t.Fatalf("missing retained Worker facts: %+v", report.Machines)
	}
	for _, forbidden := range []string{key, "/private/fixture-executable-path", "/private/fixture-selected-path"} {
		if bytes.Contains(response.Msg.ReportJson, []byte(forbidden)) || strings.Contains(logs.String(), forbidden) {
			t.Fatal("private diagnostic data leaked")
		}
	}
	_, after, err := s.Store.Snapshot(context.Background(), store.Filter{Kind: domain.AccountKind, Limit: 10})
	if err != nil || before != after {
		t.Fatal("doctor changed durable resources/events")
	}
	if entries, err := os.ReadDir(filepath.Join(s.Store.Root(), "secrets")); err != nil || len(entries) != 0 {
		t.Fatal("doctor initialized protected state")
	}
}
func TestDoctorPreservesSupersededConnectionsAndRedactsNativeFailures(t *testing.T) {
	s, secrets := newDoctorFixture(t)
	id, account := doctorAccount(t, s, domain.APIAccount, domain.BearerAuth, true)
	ref := credentials.Ref{Owner: id, ID: account.Connection.ID, Purpose: credentials.AccountAPI}
	secrets.values[ref] = []byte("temporary-doctor-key")
	secrets.failure = domain.Fail(domain.PermissionDenied, "unsafe-native-secret", "unsafe-native-secret")
	report, err := s.doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Credentials[0].Result.Code != domain.PermissionDenied || strings.Contains(report.Credentials[0].Result.Guidance, "unsafe-native-secret") {
		t.Fatal("native diagnostics escaped")
	}
	secrets.afterRead = func() {
		replacement := account
		connection := *account.Connection
		connection.ID = domain.NewID()
		replacement.Connection = &connection
		doctorPut(t, s, domain.AccountKind, id, 1, replacement)
	}
	report, err = s.doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Credentials[0].Result.State != domain.DiagnosticSuperseded || report.Credentials[0].ConnectionID != ref.ID {
		t.Fatal("old observation relabeled as new connection")
	}
}
func TestDoctorMissingVaultDoesNotCreateOrRepairState(t *testing.T) {
	s, _ := newDoctorFixture(t)
	doctorAccount(t, s, domain.APIAccount, domain.BearerAuth, true)
	s.accountSecrets = nil
	if err := os.Remove(filepath.Join(s.Store.Root(), "secrets")); err != nil {
		t.Fatal(err)
	}
	report, err := s.doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Credentials[0].Result.State == domain.DiagnosticObserved {
		t.Fatal("missing vault reported healthy")
	}
	if _, err := os.Lstat(filepath.Join(s.Store.Root(), "secrets")); !os.IsNotExist(err) {
		t.Fatal("doctor created missing scope")
	}
	if s.accountSecrets != nil || s.ownedVault != nil {
		t.Fatal("doctor retained normal writable vault")
	}
}
func TestDoctorBoundedInventoryAndCancellation(t *testing.T) {
	s, secrets := newDoctorFixture(t)
	for i := 0; i < doctorInventoryLimit+1; i++ {
		doctorAccount(t, s, domain.APIAccount, domain.BearerAuth, false)
		doctorPut(t, s, domain.MachineKind, domain.NewID(), 0, domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64"})
	}
	report, err := s.doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.MoreMachines || !report.MoreCredentials || len(report.Machines) != doctorInventoryLimit || len(report.Credentials) != doctorInventoryLimit || len(secrets.refs) != 0 {
		t.Fatal("inventory truncation not explicit")
	}
	unlock, err := s.lockAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.doctor(ctx); err == nil {
		t.Fatal("blocked credential inspection ignored cancellation")
	}
}

func TestDoctorRechecksClientRevocationAfterNativeInspection(t *testing.T) {
	s, secrets := newDoctorFixture(t)
	id, account := doctorAccount(t, s, domain.APIAccount, domain.BearerAuth, true)
	secrets.values[credentials.Ref{Owner: id, ID: account.Connection.ID, Purpose: credentials.AccountAPI}] = []byte("temporary-key")
	device := domain.NewID()
	doctorPut(t, s, domain.DeviceKind, device, 0, domain.Device{Name: "fixture", Type: domain.ClientDevice})
	secrets.afterRead = func() {
		doctorPut(t, s, domain.DeviceKind, device, 1, domain.Device{Name: "fixture", Type: domain.ClientDevice, Revoked: true})
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: device})
	if _, err := s.GetDoctor(ctx, connect.NewRequest(&pb.GetDoctorRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("revoked client received diagnostic: %v", err)
	}
	if len(secrets.returned) != 1 || !bytes.Equal(secrets.returned[0], make([]byte, len(secrets.returned[0]))) {
		t.Fatal("native temporary secret retained after revocation")
	}
}
