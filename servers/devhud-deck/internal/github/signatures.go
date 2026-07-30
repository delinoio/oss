package github

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

const callbackStateVersion = "v2"

type StatePurpose uint8

const (
	StatePurposeUnknown StatePurpose = iota
	StatePurposeOAuth
	StatePurposeInstallation
)

type CallbackState struct {
	Purpose        StatePurpose `json:"purpose"`
	AccountID      string       `json:"account_id"`
	GitHubLogin    string       `json:"github_login"`
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
	githubLogin string,
	owner OwnerBinding,
	expiresAt time.Time,
) (string, CallbackState, error) {
	if signer == nil || len(signer.key) < 32 ||
		(purpose != StatePurposeOAuth && purpose != StatePurposeInstallation) ||
		accountID == "" || !safePathSegment(githubLogin) ||
		owner.Validate() != nil || expiresAt.IsZero() {
		return "", CallbackState{}, ErrInvalidConfiguration
	}
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(signer.random, nonce); err != nil {
		return "", CallbackState{}, errors.New("deck github: state generation failed")
	}
	state := CallbackState{
		Purpose: purpose, AccountID: accountID,
		GitHubLogin: githubLogin, Owner: owner,
		Nonce:     base64.RawURLEncoding.EncodeToString(nonce),
		ExpiresAt: expiresAt.UTC().Unix(),
	}
	return signer.signState(state)
}

func (signer *StateSigner) SignOAuthForInstallation(
	accountID string,
	githubLogin string,
	owner OwnerBinding,
	installationID uint64,
	expiresAt time.Time,
) (string, CallbackState, error) {
	if installationID == 0 {
		return "", CallbackState{}, ErrInvalidConfiguration
	}
	_, state, err := signer.Sign(
		StatePurposeOAuth, accountID, githubLogin, owner, expiresAt)
	if err != nil {
		return "", CallbackState{}, err
	}
	state.InstallationID = installationID
	return signer.signState(state)
}

func (signer *StateSigner) signState(
	state CallbackState,
) (string, CallbackState, error) {
	if state.Purpose != StatePurposeOAuth &&
		state.Purpose != StatePurposeInstallation {
		return "", CallbackState{}, ErrInvalidConfiguration
	}
	nonce, err := base64.RawURLEncoding.DecodeString(state.Nonce)
	if err != nil || len(nonce) != 32 {
		return "", CallbackState{}, ErrInvalidConfiguration
	}
	handle := callbackStateVersion + "." + state.Nonce
	signature := signer.mac(
		strconv.Itoa(int(state.Purpose)) + "." + handle)
	return handle + "." +
		base64.RawURLEncoding.EncodeToString(signature), state, nil
}

func (signer *StateSigner) Verify(
	value string,
	purpose StatePurpose,
) error {
	if signer == nil || len(signer.key) < 32 {
		return ErrInvalidSignature
	}
	if purpose != StatePurposeOAuth && purpose != StatePurposeInstallation {
		return ErrInvalidSignature
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != callbackStateVersion {
		return ErrInvalidSignature
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(nonce) != 32 {
		return ErrInvalidSignature
	}
	actual, err := base64.RawURLEncoding.DecodeString(parts[2])
	expected := signer.mac(
		strconv.Itoa(int(purpose)) + "." + parts[0] + "." + parts[1])
	if err != nil || !hmac.Equal(actual, expected) {
		return ErrInvalidSignature
	}
	return nil
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
	nonce string,
) (string, error) {
	signed, _, err := signer.signState(CallbackState{
		Purpose: purpose,
		Nonce:   nonce,
	})
	return signed, err
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
