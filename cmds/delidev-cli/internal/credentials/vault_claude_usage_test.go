// SPDX-License-Identifier: Apache-2.0
package credentials

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestClaudeUsagePurposeIsDistinctFromExecutionLoginReferences(t *testing.T) {
	v, _, logs := setup(t)
	ctx := context.Background()
	owner := domain.NewID()
	usage := Ref{Owner: owner, ID: domain.NewID(), Purpose: ClaudeUsage}
	login := Ref{Owner: owner, ID: usage.ID, Purpose: AccountLogin}
	if _, err := v.Put(ctx, usage, []byte("usage-secret-value")); err != nil {
		t.Fatal(err)
	}
	// The same owner and reference identity never aliases across purposes.
	if _, err := v.Get(ctx, login); err == nil {
		t.Fatal("an execution login reference must not read a usage credential")
	}
	got, err := v.Get(ctx, usage)
	if err != nil || !bytes.Equal(got, []byte("usage-secret-value")) {
		t.Fatalf("usage credential not readable: %v", err)
	}
	refs, err := v.UnremovedReferences(ctx, owner)
	if err != nil || len(refs) != 1 || refs[0] != usage {
		t.Fatalf("references %+v %v", refs, err)
	}
	if bytes.Contains(logs.Bytes(), []byte("usage-secret-value")) {
		t.Fatal("logs must not carry secret bytes")
	}
	if err := v.Delete(ctx, usage); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Get(ctx, usage); err == nil {
		t.Fatal("a deleted usage reference must stay unreadable")
	}
	if _, err := v.Put(ctx, usage, []byte("usage-secret-value")); err == nil {
		t.Fatal("a deleted usage reference must never be resurrected")
	}
}

func TestClaudeUsageReadDoesNotLogItsPrivateReferenceIdentity(t *testing.T) {
	v, _, logs := setup(t)
	ctx := context.Background()
	usage := Ref{Owner: domain.NewID(), ID: domain.NewID(), Purpose: ClaudeUsage}
	if _, err := v.Put(ctx, usage, []byte("usage-secret-value")); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	got, err := v.Get(ctx, usage)
	if err != nil {
		t.Fatal(err)
	}
	clear(got)
	if bytes.Contains(logs.Bytes(), []byte(string(usage.ID))) {
		t.Fatal("a private usage reference identity must not be logged by reads")
	}
}
