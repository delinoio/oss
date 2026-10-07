// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
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

func doctorChatGPTAccount() domain.Account {
	return domain.Account{Alias: "ChatGPT fixture", Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Enabled: true, Health: domain.AccountReady,
		Connection:   &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: time.Now().UTC()},
		Subscription: &domain.SubscriptionState{Generation: domain.NewID(), IdentityCommitment: strings.Repeat("a", 64)}}
}
func doctorServerOperation(a domain.Account) *domain.ServerSubscriptionOperation {
	now := time.Now().UTC()
	return &domain.ServerSubscriptionOperation{ID: domain.NewID(), Epoch: domain.NewID(), FinishID: domain.NewID(), Generation: a.Subscription.Generation, Action: domain.SubscriptionRefresh, Actor: domain.Principal{Type: domain.OwnerDevice}, State: domain.SubscriptionSucceeded, StartedAt: now, ExpiresAt: now.Add(time.Minute)}
}
func doctorQuotaOperation(a domain.Account) *domain.SubscriptionObservationOperation {
	return &domain.SubscriptionObservationOperation{ID: domain.NewID(), Action: domain.SubscriptionQuota, MachineID: domain.NewID(), Actor: domain.Principal{Type: domain.OwnerDevice}, ConnectionID: a.Connection.ID, Generation: a.Subscription.Generation, Phase: domain.SubscriptionObservationSucceeded, RequestedAt: time.Now().UTC()}
}
func doctorPending(a *domain.Account, action domain.SubscriptionAction, started bool) {
	op := doctorServerOperation(*a)
	op.Action, op.State, op.NativeStarted = action, domain.SubscriptionWaiting, started
	a.Subscription.ServerOperation = op
	phase := domain.SubscriptionQueued
	if started {
		phase = domain.SubscriptionClaimed
	}
	a.Subscription.Pending = &domain.SubscriptionOperation{ID: op.ID, Action: action, Actor: op.Actor, Phase: phase}
}

func TestDoctorChatGPTExactGenerationAndFailureDisposal(t *testing.T) {
	for _, code := range []domain.Code{"", domain.NotFound, domain.PermissionDenied, domain.Unavailable, domain.Canceled} {
		t.Run(string(code), func(t *testing.T) {
			s, secrets := newDoctorFixture(t)
			a, id := doctorChatGPTAccount(), domain.NewID()
			if err := a.Validate(); err != nil {
				t.Fatal(err)
			}
			doctorPut(t, s, domain.AccountKind, id, 0, a)
			ref := credentials.Ref{Owner: id, ID: a.Subscription.Generation, Purpose: credentials.AccountLogin}
			secrets.values[ref] = []byte("private-synthetic-subscription-bundle")
			if code != "" {
				secrets.failure = domain.Fail(code, "unsafe-bundle-native-content", "unsafe-bundle-native-content")
			}
			var logs bytes.Buffer
			s.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			before, events, err := s.Store.Snapshot(context.Background(), store.Filter{Kind: domain.AccountKind, Limit: 10})
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
			expected := domain.DiagnosticObserved
			if code == domain.NotFound || code == domain.PermissionDenied {
				expected = domain.DiagnosticFailed
			}
			if code == domain.Unavailable || code == domain.Canceled {
				expected = domain.DiagnosticUnavailable
			}
			result := report.Credentials[0]
			if result.Result.State != expected || result.Result.Code != code || result.ConnectionID != a.Connection.ID || len(secrets.refs) != 1 || secrets.refs[0] != ref {
				t.Fatalf("incorrect exact generation observation: %+v refs=%+v", result, secrets.refs)
			}
			if len(secrets.returned) != 1 || !bytes.Equal(secrets.returned[0], make([]byte, len(secrets.returned[0]))) {
				t.Fatal("returned bundle bytes not cleared")
			}
			after, afterEvents, err := s.Store.Snapshot(context.Background(), store.Filter{Kind: domain.AccountKind, Limit: 10})
			beforeRaw, _ := json.Marshal(before)
			afterRaw, _ := json.Marshal(after)
			if err != nil || events != afterEvents || !bytes.Equal(beforeRaw, afterRaw) || secrets.puts != 0 || secrets.deletes != 0 || secrets.enumerations != 0 {
				t.Fatal("storage observation mutated product or vault state")
			}
			for _, forbidden := range []string{string(a.Subscription.Generation), a.Subscription.IdentityCommitment, "private-synthetic-subscription-bundle", "unsafe-bundle-native-content"} {
				if bytes.Contains(response.Msg.ReportJson, []byte(forbidden)) || strings.Contains(logs.String(), forbidden) {
					t.Fatal("private subscription selector or bundle leaked")
				}
			}
		})
	}
	// A genuinely missing reference follows the same safe classification without repair.
	s, secrets := newDoctorFixture(t)
	id := domain.NewID()
	doctorPut(t, s, domain.AccountKind, id, 0, doctorChatGPTAccount())
	result, err := s.doctorCredential(context.Background(), id)
	if err != nil || result.Result.State != domain.DiagnosticFailed || result.Result.Code != domain.NotFound || secrets.puts != 0 {
		t.Fatalf("missing bundle repaired or misclassified: %+v %v", result, err)
	}
}

func TestDoctorChatGPTOwnershipExclusionsAndCompletedHistory(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(*domain.Account)
		state   domain.DiagnosticState
		invalid bool
	}{
		{"idle", func(a *domain.Account) {}, domain.DiagnosticObserved, false},
		{"completed-server-history", func(a *domain.Account) { a.Subscription.ServerOperation = doctorServerOperation(*a) }, domain.DiagnosticObserved, false},
		{"completed-quota-history", func(a *domain.Account) { a.Subscription.Observation = doctorQuotaOperation(*a) }, domain.DiagnosticObserved, false},
		{"completed-reset-history", func(a *domain.Account) {
			o := doctorQuotaOperation(*a)
			o.Action = domain.SubscriptionResetCredit
			o.NextCredit = true
			o.CreditsObservationID = domain.NewID()
			o.Outcome = domain.SubscriptionNothingToReset
			a.Subscription.Observation = o
		}, domain.DiagnosticObserved, false},
		{"disconnected", func(a *domain.Account) {
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Subscription = &domain.SubscriptionState{}
		}, domain.DiagnosticUnconfigured, false},
		{"disconnected-without-state", func(a *domain.Account) {
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Subscription = nil
		}, domain.DiagnosticUnconfigured, false},
		{"restore-quarantine", func(a *domain.Account) {
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Subscription.RecoveryRequired = true
		}, domain.DiagnosticUnavailable, false},
		{"missing-generation", func(a *domain.Account) { a.Subscription.Generation = ""; a.Subscription.IdentityCommitment = "" }, domain.DiagnosticUnavailable, false},
		{"missing-state", func(a *domain.Account) { a.Subscription = nil }, domain.DiagnosticUnavailable, false},
		{"wrong-authentication", func(a *domain.Account) { a.Connection.Authentication = domain.BearerAuth }, domain.DiagnosticUnavailable, true},
		{"invalid-generation", func(a *domain.Account) { a.Subscription.Generation = "unsafe-generation" }, domain.DiagnosticUnavailable, true},
		{"unconnected-retained-generation", func(a *domain.Account) { a.Connection = nil; a.Health = domain.AccountDisconnected }, domain.DiagnosticUnavailable, true},
		{"recovery", func(a *domain.Account) { a.Subscription.RecoveryRequired = true }, domain.DiagnosticUnavailable, false},
		{"pending-login", func(a *domain.Account) { doctorPending(a, domain.SubscriptionLogin, false) }, domain.DiagnosticUnavailable, false},
		{"pending-refresh", func(a *domain.Account) { doctorPending(a, domain.SubscriptionRefresh, false) }, domain.DiagnosticUnavailable, false},
		{"pending-logout", func(a *domain.Account) { doctorPending(a, domain.SubscriptionLogout, false) }, domain.DiagnosticUnavailable, false},
		{"native-started", func(a *domain.Account) { doctorPending(a, domain.SubscriptionRefresh, true) }, domain.DiagnosticUnavailable, false},
		{"worker-lease", func(a *domain.Account) {
			a.Subscription.Lease = &domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: 1, Action: domain.SubscriptionExecute, MachineID: domain.NewID(), InstanceID: domain.NewID(), DeviceID: domain.NewID(), Epoch: domain.NewID(), Generation: a.Subscription.Generation, StartedAt: time.Now().UTC()}
		}, domain.DiagnosticUnavailable, false},
		{"active-quota", func(a *domain.Account) {
			o := doctorQuotaOperation(*a)
			o.Phase = domain.SubscriptionObservationSending
			a.Subscription.Observation = o
		}, domain.DiagnosticUnavailable, false},
		{"active-reset", func(a *domain.Account) {
			o := doctorQuotaOperation(*a)
			o.Phase = domain.SubscriptionObservationQueued
			o.Action = domain.SubscriptionResetCredit
			o.NextCredit = true
			o.CreditsObservationID = domain.NewID()
			a.Subscription.Observation = o
		}, domain.DiagnosticUnavailable, false},
		{"uncertain-quota", func(a *domain.Account) {
			o := doctorQuotaOperation(*a)
			o.Phase = domain.SubscriptionObservationUncertain
			a.Subscription.Observation = o
		}, domain.DiagnosticUnavailable, false},
		{"retired-uncertain-reset", func(a *domain.Account) {
			o := doctorQuotaOperation(*a)
			o.Phase = domain.SubscriptionObservationRetiredUncertain
			o.Action = domain.SubscriptionResetCredit
			o.NextCredit = true
			o.CreditsObservationID = domain.NewID()
			a.Subscription.Observation = o
		}, domain.DiagnosticUnavailable, false},
		{"removal", func(a *domain.Account) {
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Subscription.RecoveryRequired = true
			a.Removal = &domain.AccountRemoval{RequestID: domain.NewID(), ExpectedRevision: 1}
		}, domain.DiagnosticUnavailable, false},
		{"retained-owner", func(a *domain.Account) {
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Subscription = &domain.SubscriptionState{OwnerMachineID: domain.NewID()}
		}, domain.DiagnosticUnavailable, false},
		{"native-cleanup-only", func(a *domain.Account) {
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Subscription = &domain.SubscriptionState{RecoveryRequired: true}
			op := doctorServerOperation(*a)
			op.Action = domain.SubscriptionLogin
			op.State = domain.SubscriptionFailed
			op.CleanupPhase = domain.SubscriptionNativeCleanupConfirmed
			a.Subscription.ServerOperation = op
		}, domain.DiagnosticUnavailable, false},
		{"completed-initial-cleanup", func(a *domain.Account) {
			a.Connection = nil
			a.Health = domain.AccountDisconnected
			a.Subscription = &domain.SubscriptionState{}
			op := doctorServerOperation(*a)
			op.Action = domain.SubscriptionLogin
			op.State = domain.SubscriptionFailed
			op.CleanupPhase = domain.SubscriptionCredentialCleanupConfirmed
			a.Subscription.ServerOperation = op
		}, domain.DiagnosticUnconfigured, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			s, secrets := newDoctorFixture(t)
			a, id := doctorChatGPTAccount(), domain.NewID()
			test.edit(&a)
			if !test.invalid {
				if err := a.Validate(); err != nil {
					t.Fatalf("invalid ownership fixture: %v", err)
				}
			}
			doctorPut(t, s, domain.AccountKind, id, 0, a)
			if a.Subscription != nil {
				secrets.values[credentials.Ref{Owner: id, ID: a.Subscription.Generation, Purpose: credentials.AccountLogin}] = []byte("synthetic-bundle")
			}
			before, event, err := s.Store.Snapshot(context.Background(), store.Filter{Kind: domain.AccountKind, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.doctorCredential(context.Background(), id)
			if err != nil || result.Result.State != test.state {
				t.Fatalf("wrong ownership result: %+v %v", result, err)
			}
			reads := 0
			if test.state == domain.DiagnosticObserved {
				reads = 1
			}
			if len(secrets.refs) != reads {
				t.Fatalf("unexpected protected reads: %+v", secrets.refs)
			}
			if test.state == domain.DiagnosticUnavailable && result.Result.Code != domain.Unavailable {
				t.Fatalf("ownership unavailability misclassified: %+v", result)
			}
			after, afterEvent, err := s.Store.Snapshot(context.Background(), store.Filter{Kind: domain.AccountKind, Limit: 10})
			br, _ := json.Marshal(before)
			ar, _ := json.Marshal(after)
			if err != nil || event != afterEvent || !bytes.Equal(br, ar) {
				t.Fatal("ownership mutated during read-only inspection")
			}
		})
	}
}

func TestDoctorChatGPTPublicationSelectors(t *testing.T) {
	for _, change := range []string{"generation", "authentication", "eligibility", "type", "service", "commitment", "connection", "alias", "delete"} {
		t.Run(change, func(t *testing.T) {
			s, secrets := newDoctorFixture(t)
			a, id := doctorChatGPTAccount(), domain.NewID()
			doctorPut(t, s, domain.AccountKind, id, 0, a)
			secrets.values[credentials.Ref{Owner: id, ID: a.Subscription.Generation, Purpose: credentials.AccountLogin}] = []byte("private-bundle")
			secrets.afterRead = func() {
				if change == "delete" {
					_, err := s.Store.Mutate(context.Background(), domain.NewID(), "doctor.fixture-delete", id, func(tx *store.Tx) (any, error) { return nil, tx.Delete(domain.AccountKind, id, 1) })
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				switch change {
				case "generation":
					a.Subscription.Generation = domain.NewID()
				case "authentication":
					a.Connection.Authentication = domain.BearerAuth
				case "eligibility":
					a.Subscription.RecoveryRequired = true
				case "type":
					a.Type = domain.APIAccount
				case "service":
					a.SubscriptionService = domain.SubscriptionClaude
				case "commitment":
					a.Subscription.IdentityCommitment = strings.Repeat("b", 64)
				case "connection":
					a.Connection.ID = domain.NewID()
				case "alias":
					a.Alias = "Renamed"
				}
				doctorPut(t, s, domain.AccountKind, id, 1, a)
			}
			report, err := s.doctor(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			expected := domain.DiagnosticSuperseded
			if change == "alias" {
				expected = domain.DiagnosticObserved
			}
			if report.Credentials[0].Result.State != expected || len(secrets.refs) != 1 {
				t.Fatalf("stale selector published: %+v", report.Credentials)
			}
			if !bytes.Equal(secrets.returned[0], make([]byte, len(secrets.returned[0]))) {
				t.Fatal("selector race retained bundle")
			}
		})
	}
}

func TestDoctorSubscriptionUnsupportedScopesAndAbsentVault(t *testing.T) {
	s, secrets := newDoctorFixture(t)
	for _, service := range []domain.SubscriptionService{domain.SubscriptionClaude, domain.SubscriptionGrok, "", "retired-service"} {
		a := doctorChatGPTAccount()
		a.SubscriptionService = service
		doctorPut(t, s, domain.AccountKind, domain.NewID(), 0, a)
	}
	retired := doctorChatGPTAccount()
	retired.ProviderID = domain.NewID()
	doctorPut(t, s, domain.AccountKind, domain.NewID(), 0, retired)
	report, err := s.doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets.refs) != 0 {
		t.Fatal("unsupported subscription accessed protected storage")
	}
	for _, result := range report.Credentials {
		if result.Result.State != domain.DiagnosticUnavailable || result.Result.Code != domain.Unsupported {
			t.Fatalf("unsupported service inferred: %+v", result)
		}
	}
	s, _ = newDoctorFixture(t)
	doctorPut(t, s, domain.AccountKind, domain.NewID(), 0, doctorChatGPTAccount())
	s.accountSecrets = nil
	if err := os.Remove(filepath.Join(s.Store.Root(), "secrets")); err != nil {
		t.Fatal(err)
	}
	report, err = s.doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Credentials[0].Result.State == domain.DiagnosticObserved {
		t.Fatal("absent ChatGPT vault reported readable")
	}
	if _, err := os.Stat(filepath.Join(s.Store.Root(), "secrets")); !os.IsNotExist(err) {
		t.Fatal("ChatGPT inspection created missing vault")
	}
	if s.accountSecrets != nil || s.ownedVault != nil {
		t.Fatal("read-only vault installed as writable")
	}
}

func TestDoctorChatGPTRevocationDisposesBundle(t *testing.T) {
	s, secrets := newDoctorFixture(t)
	a, id := doctorChatGPTAccount(), domain.NewID()
	doctorPut(t, s, domain.AccountKind, id, 0, a)
	secrets.values[credentials.Ref{Owner: id, ID: a.Subscription.Generation, Purpose: credentials.AccountLogin}] = []byte("private-bundle")
	device := domain.NewID()
	doctorPut(t, s, domain.DeviceKind, device, 0, domain.Device{Name: "fixture", Type: domain.ClientDevice})
	secrets.afterRead = func() {
		doctorPut(t, s, domain.DeviceKind, device, 1, domain.Device{Name: "fixture", Type: domain.ClientDevice, Revoked: true})
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: device})
	if _, err := s.GetDoctor(ctx, connect.NewRequest(&pb.GetDoctorRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("revoked client got bundle observation: %v", err)
	}
	if len(secrets.returned) != 1 || !bytes.Equal(secrets.returned[0], make([]byte, len(secrets.returned[0]))) {
		t.Fatal("revocation retained bundle bytes")
	}
}

func TestDoctorChatGPTTemporaryInspectionClosesWithoutReconciling(t *testing.T) {
	s, _ := newDoctorFixture(t)
	s.accountSecrets = nil
	id := domain.NewID()
	doctorPut(t, s, domain.AccountKind, id, 0, doctorChatGPTAccount())
	root := filepath.Join(s.Store.Root(), "secrets")
	// Only empty synthetic scope metadata is installed. The missing exact record
	// returns before any OS wrapping-key lookup; no user credential is accessed.
	if err := security.WriteAtomic(filepath.Join(root, "vault.lock"), nil); err != nil {
		t.Fatal(err)
	}
	pin, _ := json.Marshal(struct {
		Scope domain.ID `json:"scope"`
	}{s.Identity.ServerID})
	if err := security.WriteAtomic(filepath.Join(root, "scope.json"), pin); err != nil {
		t.Fatal(err)
	}
	scratch := []byte("synthetic unfinished scope write")
	if err := security.WriteAtomic(filepath.Join(root, ".pending-12345"), scratch); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := s.doctorCredential(context.Background(), id)
		if err != nil || result.Result.State != domain.DiagnosticFailed || result.Result.Code != domain.NotFound {
			t.Fatalf("temporary inspection was retained or missing reference was repaired: %+v %v", result, err)
		}
		lock, err := security.TryLockExisting(filepath.Join(root, "vault.lock"))
		if err != nil {
			t.Fatalf("inspection did not close its lock: %v", err)
		}
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	}
	current, err := os.ReadFile(filepath.Join(root, ".pending-12345"))
	if err != nil || !bytes.Equal(current, scratch) {
		t.Fatal("Doctor reconciled scratch state")
	}
	if s.accountSecrets != nil || s.ownedVault != nil {
		t.Fatal("temporary reader became the writable server vault")
	}
}
