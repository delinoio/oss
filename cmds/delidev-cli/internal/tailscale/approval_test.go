// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
	"time"
)

func approvalFixture(t *testing.T) (*Approvals, ApprovalInput, []byte) {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manager := &Approvals{Root: t.TempDir(), ServerID: domain.NewID(), Origin: "https://fixture.tailnet.ts.net:8443"}
	input := ApprovalInput{ID: domain.NewID(), RequesterName: "Fixture requester", RequesterKey: key.PublicKey().Bytes(), ServerID: manager.ServerID, Origin: manager.Origin, Role: domain.ClientDevice}
	return manager, input, key.Bytes()
}
func TestApprovalOriginalGrantEncryptedOnce(t *testing.T) {
	manager, input, private := approvalFixture(t)
	a, err := manager.Request(input)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	decision := domain.NewID()
	issue := func(_ context.Context, _ domain.ID, _ string, _ domain.DeviceType, _ []byte) ([]byte, error) {
		count++
		return []byte("private-grant-sentinel"), nil
	}
	approved, err := manager.Decide(context.Background(), input.ID, decision, a.Code, true, "owner", issue)
	if err != nil || count != 1 {
		t.Fatal("original decision failed", err)
	}
	plain, err := OpenGrant(private, approved)
	if err != nil || string(plain) != "private-grant-sentinel" || bytes.Contains(approved.EncryptedGrant, plain) {
		t.Fatal("grant encryption failed")
	}
	manager = &Approvals{Root: manager.Root, ServerID: manager.ServerID, Origin: manager.Origin}
	replay, err := manager.Decide(context.Background(), input.ID, decision, a.Code, true, "owner", issue)
	if err != nil || count != 1 || !bytes.Equal(replay.EncryptedGrant, approved.EncryptedGrant) {
		t.Fatal("decision replay repeated grant")
	}
	changed := approved
	changed.Input.Role = domain.WorkerDevice
	if _, err := OpenGrant(private, changed); err == nil {
		t.Fatal("role change decrypted grant")
	}
	if _, err := manager.Decide(context.Background(), input.ID, decision, a.Code, true, "other-client", issue); err == nil {
		t.Fatal("decision actor changed")
	}
}
func TestApprovalLostGrantAcknowledgmentRetainsOriginalIntent(t *testing.T) {
	manager, input, _ := approvalFixture(t)
	a, err := manager.Request(input)
	if err != nil {
		t.Fatal(err)
	}
	decision := domain.NewID()
	var original domain.ID
	var originalCode string
	calls := 0
	issue := func(_ context.Context, id domain.ID, code string, _ domain.DeviceType, _ []byte) ([]byte, error) {
		calls++
		if calls == 1 {
			original = id
			originalCode = code
			return nil, errors.New("fixture response lost")
		}
		if id != original || code != originalCode {
			t.Fatal("uncertainty replaced grant ownership")
		}
		return []byte("original receipt grant"), nil
	}
	if _, err := manager.Decide(context.Background(), input.ID, decision, a.Code, true, "owner", issue); err == nil {
		t.Fatal("lost reply became success")
	}
	// Expiry prevents new consent; an already accepted decision still reconciles
	// its exact original idempotent Device operation rather than minting another.
	manager.Now = func() time.Time { return a.ExpiresAt.Add(time.Minute) }
	replay, err := manager.Decide(context.Background(), input.ID, decision, a.Code, true, "owner", issue)
	if err != nil || replay.State != ApprovalApproved || calls != 2 {
		t.Fatal("original recovery failed", err)
	}
}
func TestApprovalDenialExpiryCancellationGrantNoAuthority(t *testing.T) {
	for _, mode := range []string{"deny", "expire", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			manager, input, _ := approvalFixture(t)
			a, err := manager.Request(input)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			issue := func(context.Context, domain.ID, string, domain.DeviceType, []byte) ([]byte, error) {
				calls++
				return nil, nil
			}
			allow := true
			if mode == "deny" {
				allow = false
			}
			if mode == "expire" {
				manager.Now = func() time.Time { return a.ExpiresAt.Add(time.Second) }
			}
			if mode == "cancel" {
				if _, err := manager.Cancel(input.ID, input.RequesterKey); err != nil {
					t.Fatal(err)
				}
			}
			result, err := manager.Decide(context.Background(), input.ID, domain.NewID(), a.Code, allow, "owner", issue)
			if err != nil || calls != 0 || result.State == ApprovalApproved {
				t.Fatal("terminal request created grant")
			}
		})
	}
}
func TestApprovalRejectsChangedTranscriptAndInvalidKey(t *testing.T) {
	manager, input, _ := approvalFixture(t)
	if _, err := manager.Request(input); err != nil {
		t.Fatal(err)
	}
	input.Origin = "https://foreign.tailnet.ts.net:8443"
	if _, err := manager.Request(input); err == nil {
		t.Fatal("foreign origin adopted")
	}
	input.Origin = manager.Origin
	input.Role = domain.WorkerDevice
	if _, err := manager.Request(input); err == nil {
		t.Fatal("changed role adopted")
	}
	input.ID = domain.NewID()
	input.RequesterKey = make([]byte, 32)
	if _, err := manager.Request(input); err == nil {
		t.Fatal("invalid X25519 peer key accepted")
	}
}

func TestWorkerDeliveryRetainsTargetServerAndOriginalAttempt(t *testing.T) {
	requester, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	input := ApprovalInput{ID: domain.NewID(), RequesterName: "fixture requester", RequesterKey: requester.PublicKey().Bytes(), ServerID: domain.NewID(), Origin: "https://target.fixture.ts.net:8443", Role: domain.WorkerDevice, WorkerServerID: domain.NewID(), WorkerServerOrigin: "https://selected.fixture.ts.net:8443"}
	a := &Approvals{Root: t.TempDir(), ServerID: input.ServerID, Origin: input.Origin}
	pending, err := a.Request(input)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := a.Decide(context.Background(), input.ID, domain.NewID(), pending.Code, true, "original-client", func(_ context.Context, _ domain.ID, code string, _ domain.DeviceType, _ []byte) ([]byte, error) {
		return json.Marshal(struct {
			Token string `json:"token"`
		}{code})
	})
	if err != nil {
		t.Fatal(err)
	}
	ticketRaw, err := OpenGrant(requester.Bytes(), approved)
	if err != nil {
		t.Fatal(err)
	}
	var ticket struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(ticketRaw, &ticket) != nil {
		t.Fatal("missing encrypted approval ticket")
	}
	payload, _ := json.Marshal(WorkerDelivery{Token: ticket.Token, Grant: json.RawMessage(`{"fixture":true}`)})
	sealed, err := SealWorkerDelivery(requester.Bytes(), approved, payload)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	device := domain.NewID()
	consume := func(_ context.Context, selected ApprovalInput, _ json.RawMessage, fresh bool) (domain.ID, error) {
		calls++
		if selected.WorkerServerID != input.WorkerServerID || selected.WorkerServerOrigin != input.WorkerServerOrigin || selected.ServerID != input.ServerID {
			t.Fatal("Worker selected wrong server")
		}
		if calls == 1 {
			if !fresh {
				t.Fatal("first delivery not retained")
			}
			return "", errors.New("lost pairing reply")
		}
		if fresh {
			t.Fatal("retry reconstructed pairing")
		}
		return device, nil
	}
	if _, err = a.DeliverWorker(context.Background(), input.ID, input.RequesterKey, sealed, consume); err == nil {
		t.Fatal("lost reply treated as completion")
	}
	restarted := &Approvals{Root: a.Root, ServerID: a.ServerID, Origin: a.Origin}
	if got, err := restarted.DeliverWorker(context.Background(), input.ID, input.RequesterKey, sealed, consume); err != nil || got != device {
		t.Fatal("original delivery did not reconcile", err)
	}
	if _, err = restarted.DeliverWorker(context.Background(), input.ID, input.RequesterKey, sealed, consume); err != nil || calls != 2 {
		t.Fatal("completed delivery repeated mutation")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err = restarted.DeliverWorker(context.Background(), input.ID, input.RequesterKey, sealed, consume); err == nil {
		t.Fatal("changed delivery accepted")
	}
}
