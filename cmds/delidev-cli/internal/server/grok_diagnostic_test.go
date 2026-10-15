// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

var sensitiveServerGrokText = regexp.MustCompile(`(?i)(sentinel|secret|refresh_token|https?://|/users/|/home/|/controlled/|[a-z]:\\|@[a-z0-9-]+\.[a-z]|eyj[a-z0-9_-]{8,})`)

func TestGrokDiagnosticWireMappingCoversEveryPhase(t *testing.T) {
	seen := map[pb.GrokDiagnosticPhase]domain.GrokPhase{}
	for _, phase := range domain.GrokPhases {
		wire, ok := grokDiagnosticPhases[phase]
		if !ok || wire == pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_UNSPECIFIED {
			t.Fatalf("phase %q has no wire member", phase)
		}
		if other, duplicate := seen[wire]; duplicate {
			t.Fatalf("phases %q and %q share wire member %s", phase, other, wire)
		}
		seen[wire] = phase
		// The wire name follows the domain name, so a drifted mapping fails here.
		if !strings.HasSuffix(wire.String(), strings.ToUpper(string(phase))) {
			t.Fatalf("phase %q mapped to %s", phase, wire)
		}
	}
	if len(grokDiagnosticPhases) != len(domain.GrokPhases) {
		t.Fatal("the mapping contains a phase the domain does not define")
	}
}

func TestGrokDiagnosticMessageOmitsInvalidMetadata(t *testing.T) {
	valid := domain.GrokErrorDiagnostic(domain.WithGrokDiagnostic("1.0.52", domain.GrokProfile, domain.Fail(domain.Unsupported, "x", "y")))
	got := grokDiagnosticMessage(valid)
	if got == nil || got.DetectedVersion != "1.0.52" || got.SupportedVersion != domain.GrokSubscriptionBaselineVersion || got.Phase != pb.GrokDiagnosticPhase_GROK_DIAGNOSTIC_PHASE_PROFILE || got.Code != string(domain.Unsupported) || got.Message != valid.Message || got.Guidance != valid.Guidance {
		t.Fatalf("unexpected wire diagnostic: %v", got)
	}
	tampered := *valid
	tampered.Message = "raw native stderr with /Users/private/.grok/auth.json"
	if grokDiagnosticMessage(&tampered) != nil || grokDiagnosticMessage(nil) != nil {
		t.Fatal("invalid or absent metadata was forwarded")
	}
}

// seedGrokOperation stores one Grok server operation carrying diagnostic (an
// unsupported one also keeps its original operation queued).
func (f *grokFixture) seedGrokOperation(diagnostic *domain.GrokDiagnostic, state domain.SubscriptionLoginState) domain.ID {
	f.t.Helper()
	op := domain.NewID()
	now := time.Now().UTC()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.operation", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		owner := domain.Principal{Type: domain.OwnerDevice}
		d := *diagnostic
		d.CorrelationID = string(op)
		a.Subscription = &domain.SubscriptionState{ServerOperation: &domain.ServerSubscriptionOperation{ID: op, Action: domain.SubscriptionLogin, Epoch: f.service.subscriptionServerEpoch(), FinishID: domain.NewID(), Actor: owner, State: state, StartedAt: now, ExpiresAt: now.Add(15 * time.Minute), GrokDiagnostic: &d}}
		if state == domain.SubscriptionUnsupported {
			// An unsupported record is a refused Take, which leaves its original
			// operation queued; progress serves the diagnostic only while it is.
			a.Subscription.Pending = &domain.SubscriptionOperation{ID: op, Action: domain.SubscriptionLogin, MachineID: f.input.MachineID, Actor: owner, Phase: domain.SubscriptionQueued}
		}
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return op
}

// GetSubscriptionProgress populates grok_diagnostic (10) for Grok operations
// and leaves the Codex (7) and native (9) diagnostics unset.
func TestGrokProgressPopulatesGrokDiagnosticForEveryPhase(t *testing.T) {
	f := newGrokFixture(t)
	for _, phase := range domain.GrokPhases {
		t.Run(string(phase), func(t *testing.T) {
			diagnostic := domain.GrokErrorDiagnostic(domain.WithGrokDiagnostic("1.0.52", phase, domain.Fail(domain.Unsupported, "raw-native-sentinel /Users/private", "secret https://auth.x.ai")))
			op := f.seedGrokOperation(diagnostic, domain.SubscriptionUnsupported)
			p := f.progressFor(string(op))
			g := p.GrokDiagnostic
			if g == nil || g.Phase != grokDiagnosticPhases[phase] || g.Code != string(domain.Unsupported) || g.DetectedVersion != "1.0.52" || g.SupportedVersion != domain.GrokSubscriptionBaselineVersion || g.CorrelationId != string(op) || g.Message == "" || g.Guidance == "" {
				t.Fatalf("grok_diagnostic was not populated: %v", g)
			}
			if p.Diagnostic != nil || p.NativeDiagnostic != nil || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_UNSUPPORTED || p.Url != "" || p.UserCode != "" {
				t.Fatalf("progress populated another diagnostic or login presentation: %v", p)
			}
			raw, err := protojson.Marshal(p)
			if err != nil || sensitiveServerGrokText.Match(raw) {
				t.Fatalf("progress response contains a sensitive shape: %s %v", raw, err)
			}
		})
	}
}

func TestGrokProgressRejectsAnotherOperationAndOmitsAbsentDiagnostic(t *testing.T) {
	f := newGrokFixture(t)
	diagnostic := domain.GrokErrorDiagnostic(domain.WithGrokDiagnostic("1.0.52", domain.GrokProfile, domain.Fail(domain.Unsupported, "x", "y")))
	f.seedGrokOperation(diagnostic, domain.SubscriptionUnsupported)
	if _, err := f.client.GetSubscriptionProgress(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.GetSubscriptionProgressRequest{AccountId: string(f.input.AccountID), OperationId: string(domain.NewID())})); err == nil {
		t.Fatal("progress served an operation that is not the account's original")
	}
	_, a := f.record()
	op := a.Subscription.ServerOperation.ID
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.grok.clear", nil, func(tx *store.Tx) (any, error) {
		r, a, err := accountFromTx(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.ServerOperation.GrokDiagnostic = nil
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	if p := f.progressFor(string(op)); p.GrokDiagnostic != nil || p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_UNSUPPORTED {
		t.Fatalf("an operation without a failure invented a diagnostic: %v", p)
	}
}

// A refused Take reports the original phase, installed version and supported
// baseline from a closed diagnostic, never native text or the executable path.
func TestGrokTakeRefusalCarriesPhaseAndVersions(t *testing.T) {
	observed := time.Now().UTC()
	profileFailure := domain.GrokErrorDiagnostic(domain.WithGrokDiagnostic("1.0.52", domain.GrokProfile, domain.Fail(domain.Unsupported, "x", "y")))
	for _, test := range []struct {
		name    string
		edit    func(*domain.Installation)
		phase   domain.GrokPhase
		version string
		message string
		code    domain.Code
	}{
		{"below baseline", func(i *domain.Installation) { i.Version = "1.0.45" }, domain.GrokVersion, "1.0.45", "at or above " + domain.GrokSubscriptionBaselineVersion, ""},
		{"invalid version metadata", func(i *domain.Installation) { i.Version = "not-a-version" }, domain.GrokVersion, "", "version metadata is invalid", ""},
		{"missing executable", func(i *domain.Installation) { i.State, i.ResolvedPath = domain.InstallationMissing, "" }, domain.GrokDiscovery, "1.0.52", "executable discovery", domain.NotFound},
		{"never observed", func(i *domain.Installation) { i.ObservedAt = nil }, domain.GrokDiscovery, "1.0.52", "executable discovery", ""},
		{"observed in the future", func(i *domain.Installation) { at := observed.Add(time.Hour); i.ObservedAt = &at }, domain.GrokDiscovery, "1.0.52", "executable discovery", ""},
		{"stored profile failure on a future observation", func(i *domain.Installation) {
			at := observed.Add(time.Hour)
			i.ObservedAt, i.ProtocolVerified = &at, false
			i.Protocol = &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.GrokBuild), State: domain.ProtocolUnsupported, GrokDiagnostic: profileFailure}
		}, domain.GrokDiscovery, "1.0.52", "executable discovery", ""},
		{"profile unverified", func(i *domain.Installation) { i.ProtocolVerified = false }, domain.GrokProfile, "1.0.52", "profile validation", ""},
		{"stored profile failure", func(i *domain.Installation) {
			i.ProtocolVerified = false
			i.Protocol = &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.GrokBuild), State: domain.ProtocolUnsupported, GrokDiagnostic: profileFailure}
		}, domain.GrokProfile, "1.0.52", "profile validation", ""},
		// A release between the discovery baseline (1.0.41) and the subscription
		// baseline keeps its probe's profile diagnostic, but admission must still
		// classify it as a version failure.
		{"stored profile failure below subscription baseline", func(i *domain.Installation) {
			i.Version, i.ProtocolVerified = "1.0.43", false
			i.Protocol = &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.GrokBuild), State: domain.ProtocolUnsupported, GrokDiagnostic: domain.GrokErrorDiagnostic(domain.WithGrokDiagnostic("1.0.43", domain.GrokProfile, domain.Fail(domain.Unsupported, "x", "y")))}
		}, domain.GrokVersion, "1.0.43", "at or above " + domain.GrokSubscriptionBaselineVersion, ""},
		{"stored profile failure on an undiscovered installation", func(i *domain.Installation) {
			i.State, i.ResolvedPath, i.ProtocolVerified = domain.InstallationMissing, "", false
			i.Protocol = &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.GrokBuild), State: domain.ProtocolUnsupported, GrokDiagnostic: profileFailure}
		}, domain.GrokDiscovery, "1.0.52", "executable discovery", domain.NotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGrokFixture(t)
			f.setGrokInstallation(t, func(installations *[]domain.Installation) {
				(*installations)[0] = domain.Installation{Harness: domain.GrokBuild, State: domain.InstallationDetected, Version: "1.0.52", ResolvedPath: "/controlled/native/grok", ProtocolVerified: true, ObservedAt: &observed, Protocol: &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.GrokBuild), State: domain.ProtocolVerified}}
				test.edit(&(*installations)[0])
			})
			_, err := f.take(f.queue(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN), pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
			if err == nil {
				t.Fatal("Take admitted a refused installation")
			}
			problem := domain.SafeError(rpc.ClientError(err))
			want := test.code
			if want == "" {
				want = domain.Unsupported
			}
			if problem.Code != want || !strings.Contains(problem.Message, test.message) || sensitiveServerGrokText.MatchString(problem.Message+problem.Guidance) {
				t.Fatalf("unexpected refusal: %#v", problem)
			}
			label := test.version
			if label == "" {
				label = "not detected"
			}
			if !strings.HasPrefix(problem.Message, "Grok Build "+label+" did not complete") {
				t.Fatalf("refusal lost the installed version: %q", problem.Message)
			}
			var found domain.Installation
			f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				_, m, err := activeMachine(tx, f.input.MachineID)
				found = m.Installations[0]
				return err
			})
			d := domain.GrokErrorDiagnostic(grokAdmissionFailure(&found, time.Now().UTC()))
			if d == nil || d.Phase != test.phase || d.Code != want || d.DetectedVersion != test.version || d.SupportedVersion != domain.GrokSubscriptionBaselineVersion || d.Validate() != nil {
				t.Fatalf("unexpected admission diagnostic: %#v", d)
			}
		})
	}
	if d := domain.GrokErrorDiagnostic(grokAdmissionFailure(nil, time.Now().UTC())); d == nil || d.Phase != domain.GrokDiscovery || d.DetectedVersion != "" {
		t.Fatalf("an absent installation was not a discovery failure: %#v", d)
	}
}

// Admission keeps the classification that discovery and protocol validation
// already recorded instead of reporting every refusal as generic unsupported.
// The table covers every discovery state and protocol state an installation can
// persist.
func TestGrokAdmissionFailurePreservesRecordedClassification(t *testing.T) {
	now := time.Now().UTC()
	observed := now
	future := func(offset time.Duration) func(*domain.Installation) {
		return func(i *domain.Installation) {
			at := now.Add(offset)
			i.ObservedAt = &at
		}
	}
	storedRuntime := func(i *domain.Installation) {
		i.Protocol.State = domain.ProtocolFailed
		i.Protocol.GrokDiagnostic = domain.GrokErrorDiagnostic(domain.WithGrokDiagnostic(i.Version, domain.GrokRuntime, domain.Fail(domain.PermissionDenied, "x", "y")))
	}
	detected := func(edit func(*domain.Installation)) *domain.Installation {
		i := &domain.Installation{Harness: domain.GrokBuild, State: domain.InstallationDetected, Version: domain.GrokSubscriptionBaselineVersion, ResolvedPath: "/controlled/native/grok", ObservedAt: &observed, Protocol: &domain.ProtocolObservation{Protocol: domain.ProtocolFor(domain.GrokBuild), State: domain.ProtocolVerified}}
		edit(i)
		return i
	}
	failed := func(state domain.InstallationState) *domain.Installation {
		return &domain.Installation{Harness: domain.GrokBuild, State: state, Problem: domain.InstallationProblem(state)}
	}
	for _, test := range []struct {
		name  string
		found *domain.Installation
		phase domain.GrokPhase
		code  domain.Code
	}{
		{"not reported", nil, domain.GrokDiscovery, domain.Unsupported},
		{"unchecked", failed(domain.InstallationUnchecked), domain.GrokDiscovery, domain.Unsupported},
		{"missing", failed(domain.InstallationMissing), domain.GrokDiscovery, domain.NotFound},
		{"permission denied", failed(domain.InstallationDenied), domain.GrokDiscovery, domain.PermissionDenied},
		{"incompatible", failed(domain.InstallationIncompatible), domain.GrokDiscovery, domain.Unsupported},
		{"failed", failed(domain.InstallationFailed), domain.GrokDiscovery, domain.Unavailable},
		{"detected without a path", detected(func(i *domain.Installation) { i.ResolvedPath = "" }), domain.GrokDiscovery, domain.Unsupported},
		{"detected without an observation", detected(func(i *domain.Installation) { i.ObservedAt = nil }), domain.GrokDiscovery, domain.Unsupported},
		// An observation later than the admission time plus the tolerated skew is
		// invalid discovery evidence, so it never restores a stored diagnostic or
		// reports a profile failure. The boundary itself is admitted.
		{"observation in the future", detected(future(time.Hour)), domain.GrokDiscovery, domain.Unsupported},
		{"observation just beyond the skew", detected(future(grokObservationSkew + time.Millisecond)), domain.GrokDiscovery, domain.Unsupported},
		{"observation exactly at the skew", detected(future(grokObservationSkew)), domain.GrokProfile, domain.Unsupported},
		{"observation inside the skew", detected(future(time.Millisecond)), domain.GrokProfile, domain.Unsupported},
		{"future observation below the baseline", detected(func(i *domain.Installation) { future(time.Hour)(i); i.Version = "1.0.45" }), domain.GrokDiscovery, domain.Unsupported},
		{"future observation with a failed protocol", detected(func(i *domain.Installation) { future(time.Hour)(i); i.Protocol.State = domain.ProtocolFailed }), domain.GrokDiscovery, domain.Unsupported},
		{"future observation with a stored diagnostic", detected(func(i *domain.Installation) { future(time.Hour)(i); storedRuntime(i) }), domain.GrokDiscovery, domain.Unsupported},
		{"detected with a recorded problem", detected(func(i *domain.Installation) { i.Problem = domain.Fail(domain.Unavailable, "safe", "") }), domain.GrokDiscovery, domain.Unavailable},
		{"below the baseline", detected(func(i *domain.Installation) { i.Version = "1.0.45" }), domain.GrokVersion, domain.Unsupported},
		{"invalid version", detected(func(i *domain.Installation) { i.Version = "not-a-version" }), domain.GrokVersion, domain.Unsupported},
		{"no protocol observation", detected(func(i *domain.Installation) { i.Protocol = nil }), domain.GrokProfile, domain.Unsupported},
		{"protocol verified but not marked", detected(func(i *domain.Installation) {}), domain.GrokProfile, domain.Unsupported},
		{"protocol unsupported", detected(func(i *domain.Installation) { i.Protocol.State = domain.ProtocolUnsupported }), domain.GrokProfile, domain.Unsupported},
		{"protocol failed", detected(func(i *domain.Installation) { i.Protocol.State = domain.ProtocolFailed }), domain.GrokProfile, domain.Unavailable},
		{"stored diagnostic wins over the protocol state", detected(storedRuntime), domain.GrokRuntime, domain.PermissionDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := grokAdmissionFailure(test.found, now)
			d := domain.GrokErrorDiagnostic(err)
			if d == nil || d.Phase != test.phase || d.Code != test.code || d.Validate() != nil || d.SupportedVersion != domain.GrokSubscriptionBaselineVersion {
				t.Fatalf("unexpected admission diagnostic: %#v", d)
			}
			if safe := domain.SafeError(err); safe.Code != test.code || safe.Message != d.Message {
				t.Fatalf("transport error differs from the diagnostic: %#v", safe)
			}
			if sensitiveServerGrokText.MatchString(d.Message + d.Guidance) {
				t.Fatalf("diagnostic text contains a sensitive shape: %q", d.Message)
			}
		})
	}
}
