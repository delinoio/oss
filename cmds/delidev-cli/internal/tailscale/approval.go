// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type Role string

const (
	ClientRole Role = "client"
	WorkerRole Role = "worker"
)

type ApprovalState string

const (
	Pending   ApprovalState = "pending"
	Approved  ApprovalState = "approved"
	Denied    ApprovalState = "denied"
	Expired   ApprovalState = "expired"
	Completed ApprovalState = "completed"
	Uncertain ApprovalState = "uncertain"
)

type Approval struct {
	ID             domain.ID     `json:"id"`
	ServerID       domain.ID     `json:"server_id"`
	Origin         string        `json:"origin"`
	Role           Role          `json:"role"`
	Name           string        `json:"name"`
	RequesterKey   []byte        `json:"requester_key"`
	TargetKey      []byte        `json:"target_key"`
	SelectedServer domain.ID     `json:"selected_server,omitempty"`
	SelectedOrigin string        `json:"selected_origin,omitempty"`
	ExpiresAt      time.Time     `json:"expires_at"`
	Code           string        `json:"code"`
	State          ApprovalState `json:"state"`
	EncryptedGrant []byte        `json:"encrypted_grant,omitempty"`
	ProfileID      domain.ID     `json:"profile_id,omitempty"`
	MachineID      domain.ID     `json:"machine_id,omitempty"`
}

func (a Approval) Validate() error {
	if a.ID.Validate() != nil || a.ServerID.Validate() != nil || ValidateOrigin(a.Origin) != nil || domain.Text(a.Name, "requester name", 256, true) != nil || len(a.RequesterKey) != 32 || len(a.TargetKey) != 32 || a.ExpiresAt.IsZero() || a.Code != a.Confirmation() {
		return fail(domain.InvalidArgument)
	}
	switch a.State {
	case Pending, Approved, Denied, Expired, Completed, Uncertain:
	default:
		return fail(domain.InvalidArgument)
	}
	switch a.Role {
	case "client":
		if a.SelectedServer != "" || a.SelectedOrigin != "" {
			return fail(domain.InvalidArgument)
		}
	case "worker":
		if a.SelectedServer.Validate() != nil || ValidateOrigin(a.SelectedOrigin) != nil {
			return fail(domain.InvalidArgument)
		}
	default:
		return fail(domain.InvalidArgument)
	}
	if a.ProfileID != "" && a.ProfileID.Validate() != nil || a.MachineID != "" && a.MachineID.Validate() != nil {
		return fail(domain.InvalidArgument)
	}
	return nil
}
func MatchesKey(private, public []byte) bool {
	key, err := ecdh.X25519().NewPrivateKey(private)
	return err == nil && bytes.Equal(key.PublicKey().Bytes(), public)
}

func (a Approval) Transcript() []byte {
	raw, _ := json.Marshal(struct {
		Version           int
		ID, Server        domain.ID
		Origin, Name      string
		Role              Role
		Requester, Target []byte
		Selected          domain.ID
		SelectedOrigin    string
		Expires           time.Time
	}{1, a.ID, a.ServerID, a.Origin, a.Name, a.Role, a.RequesterKey, a.TargetKey, a.SelectedServer, a.SelectedOrigin, a.ExpiresAt})
	return raw
}
func (a Approval) Confirmation() string {
	sum := sha256.Sum256(append([]byte("delidev-tailscale-confirmation-v1\x00"), a.Transcript()...))
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(sum[:4])%1000000)
}
func Encrypt(private, public, transcript, plain []byte) ([]byte, error) {
	key, err := derive(private, public, transcript)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, transcript), nil
}
func Decrypt(private, public, transcript, encrypted []byte) ([]byte, error) {
	if len(encrypted) > 32<<10 {
		return nil, fail(domain.ResourceExhausted)
	}
	key, err := derive(private, public, transcript)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(encrypted) < gcm.NonceSize() {
		return nil, fail(domain.InvalidArgument)
	}
	plain, err := gcm.Open(nil, encrypted[:gcm.NonceSize()], encrypted[gcm.NonceSize():], transcript)
	if err != nil {
		return nil, fail(domain.PermissionDenied)
	}
	return plain, nil
}
func derive(private, public, transcript []byte) ([]byte, error) {
	secret, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil {
		return nil, fail(domain.InvalidArgument)
	}
	peer, err := ecdh.X25519().NewPublicKey(public)
	if err != nil {
		return nil, fail(domain.InvalidArgument)
	}
	shared, err := secret.ECDH(peer)
	if err != nil {
		return nil, fail(domain.PermissionDenied)
	}
	defer clear(shared)
	sum := sha256.New()
	sum.Write([]byte("delidev-tailscale-grant-v1\x00"))
	sum.Write(shared)
	sum.Write(transcript)
	return sum.Sum(nil), nil
}
func NewKey() ([]byte, []byte, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return key.Bytes(), key.PublicKey().Bytes(), nil
}
