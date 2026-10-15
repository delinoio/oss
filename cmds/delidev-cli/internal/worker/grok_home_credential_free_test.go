// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"os"
	"testing"
)

// The credential-free home table. A lease is never released over a plaintext
// credential this Worker did not account for:
//
//	credential file in the bound home           | before the Take                      | after the Take, no attempt (finish)
//	absent                                      | Take proceeds                        | cleanup confirmed
//	regular file (leftover or appeared)         | retired, Take proceeds               | retired, cleanup confirmed
//	entry that cannot be retired                | refused, no lease                    | cleanup not confirmed, lease stays fenced
func TestGrokExecutionRetiresALeftoverCredentialBeforeTheTake(t *testing.T) {
	f := newGrokExecutionServer(t)
	if err := os.WriteFile(f.authPath(), []byte(`{"leftover":"credential"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := f.begin(t)
	if err != nil {
		t.Fatalf("a retirable leftover credential blocked the Take: %v", err)
	}
	if _, err := os.Lstat(f.authPath()); !os.IsNotExist(err) {
		t.Fatal("the leftover credential stayed in the bound home")
	}
	if len(f.takes) != 1 {
		t.Fatalf("takes = %d", len(f.takes))
	}
	if err := run.finish(true); err != nil {
		t.Fatal(err)
	}
}

func TestGrokExecutionRefusesAHomeWhoseLeftoverCredentialCannotBeRetiredBeforeTheTake(t *testing.T) {
	f := newGrokExecutionServer(t)
	// A directory named like the credential cannot be retired as a credential file.
	if err := os.Mkdir(f.authPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.begin(t); err == nil {
		t.Fatal("a home with an unretirable credential entry was admitted")
	}
	if len(f.takes) != 0 {
		t.Fatalf("a lease was taken for an unprovable home: %d takes", len(f.takes))
	}
}

func TestGrokExecutionThatNeverMaterializedRetiresACredentialThatAppearedInTheHome(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	// The profile probe refuses the run when a credential appears after the Take,
	// so the credential callback never runs and nothing was attempted.
	if err := os.WriteFile(f.authPath(), []byte(`{"appeared":"credential"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run.release(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f.authPath()); !os.IsNotExist(err) {
		t.Fatal("the Finish released the lease over a plaintext credential")
	}
	request := f.onlyFinish(t)
	if request.Succeeded || !request.CleanupConfirmed {
		t.Fatalf("the retired credential was not confirmed: %+v", request)
	}
}

func TestGrokExecutionThatNeverMaterializedDoesNotConfirmAnUnretirableCredentialEntry(t *testing.T) {
	f := newGrokExecutionServer(t)
	run, err := f.begin(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.authPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := run.release(true); err != nil {
		t.Fatal(err)
	}
	if request := f.onlyFinish(t); request.CleanupConfirmed || request.Succeeded {
		t.Fatalf("cleanup was confirmed over an entry that could not be retired: %+v", request)
	}
}
