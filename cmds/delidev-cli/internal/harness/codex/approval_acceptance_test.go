package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func ownedPermissionFixture(t *testing.T) (*Client, domain.ID, Event, PermissionGrant) {
	t.Helper()
	c, _, turn, request := ownedApprovalFixture(t, "approvals", "item/permissions/requestApproval", map[string]any{"itemId": "permissions-tool", "startedAtMs": 1, "cwd": "/fixture", "permissions": map[string]any{"fileSystem": map[string]any{"write": []string{"/fixture"}}}})
	grant := PermissionGrant{Permissions: request.Interaction.Approval.Permissions.Permissions, Scope: PermissionTurn}
	return c, turn, request, grant
}
func permissionOutputFixture(t *testing.T, c *Client, turn domain.ID, item, output string) nativewire.Event {
	t.Helper()
	return nativewire.Event{Kind: nativewire.Notification, Method: "rawResponseItem/completed", Params: mustJSON(t, map[string]any{"threadId": c.thread, "turnId": turn, "item": map[string]any{"type": "function_call_output", "call_id": item, "output": output}})}
}

const exactPermissionOutput = `{"permissions":{"network":null,"file_system":{"write":["/fixture"]}},"scope":"turn"}`

func TestPermissionAcceptanceMatchesExactGrantScopeAndPreservesOtherRecovery(t *testing.T) {
	for _, priorRecovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "prior-recovery"}[priorRecovery], func(t *testing.T) {
			c, turn, request, grant := ownedPermissionFixture(t)
			responseID := domain.NewID()
			if _, err := c.GrantPermissions(context.Background(), responseID, request.Interaction.ID, turn, grant); err != nil {
				t.Fatal(err)
			}
			grant.Permissions.FileSystem.Write[0] = "/changed-after-send"
			nextKind(t, c, InteractionClosedEvent)
			if !c.execution.interactions.blocksInput() {
				t.Fatal("closure fabricated grant acceptance")
			}
			if priorRecovery {
				c.problem, c.execution.paused = interactionUncertain(), true
			}
			native := permissionOutputFixture(t, c, turn, request.ItemID, exactPermissionOutput)
			event, err := c.observeEventLocked(native)
			if err != nil || event.Kind != ApprovalAcceptedEvent || event.Native != nil || !event.Correlated || event.InteractionState == nil || !event.InteractionState.Accepted || event.InteractionState.ResponseID != responseID || event.InteractionState.ApprovalEvidence != PermissionOutputEvidence || event.InteractionState.Closure != InteractionNativeClosed {
				t.Fatal("exact native grant lost independent acceptance", err)
			}
			raw := string(mustJSON(t, event))
			if strings.Contains(raw, "/fixture") || strings.Contains(raw, "file_system") || strings.Contains(raw, "function_call_output") || c.execution.interactions.blocksInput() {
				t.Fatal("private grant escaped or confirmed response still blocks input")
			}
			duplicate, err := c.observeEventLocked(native)
			if err != nil || duplicate.Kind != MetadataEvent || duplicate.Metadata != RawSupplementDiscarded {
				t.Fatal("exact evidence published twice")
			}
			if priorRecovery && (c.problem == nil || !c.execution.paused) {
				t.Fatal("acceptance cleared unrelated recovery")
			}
			if !priorRecovery {
				fixtureSignal(t, c, "finish", map[string]any{"status": TurnCompleted})
				nextKind(t, c, TurnCompletedEvent)
				if _, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode)); err != nil {
					t.Fatal("confirmed permissions blocked next native turn", err)
				}
			}
		})
	}
}

func TestPermissionAcceptanceRejectsForeignAmbiguousAndChangedEvidence(t *testing.T) {
	for _, change := range []string{"scope", "strict", "path", "deny", "network", "empty", "null", "missing-scope", "missing-permissions", "unknown", "duplicate", "mixed-filesystem", "name", "namespace", "unknown-turn", "foreign-thread", "foreign-call", "no-send", "ambiguous"} {
		t.Run(change, func(t *testing.T) {
			c, turn, request, grant := ownedPermissionFixture(t)
			if change != "no-send" {
				if _, err := c.GrantPermissions(context.Background(), domain.NewID(), request.Interaction.ID, turn, grant); err != nil {
					t.Fatal(err)
				}
				nextKind(t, c, InteractionClosedEvent)
			}
			output := exactPermissionOutput
			switch change {
			case "scope":
				output = strings.Replace(output, `"turn"`, `"session"`, 1)
			case "strict":
				output = strings.TrimSuffix(output, "}") + `,"strict_auto_review":true}`
			case "path":
				output = strings.Replace(output, "/fixture", "/other", 1)
			case "deny":
				output = `{"permissions":{"file_system":{"entries":[{"access":"deny","path":{"type":"path","path":"/fixture"}}]}},"scope":"turn"}`
			case "network":
				output = strings.Replace(output, `"network":null`, `"network":{"enabled":true}`, 1)
			case "empty":
				output = `{"permissions":{},"scope":"turn"}`
			case "null":
				output = `{"permissions":null,"scope":"turn"}`
			case "missing-scope":
				output = `{"permissions":{"file_system":{"write":["/fixture"]}}}`
			case "missing-permissions":
				output = `{"scope":"turn"}`
			case "unknown":
				output = strings.TrimSuffix(output, "}") + `,"approved":true}`
			case "duplicate":
				output = strings.TrimSuffix(output, "}") + `,"scope":"turn"}`
			case "mixed-filesystem":
				output = `{"permissions":{"file_system":{"write":["/fixture"],"entries":[]}},"scope":"turn"}`
			}
			native := permissionOutputFixture(t, c, turn, request.ItemID, output)
			var fields map[string]any
			_ = json.Unmarshal(native.Params, &fields)
			item := fields["item"].(map[string]any)
			switch change {
			case "name":
				item["name"] = "request_user_input"
			case "namespace":
				item["namespace"] = "foreign"
			case "unknown-turn":
				fields["turnId"] = domain.NewID()
			case "foreign-thread":
				fields["threadId"] = domain.NewID()
			case "foreign-call":
				item["call_id"] = "foreign"
			case "ambiguous":
				other := approvalEvent(t, c, turn, "item/permissions/requestApproval", map[string]any{"itemId": request.ItemID, "startedAtMs": 2, "cwd": "/fixture", "permissions": map[string]any{}})
				other.ID = json.RawMessage(`8`)
				if _, err := c.observeEventLocked(other); err != nil {
					t.Fatal(err)
				}
			}
			native.Params = mustJSON(t, fields)
			event, err := c.observeEventLocked(native)
			if change == "foreign-thread" || change == "foreign-call" || change == "no-send" {
				if err != nil || event.Kind == ApprovalAcceptedEvent {
					t.Fatal("unowned output gained acceptance", err)
				}
			} else if err == nil {
				t.Fatal("changed/ambiguous output gained acceptance")
			}
			status, err := c.InspectInteraction(context.Background(), request.Interaction.ID)
			if err != nil || status.Accepted || !c.execution.interactions.blocksInput() {
				t.Fatal("invalid output changed retained acceptance")
			}
		})
	}
}

func TestPermissionEvidenceCanonicalizesOnlyNativeSerializationForms(t *testing.T) {
	legacy := PermissionGrant{Scope: PermissionSession, Permissions: PermissionProfile{FileSystem: &AdditionalFilePermissions{Read: []string{"/fixture/read"}, Write: []string{"/fixture/write"}}}}
	digest, err := permissionGrantDigest(legacy)
	if err != nil {
		t.Fatal(err)
	}
	core, err := decodePermissionOutput([]byte(`{"permissions":{"file_system":{"entries":[{"access":"read","path":{"type":"path","path":"/fixture/read"}},{"access":"write","path":{"type":"path","path":"/fixture/write"}}]}},"scope":"session","strict_auto_review":false}`))
	if err != nil {
		t.Fatal(err)
	}
	same, err := permissionGrantDigest(core)
	if err != nil || digest != same {
		t.Fatal("native legacy/canonical form changed exact grant")
	}
	core.Permissions.FileSystem.Entries[0], core.Permissions.FileSystem.Entries[1] = core.Permissions.FileSystem.Entries[1], core.Permissions.FileSystem.Entries[0]
	other, _ := permissionGrantDigest(core)
	if digest == other {
		t.Fatal("rule order was silently reinterpreted")
	}
	for _, output := range []string{`{"permissions":{},"scope":"turn","strict_auto_review":null}`, `{"permissions":{},"scope":"forever"}`, `{"permissions":{},"scope":"session","strict_auto_review":true}`} {
		grant, err := decodePermissionOutput([]byte(output))
		if err == nil {
			_, err = permissionGrantDigest(grant)
		}
		if err == nil {
			t.Fatal("invalid native permission evidence accepted")
		}
	}
}
