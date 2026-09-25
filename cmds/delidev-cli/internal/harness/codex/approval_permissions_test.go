package codex

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func decodePermissionFixture(t *testing.T, raw string) PermissionProfile {
	t.Helper()
	var result PermissionProfile
	if err := domain.Decode([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNativePermissionDescriptorsPreserveClosedUnions(t *testing.T) {
	raw := `{"network":{"enabled":true},"fileSystem":{"read":["/fixture/read"],"write":["/fixture/write"],"globScanMaxDepth":3,"entries":[{"access":"write","path":{"type":"path","path":"/fixture"}},{"access":"deny","path":{"type":"glob_pattern","pattern":"**/.env"}},{"access":"read","path":{"type":"special","value":{"kind":"project_roots","subpath":"src"}}},{"access":"read","path":{"type":"special","value":{"kind":"unknown","path":"future-path","subpath":null}}}]}}`
	original := decodePermissionFixture(t, raw)
	encoded, err := json.Marshal(original)
	if err != nil || !reflect.DeepEqual(original, decodePermissionFixture(t, string(encoded))) {
		t.Fatal("permission descriptor roundtrip changed native meaning", err)
	}
	for _, bad := range []string{
		`{"network":{"enabled":"true"}}`,
		`{"network":{"enabled":true,"bypass":true}}`,
		`{"fileSystem":{"read":[null]}}`,
		`{"fileSystem":{"globScanMaxDepth":0}}`,
		`{"fileSystem":{"globScanMaxDepth":-1}}`,
		`{"fileSystem":{"globScanMaxDepth":1.5}}`,
		`{"fileSystem":{"entries":[null]}}`,
		`{"fileSystem":{"entries":[{"access":"none","path":{"type":"path","path":"/fixture"}}]}}`,
		`{"fileSystem":{"entries":[{"access":"write","path":{"type":"path","path":"/fixture","pattern":null}}]}}`,
		`{"fileSystem":{"entries":[{"access":"write","path":{"type":"glob_pattern","path":null,"pattern":"**"}}]}}`,
		`{"fileSystem":{"entries":[{"access":"read","path":{"type":"special","value":{"kind":"root","subpath":null}}}]}}`,
		`{"fileSystem":{"entries":[{"access":"read","path":{"type":"special","value":{"kind":"project_roots","path":null}}}]}}`,
		`{"fileSystem":{"entries":[{"access":"read","path":{"type":"special","value":{"kind":"unknown"}}}]}}`,
		`{"fileSystem":{"entries":[{"access":"read","path":{"type":"special","value":{"kind":"future"}}}]}}`,
		`{"fileSystem":{"entries":[{"access":"read","path":{"type":"path","path":"/fixture","path":"/other"}}]}}`,
	} {
		var profile PermissionProfile
		if domain.Decode([]byte(bad), &profile) == nil {
			t.Fatalf("invalid permission descriptor accepted: %s", bad)
		}
	}
}

func TestNativePermissionGrantsCannotBroadenOrDropRequestedRestrictions(t *testing.T) {
	requested := decodePermissionFixture(t, `{"network":{"enabled":true},"fileSystem":{"read":["/fixture/read"],"write":["/fixture/write"],"globScanMaxDepth":3,"entries":[{"access":"write","path":{"type":"path","path":"/fixture"}},{"access":"deny","path":{"type":"glob_pattern","pattern":"**/.env"}}]}}`)
	request := &PermissionsApprovalRequest{Cwd: "/fixture", Permissions: requested}
	for _, raw := range []string{
		`{}`,
		`{"network":{"enabled":false}}`,
		`{"fileSystem":{"read":["/fixture/write"],"entries":[{"access":"deny","path":{"type":"glob_pattern","pattern":"**/.env"}}]}}`,
		`{"fileSystem":{"entries":[{"access":"read","path":{"type":"path","path":"/fixture"}},{"access":"deny","path":{"type":"glob_pattern","pattern":"**/.env"}}]}}`,
	} {
		grant := PermissionGrant{Scope: PermissionTurn, Permissions: decodePermissionFixture(t, raw)}
		if err := grant.validate(request); err != nil {
			t.Fatal("valid explicit reduction rejected", err)
		}
	}
	for _, raw := range []string{
		`{"fileSystem":{"write":["/new"]}}`,
		`{"fileSystem":{"write":["/fixture/read"]}}`,
		`{"fileSystem":{"globScanMaxDepth":4}}`,
		`{"fileSystem":{"entries":[{"access":"write","path":{"type":"path","path":"/fixture"}}]}}`,
		`{"fileSystem":{"read":["/fixture/write"]}}`,
		`{"fileSystem":{"entries":[{"access":"read","path":{"type":"path","path":"/fixture/child"}},{"access":"deny","path":{"type":"glob_pattern","pattern":"**/.env"}}]}}`,
		`{"fileSystem":{"entries":[{"access":"write","path":{"type":"glob_pattern","pattern":"**/.env"}}]}}`,
	} {
		grant := PermissionGrant{Scope: PermissionTurn, Permissions: decodePermissionFixture(t, raw)}
		assertCode(t, grant.validate(request), domain.InvalidArgument)
	}
	grant := PermissionGrant{Scope: PermissionSession, Permissions: requested}
	if err := grant.validate(request); err != nil {
		t.Fatal("exact requested session grant failed", err)
	}
	strict := true
	grant.StrictAutoReview = &strict
	assertCode(t, grant.validate(request), domain.InvalidArgument)
	grant.Scope = PermissionTurn
	if err := grant.validate(request); err != nil {
		t.Fatal("strict turn grant failed", err)
	}
	assertCode(t, grant.validate(&PermissionsApprovalRequest{}), domain.InvalidArgument)
	grant.Scope = ""
	assertCode(t, grant.validate(request), domain.InvalidArgument)
}

func TestNativePermissionsApprovalTransmitsOnlyItsOriginalGrant(t *testing.T) {
	permissions := decodePermissionFixture(t, `{"network":{"enabled":true},"fileSystem":{"read":["/fixture"]}}`)
	c, _, turn, event := ownedApprovalFixture(t, "approvals", "item/permissions/requestApproval", map[string]any{"itemId": "permissions-tool", "startedAtMs": 1, "cwd": "/fixture", "permissions": permissions})
	if event.Interaction.Approval.Kind != PermissionsApproval || !reflect.DeepEqual(event.Interaction.Approval.Permissions.Permissions, permissions) {
		t.Fatal("permission request changed")
	}
	_, err := c.RespondApproval(context.Background(), domain.NewID(), event.Interaction.ID, turn, ApprovalDecision{Kind: ApprovalAccept})
	assertCode(t, err, domain.InvalidArgument)
	event.Interaction.Approval.Permissions.Permissions.FileSystem.Read[0] = "/other"
	_, err = c.GrantPermissions(context.Background(), domain.NewID(), event.Interaction.ID, turn, PermissionGrant{Scope: PermissionTurn, Permissions: decodePermissionFixture(t, `{"fileSystem":{"read":["/other"]}}`)})
	assertCode(t, err, domain.InvalidArgument)
	response, err := c.GrantPermissions(context.Background(), domain.NewID(), event.Interaction.ID, turn, PermissionGrant{Scope: PermissionTurn, Permissions: permissions})
	if err != nil || response.Delivery != QuestionTransmitted || response.Accepted {
		t.Fatal("exact grant failed or implied acceptance", err)
	}
	closed := nextKind(t, c, InteractionClosedEvent)
	if closed.InteractionState.Accepted || !c.execution.interactions.blocksInput() {
		t.Fatal("permission closure inferred semantic acceptance")
	}
}

func TestNativePermissionEntriesOverrideDeprecatedMirrors(t *testing.T) {
	for _, entries := range []string{`[]`, `[{"access":"write","path":{"type":"path","path":"/effective"}}]`} {
		requested := decodePermissionFixture(t, `{"fileSystem":{"write":["/ignored"],"entries":`+entries+`}}`)
		request := &PermissionsApprovalRequest{Cwd: "/fixture", Permissions: requested}
		before, _ := json.Marshal(request)
		for _, test := range []struct {
			profile string
			allowed bool
		}{
			{`{"fileSystem":{"write":["/ignored"]}}`, false},
			{`{"fileSystem":{"read":["/effective"]}}`, entries != `[]`},
			{`{"fileSystem":{"write":["/ignored"],"entries":[]}}`, true},
			{`{"fileSystem":{"entries":` + entries + `}}`, true},
		} {
			grant := PermissionGrant{Scope: PermissionTurn, Permissions: decodePermissionFixture(t, test.profile)}
			if (grant.validate(request) == nil) != test.allowed {
				t.Fatal("native entries precedence changed grant authority")
			}
		}
		after, _ := json.Marshal(request)
		if string(before) != string(after) {
			t.Fatal("validation rewrote original native request")
		}
		full, err := permissionGrantDigest(PermissionGrant{Scope: PermissionTurn, Permissions: requested})
		if err != nil {
			t.Fatal(err)
		}
		effective := decodePermissionFixture(t, `{"fileSystem":{"entries":`+entries+`}}`)
		canonical, err := permissionGrantDigest(PermissionGrant{Scope: PermissionTurn, Permissions: effective})
		if err != nil || full != canonical {
			t.Fatal("ignored mirrors changed exact native processing evidence", err)
		}
		if entries == `[]` {
			empty, err := permissionGrantDigest(PermissionGrant{Scope: PermissionTurn})
			if err != nil || full != empty {
				t.Fatal("present empty entries did not override legacy access")
			}
		}
	}
}
