// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeShellExclusiveOriginalClaimCannotAuthorizeAnotherSend(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	owner, thread := domain.NewID(), domain.NewID()
	input := domain.SessionCompactionInput{ActionID: domain.NewID(), Shell: &domain.NativeShellCommand{Command: "printf original", FullAccessConfirmed: true}}
	config := Config{Root: root, execution: &PublicationConfig{Assignment: &pb.Resource{Id: string(owner), Revision: 2, DocumentJson: []byte(`{"original":true}`)}}}
	claim := nativeShellSendClaimFor(config, owner, input, thread)
	if err := writeCompactionClaim(root, owner, nativeShellSendClaim, claim); err != nil {
		t.Fatal(err)
	}
	if err := verifyNativeShellSendClaim(config, owner, input, thread); err != nil {
		t.Fatal("original claim rejected", err)
	}
	if err := writeCompactionClaim(root, owner, nativeShellSendClaim, claim); err == nil {
		t.Fatal("exclusive send claim overwritten")
	}
	if err := verifyNativeShellSendClaim(config, owner, input, domain.NewID()); err == nil {
		t.Fatal("foreign native thread adopted original claim")
	}
	input.Shell.Command = "replacement"
	if err := verifyNativeShellSendClaim(config, owner, input, thread); err == nil {
		t.Fatal("changed command adopted original claim")
	}
	input.Shell.Command = claim.Command.Command
	config.execution.Assignment.DocumentJson = []byte(`{"replacement":true}`)
	if err := verifyNativeShellSendClaim(config, owner, input, thread); err == nil {
		t.Fatal("changed assignment adopted original claim")
	}
	config.execution.Assignment.DocumentJson = []byte(`{"original":true}`)
	if err := os.Remove(filepath.Join(root, "jobs", string(owner), string(nativeShellSendClaim))); err != nil {
		t.Fatal(err)
	}
	if err := verifyNativeShellSendClaim(config, owner, input, thread); err == nil {
		t.Fatal("missing original claim fabricated cleanup authority")
	}
}
