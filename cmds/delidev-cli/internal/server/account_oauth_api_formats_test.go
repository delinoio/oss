// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Synthetic OAuth registration/exchange fixtures establish local ownership only.
func TestOAuthAPIFormatsPinAccountAndInspection(t *testing.T) {
	for _, hf := range []bool{false, true} {
		for _, protocol := range []domain.APIProtocol{domain.OpenAIResponses, domain.OpenAIChat, domain.AnthropicMessages} {
			name := string(protocol)
			if hf {
				name = "hugging-face/" + name
			}
			t.Run(name, func(t *testing.T) {
				f := newOAuthFixture(t)
				if hf {
					f = newHuggingFaceFixture(t)
				}
				var calls atomic.Int32
				f.s.oauthExchange = oauthExchangeFunc(func(context.Context, []byte, []byte) ([]byte, error) {
					calls.Add(1)
					return []byte("fixture-format-key"), nil
				})
				f.s.oauthTokenClient = oauthTokenFixture{exchange: func(context.Context, oauthProfile, []byte, []byte, string) (oauthTokenResult, error) {
					calls.Add(1)
					return tokenResult("fixture-access", "fixture-refresh", time.Now().Add(time.Hour)), nil
				}}
				input := &pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID()), ApiProtocol: rpc.WireAPIFormat(domain.ProviderAPIFormat{Protocol: protocol}).Protocol}
				if hf {
					input.CallbackUrl = "http://localhost:55451/oauth/hugging-face/callback"
				}
				r, err := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(input))
				if err != nil {
					t.Fatal(err)
				}
				start := r.Msg
				replay, err := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(input))
				if err != nil || !replay.Msg.Replayed || replay.Msg.Attempt.ApiProtocol != input.ApiProtocol {
					t.Fatal("format replay", err)
				}
				input.ApiProtocol = pb.ApiProtocol_API_PROTOCOL_UNSPECIFIED
				_, err = f.s.StartAccountOAuth(f.ctx, connect.NewRequest(input))
				wantAccountCode(t, err, domain.Conflict)
				completion := &pb.CompleteAccountOAuthRequest{Mutation: oauthMutation(start.Attempt, domain.NewID()), AuthorizationCode: []byte("fixture-original-code")}
				if hf {
					u, _ := url.Parse(start.AuthorizationUrl)
					completion.AuthorizationState = []byte(u.Query().Get("state"))
				}
				result, err := f.s.CompleteAccountOAuth(f.ctx, connect.NewRequest(completion))
				if err != nil || result.Msg.Account == nil {
					t.Fatal("completion", err)
				}
				account := accountBody(t, result.Msg.Account)
				if result.Msg.Account.SchemaVersion != 3 || account.APIProtocol != protocol || account.Connection.APIFormat == nil || account.Connection.APIFormat.Protocol != protocol {
					t.Fatal("lost selected connection")
				}
				status, err := f.s.GetAccountOAuthStatus(f.ctx, connect.NewRequest(&pb.GetAccountOAuthStatusRequest{AttemptId: start.Attempt.Id}))
				if err != nil || status.Msg.Attempt.ApiProtocol != start.Attempt.ApiProtocol {
					t.Fatal("status lost selection", err)
				}
				if err := f.s.Store.Read(f.ctx, func(tx *store.Tx) error {
					_, p, e := inspectionPreflight(tx, disconnectAccountInput{ID: domain.ID(result.Msg.Account.Id), Revision: result.Msg.Account.Revision}, validationInspection)
					if e == nil && p.Protocol != protocol {
						t.Error("inspection used default")
					}
					return e
				}); err != nil {
					t.Fatal(err)
				}
				credential, err := f.s.resolveAPICredential(f.ctx, domain.ID(result.Msg.Account.Id), account.Connection.ID, domain.ID(f.provider.Id))
				if err != nil || len(credential.key) == 0 {
					t.Fatal("credential ownership", err)
				}
				clear(credential.key)
				if calls.Load() != 1 {
					t.Fatal("exchange repeated")
				}
			})
		}
	}
}

func TestOAuthAPIFormatRecoveryKeepsOriginalSelection(t *testing.T) {
	f := newOAuthFixture(t)
	start, err := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID()), ApiProtocol: pb.ApiProtocol_API_PROTOCOL_OPENAI_RESPONSES}))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	f.s.oauthExchange = oauthExchangeFunc(func(context.Context, []byte, []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("fixture-recovery-key"), nil
	})
	id := domain.NewID()
	f.vault.putError = domain.Fail(domain.RecoveryRequired, "Lost acknowledgment.", "")
	result, err := f.complete(start.Msg.Attempt, id, "fixture-code")
	if err != nil || result.Msg.Attempt.State != pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED {
		t.Fatal("uncertain save", err)
	}
	f.vault.putError = nil
	f.restart(t)
	result, err = f.complete(start.Msg.Attempt, id, "")
	if err != nil || result.Msg.Account == nil {
		t.Fatal("original recovery", err)
	}
	if accountBody(t, result.Msg.Account).APIProtocol != domain.OpenAIResponses || calls.Load() != 1 {
		t.Fatal("recovery changed format or exchanged again")
	}
}

func TestOAuthAPIFormatAdmissionRejectsUnknownWithoutAttempt(t *testing.T) {
	f := newOAuthFixture(t)
	_, err := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID()), ApiProtocol: pb.ApiProtocol(999)}))
	wantAccountCode(t, err, domain.Unsupported)
	if e := f.s.Store.Read(f.ctx, func(tx *store.Tx) error {
		n, e := tx.AccountOAuthPending()
		if n != 0 {
			t.Error("invalid selection admitted attempt")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}

func TestDeviceOAuthRejectsUndeclaredResponsesBeforeAuthorization(t *testing.T) {
	f := newDeviceFixture(t)
	var calls atomic.Int32
	f.s.oauthDeviceClient = deviceFixtureClient{authorize: func(context.Context, oauthProfile) (oauthDeviceGrant, error) {
		calls.Add(1)
		return deviceGrant(), nil
	}}
	_, err := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID()), ApiProtocol: pb.ApiProtocol_API_PROTOCOL_OPENAI_RESPONSES}))
	wantAccountCode(t, err, domain.Unsupported)
	if calls.Load() != 0 {
		t.Fatal("unsupported format started authorization")
	}
}

func TestDeviceOAuthSelectedMessagesSurviveProtectedRecovery(t *testing.T) {
	f := newDeviceFixture(t)
	var polls atomic.Int32
	f.vault.putError = domain.Fail(domain.RecoveryRequired, "Lost acknowledgment.", "")
	f.s.oauthDeviceClient = deviceFixtureClient{authorize: func(context.Context, oauthProfile) (oauthDeviceGrant, error) { return deviceGrant(), nil }, poll: func(context.Context, oauthProfile, []byte) (oauthTokenResult, oauthDevicePoll, error) {
		polls.Add(1)
		return tokenResult("fixture-device-access", "fixture-device-refresh", time.Now().Add(time.Hour)), oauthDeviceIssued, nil
	}}
	start, err := f.s.StartAccountOAuth(f.ctx, connect.NewRequest(&pb.StartAccountOAuthRequest{Provider: acctMutation(f.provider, domain.NewID()), ApiProtocol: pb.ApiProtocol_API_PROTOCOL_ANTHROPIC_MESSAGES}))
	if err != nil {
		t.Fatal(err)
	}
	status := f.deviceStatus(t, start.Msg.Attempt.Id, pb.AccountOAuthState_ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED)
	f.deviceJoined(t)
	f.vault.putError = nil
	result, err := f.s.CompleteAccountOAuth(f.ctx, connect.NewRequest(&pb.CompleteAccountOAuthRequest{Mutation: oauthMutation(start.Msg.Attempt, domain.ID(status.RequestId))}))
	if err != nil || result.Msg.Account == nil {
		t.Fatal("device recovery", err)
	}
	if accountBody(t, result.Msg.Account).APIProtocol != domain.AnthropicMessages || polls.Load() != 1 {
		t.Fatal("device selection lost or polling repeated")
	}
}
