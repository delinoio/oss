// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestNativeShellPrivateSendFenceSurvivesLostOuterJournal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "worker")
	job := domain.NewID()
	send := nativeShellSend{Version: 1, JobID: job, ActionID: domain.NewID(), SessionID: domain.NewID(), ExecutionID: domain.NewID(), NativeThreadID: domain.NativeIdentity(domain.NewID()), Command: domain.NativeShellCommand{Command: "  exact script bytes  ", FullAccess: true, Actor: domain.Principal{Type: domain.OwnerDevice}}, ClaimRequestID: domain.NewID(), InputDigest: "original-assignment-digest"}
	if err := writeCompactionClaim(root, job, nativeShellSendClaim, send); err != nil {
		t.Fatal(err)
	}
	// No outer execution journal exists: its absence cannot reopen this send.
	if _, err := os.Lstat(filepath.Join(root, "jobs", string(job)+".json")); !os.IsNotExist(err) {
		t.Fatal("fixture unexpectedly supplied outer delivery proof")
	}
	path := filepath.Join(root, "jobs", string(job), string(nativeShellSendClaim))
	before, err := security.ReadPrivate(path, 128<<10)
	if err != nil {
		t.Fatal(err)
	}
	var retained nativeShellSend
	if domain.Decode(before, &retained) != nil || retained.Command.Command != send.Command.Command || retained.ActionID != send.ActionID {
		t.Fatal("private send claim replaced exact original bytes")
	}
	if err := writeCompactionClaim(root, job, nativeShellSendClaim, send); err == nil {
		t.Fatal("replayed claim reopened native send")
	}
	changed := send
	changed.Command.Command = "replacement"
	if err := writeCompactionClaim(root, job, nativeShellSendClaim, changed); err == nil {
		t.Fatal("changed command replaced original send fence")
	}
	after, err := security.ReadPrivate(path, 128<<10)
	if err != nil || string(before) != string(after) {
		t.Fatal("lost outer journal changed retained send intent", err)
	}
	raw, _ := json.Marshal(send)
	if string(raw) != string(after) {
		t.Fatal("private claim lost canonical original identity")
	}
}
func TestNativeShellCheckpointPathCannotAdoptForeignFile(t *testing.T) {
	if _, err := nativeShellCheckpointPath(t.TempDir(), "../../foreign"); err == nil {
		t.Fatal("foreign filesystem identity accepted")
	}
}
