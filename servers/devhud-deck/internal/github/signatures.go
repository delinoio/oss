package github

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

const callbackStateVersion = "v1"

type StatePurpose uint8

const (
	StatePurposeUnknown StatePurpose = iota
	StatePurposeOAuth
	StatePurposeInstallation
)

type CallbackState struct {
	Purpose        StatePurpose `json:"purpose"`
	AccountID      string       `json:"account_id"`
	Owner          OwnerBinding `json:"owner"`
	InstallationID uint64       `json:"installation_id,omitempty"`
	Nonce          string       `json:"nonce"`
	ExpiresAt      int64        `json:"expires_at"`
}

type StateSigner struct {
	key    []byte
	random io.Reader
}

func NewStateSigner(key []byte) (*StateSigner, error) {
	if len(key) < 32 {
		return nil, ErrInvalidConfiguration
	}
	return &StateSigner{key: append([]byte(nil), key...), random: rand.Reader}, nil
}

func (signer *StateSigner) Sign(
	purpose StatePurpose,
	accountID string,
	owner OwnerBinding,
	expiresAt time.Time,
) (string, CallbackState, error) {
	if signer == nil || len(signer.key) < 32 ||
		(purpose != StatePurposeOAuth && purpose != StatePurposeInstallation) ||
		accountID == "" || owner.Validate() != nil || expiresAt.IsZero() {
		return "", CallbackState{}, ErrInvalidConfiguration
	}
	nonce := make([]byte, 24)
	if _, err := io.ReadFull(signer.random, nonce); err != nil {
		return "", CallbackState{}, errors.New("deck github: state generation failed")
	}
	state := CallbackState{
		Purpose: purpose, AccountID: accountID, Owner: owner,
		Nonce:     base64.RawURLEncoding.EncodeToString(nonce),
		ExpiresAt: expiresAt.UTC().Unix(),
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return "", CallbackState{}, ErrInvalidConfiguration
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := signer.mac(callbackStateVersion + "." + encoded)
	return callbackStateVersion + "." + encoded + "." +
		base64.RawURLEncoding.EncodeToString(signature), state, nil
}

func (signer *StateSigner) SignOAuthForInstallation(
	accountID string,
	owner OwnerBinding,
	installationID uint64,
	expiresAt time.Time,
) (string, CallbackState, error) {
	if installationID == 0 {
		return "", CallbackState{}, ErrInvalidConfiguration
	}
	_, state, err := signer.Sign(
		StatePurposeOAuth, accountID, owner, expiresAt)
	if err != nil {
		return "", CallbackState{}, err
	}
	state.InstallationID = installationID
	return signer.signState(state)
}

func (signer *StateSigner) signState(
	state CallbackState,
) (string, CallbackState, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return "", CallbackState{}, ErrInvalidConfiguration
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := signer.mac(callbackStateVersion + "." + encoded)
	return callbackStateVersion + "." + encoded + "." +
		base64.RawURLEncoding.EncodeToString(signature), state, nil
}

func (signer *StateSigner) Verify(
	value string,
	purpose StatePurpose,
	now time.Time,
) (CallbackState, error) {
	if signer == nil || len(signer.key) < 32 {
		return CallbackState{}, ErrInvalidSignature
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != callbackStateVersion {
		return CallbackState{}, ErrInvalidSignature
	}
	actual, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(actual, signer.mac(parts[0]+"."+parts[1])) {
		return CallbackState{}, ErrInvalidSignature
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return CallbackState{}, ErrInvalidSignature
	}
	var state CallbackState
	if err := json.Unmarshal(payload, &state); err != nil ||
		state.Purpose != purpose || state.AccountID == "" ||
		state.Owner.Validate() != nil || len(state.Nonce) < 32 {
		return CallbackState{}, ErrInvalidSignature
	}
	if state.ExpiresAt <= now.UTC().Unix() {
		return CallbackState{}, ErrExpiredState
	}
	return state, nil
}

func (signer *StateSigner) mac(value string) []byte {
	mac := hmac.New(sha256.New, signer.key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func StateHash(state string) [sha256.Size]byte {
	return sha256.Sum256([]byte(state))
}

func WebhookSignature(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func VerifyWebhookSignature(secret, payload []byte, signature string) error {
	if len(secret) < 32 || !strings.HasPrefix(signature, "sha256=") {
		return ErrInvalidSignature
	}
	actual, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return ErrInvalidSignature
	}
	expectedHex := strings.TrimPrefix(WebhookSignature(secret, payload), "sha256=")
	expected, _ := hex.DecodeString(expectedHex)
	if !hmac.Equal(actual, expected) {
		return ErrInvalidSignature
	}
	return nil
}

func signedFixtureState(
	signer *StateSigner,
	purpose StatePurpose,
	accountID string,
	owner OwnerBinding,
	nonce string,
	expiresAt int64,
) (string, error) {
	state := CallbackState{
		Purpose: purpose, AccountID: accountID, Owner: owner, Nonce: nonce,
		ExpiresAt: expiresAt,
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	body := callbackStateVersion + "." + encoded
	return body + "." + base64.RawURLEncoding.EncodeToString(signer.mac(body)), nil
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil &&
		seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if timestamp, err := time.Parse(time.RFC1123, value); err == nil &&
		timestamp.After(now) {
		return timestamp.Sub(now)
	}
	return 0
}
