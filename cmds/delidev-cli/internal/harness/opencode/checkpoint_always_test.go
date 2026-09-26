package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestCheckpointAlwaysCapturePreservesOnlyOriginalReadAllowance(t *testing.T) {
	for _, fault := range []string{"valid", "http", "native", "unclaimed", "body", "permission", "empty", "base", "order-missing", "order-extra", "order-duplicate", "tool", "rules-bound"} {
		t.Run(fault, func(t *testing.T) {
			value, s, i := checkpointPermissionFixture(t, PermissionAlways)
			switch fault {
			case "http":
				i.attempt.receipt.HTTPAccepted = false
			case "native":
				i.attempt.receipt.NativeAccepted = false
			case "unclaimed":
				i.attempt = nil
			case "body":
				i.attempt.body = []byte(checkpointOnceBody)
			case "permission":
				i.value.Permission.Name = "future"
			case "empty":
				i.value.Permission.Always = nil
			case "base":
				s.creation.settings.Permission = []PermissionRule{{"read", "*", PermissionDeny}}
			case "order-missing":
				s.observer.alwaysOrder = nil
			case "order-extra":
				s.observer.alwaysOrder = append(s.observer.alwaysOrder, "foreign")
			case "order-duplicate":
				s.observer.alwaysOrder = append(s.observer.alwaysOrder, i.value.ID)
			case "tool":
				s.observer.parts[i.attempt.claim.PartID].value.Tool = checkpointInlineToolFixture(checkpointShellTool)
			case "rules-bound":
				i.value.Permission.Always = make([]string, 129)
			}
			proof := s.checkpointToolHistory(value)
			if (proof != nil) != (fault == "valid") {
				t.Fatal("invalid remembered permission acquired restoration")
			}
			if proof != nil {
				if proof.AppliedAlways != 0 || len(proof.Always[0].Rules) != len(i.value.Permission.Always) {
					t.Fatal("capture fabricated already-applied rules")
				}
				for index, rule := range proof.Always[0].Rules {
					if rule != (PermissionRule{"read", i.value.Permission.Always[index], PermissionAllow}) {
						t.Fatal("original native allowance changed")
					}
				}
			}
		})
	}
}

func TestCheckpointAlwaysProofRejectsChangedRestorationScope(t *testing.T) {
	for _, fault := range []string{"version", "flag", "empty", "applied", "name", "action", "pattern", "claim", "duplicate", "tool"} {
		t.Run(fault, func(t *testing.T) {
			value, _, _ := checkpointPermissionFixture(t, PermissionAlways)
			p := value.Tools
			switch fault {
			case "version":
				p.Version = 2
			case "flag":
				p.InteractionFree = true
			case "empty":
				p.Always[0].Rules = nil
			case "applied":
				p.AppliedAlways = 2
			case "name":
				p.Always[0].Rules[0].Permission = "*"
			case "action":
				p.Always[0].Rules[0].Action = PermissionDeny
			case "pattern":
				p.Always[0].Rules[0].Pattern = ""
			case "claim":
				p.Always[0].Claim.BodyDigest = mutationDigest([]byte(checkpointOnceBody))
			case "duplicate":
				p.Always = append(p.Always, p.Always[0])
			case "tool":
				p.Parts[0].Name = checkpointShellTool
			}
			if validCheckpointTools(value) {
				t.Fatal("contradictory remembered allowance accepted")
			}
		})
	}
}

func TestCheckpointAlwaysSuccessorPinsAppliedPrefixWithoutChangingOriginal(t *testing.T) {
	value, s, _ := checkpointPermissionFixture(t, PermissionAlways)
	s.predecessor, s.observer = &value, &inputObserver{}
	s.restoredAlways = 1
	s.sessionPermissions = checkpointAppliedPermissions(value.Tools, 1)
	next := nativeCheckpoint{Project: "global", NativeRoot: "/", Previous: []HistoryObservation{value.History}}
	next.Tools = s.checkpointToolHistory(next)
	if next.Tools == nil || next.Tools.AppliedAlways != 1 || value.Tools.AppliedAlways != 0 || checkpointReplacementProfile(next) != nil {
		t.Fatal("successor lost independently applied allowance prefix")
	}
	if s.freshCheckpointInput(value.Tools.Always[0].Claim.RequestID, "new-message", "new-part") {
		t.Fatal("remembered approval request was reused")
	}
	next.Tools.Always[0].Rules[0].Pattern = "changed"
	if value.Tools.Always[0].Rules[0].Pattern == "changed" {
		t.Fatal("successor changed original permission pattern")
	}
	s.sessionPermissions[0].Pattern = "contradictory"
	if s.checkpointToolHistory(next) != nil {
		t.Fatal("contradictory runtime permissions were silently checkpointed")
	}
}

func TestCheckpointPermissionRestorationAppendsOnceWithoutReplay(t *testing.T) {
	for _, scenario := range []string{"valid", "append-suffix", "lost", "changed-response", "wrong-prefix", "active", "base", "failed", "external-valid", "external-append-suffix", "external-lost"} {
		t.Run(scenario, func(t *testing.T) {
			value, s, _ := checkpointPermissionFixture(t, PermissionAlways)
			if strings.HasPrefix(scenario, "external-") {
				scenario = strings.TrimPrefix(scenario, "external-")
				value.Tools.Version = 13
				for index := range value.Tools.Always[0].Rules {
					value.Tools.Always[0].Rules[index].Permission = "external_directory"
				}
			}
			value.Reference.SessionID = value.History.SessionID
			s.creation.recorded, s.input, s.events, s.observer = false, nil, nil, nil
			s.apiVerified, s.apiProfile = true, &nativeAPIProfile{Settings: s.creation.settings}
			s.sessionPermissions = []PermissionRule{}
			if scenario == "append-suffix" {
				second := value.Tools.Always[0]
				second.Claim.RequestID = domain.NewID()
				second.Claim.InteractionID = "per_01960dcbe1ffABCDEFGHIJKLMN"
				second.Claim.ArrivalID = "evt_01960dcbe1ffABCDEFGHIJKLMN"
				second.Rules = []PermissionRule{{value.Tools.Always[0].Rules[0].Permission, "private-next-pattern", PermissionAllow}}
				value.Tools.Always = append(value.Tools.Always, second)
				value.Tools.AppliedAlways, s.restoredAlways = 1, 1
				s.sessionPermissions = checkpointAppliedPermissions(value.Tools, 1)
			}
			next := checkpointAppliedPermissions(value.Tools, uint32(len(value.Tools.Always)))
			want := slices.Clone(next[len(s.sessionPermissions):])
			switch scenario {
			case "wrong-prefix":
				s.sessionPermissions = []PermissionRule{{"read", "changed", PermissionAllow}}
			case "active":
				s.input = &sessionInput{}
			case "base":
				s.creation.settings.Permission = []PermissionRule{{"read", "*", PermissionAsk}}
			case "failed":
				s.problem = sessionProblem()
			}
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/session/"+value.Reference.SessionID {
					t.Error("restoration escaped original session")
				}
				if r.Method == http.MethodPatch {
					posts++
					var request struct {
						Permission []PermissionRule `json:"permission"`
					}
					raw, _ := io.ReadAll(r.Body)
					if r.Header.Get("Content-Type") != "application/json" || domain.Decode(raw, &request) != nil || !slices.Equal(request.Permission, want) {
						t.Error("restoration did not append the exact original suffix")
					}
					if scenario == "lost" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
				} else if r.Method != http.MethodGet {
					t.Error("unexpected restoration mutation")
				}
				settings := s.creation.settings
				settings.Permission = slices.Clone(next)
				if scenario == "changed-response" {
					settings.Permission[0].Pattern = "changed"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(fixtureSession(s.cwd, s.creation.request, settings))
			}))
			defer server.Close()
			s.origin = server.URL
			client, transport := probeHTTPClient(strings.TrimPrefix(server.URL, "http://"))
			defer transport.CloseIdleConnections()
			s.client = client
			err := s.restoreCheckpointPermissions(context.Background(), value)
			valid := scenario == "valid" || scenario == "append-suffix"
			if (err == nil) != valid {
				t.Fatal("restoration outcome contradicted original native acceptance", err)
			}
			if valid && (!slices.Equal(s.sessionPermissions, next) || s.restoredAlways != uint32(len(value.Tools.Always))) {
				t.Fatal("restoration lost exact applied state")
			}
			wantPosts := 0
			if valid || scenario == "lost" || scenario == "changed-response" {
				wantPosts = 1
			}
			if posts != wantPosts || s.permissionRestore != nil {
				t.Fatal("restoration route remained available")
			}
			if s.restoreCheckpointPermissions(context.Background(), value) == nil || posts != wantPosts {
				t.Fatal("original restoration was replayed")
			}
		})
	}
}
