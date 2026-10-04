// SPDX-License-Identifier: Apache-2.0
// Package workernetwork owns recipient-only transfer and protected derivative
// caches. Encryption is not sender authentication: every import independently
// requires the digest obtained from the authenticated issuing server.
package workernetwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"time"

	"filippo.io/age"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
)

const MaxPlaintext = 48 << 10
const MaxCiphertext = 96 << 10

// Transfer spans independently clocked hosts. This allowance never extends expiry.
const maxTransferClockSkew = 30 * time.Second

// Authority is selected independently of the file being imported. Pending
// pairing and an existing paired device retain the same exact scope.
type Authority struct {
	ServerID  domain.ID `json:"server_id"`
	Endpoint  string    `json:"endpoint"`
	MachineID domain.ID `json:"machine_id"`
	DeviceID  domain.ID `json:"device_id"`
	PairingID domain.ID `json:"pairing_id"`
}

func (a Authority) Validate() error {
	for _, id := range []domain.ID{a.ServerID, a.MachineID, a.DeviceID, a.PairingID} {
		if id.Validate() != nil {
			return invalid()
		}
	}
	if rpc.ValidateEndpoint(a.Endpoint) != nil {
		return invalid()
	}
	u, err := url.Parse(a.Endpoint)
	if err != nil || !domain.ProxyHost(u.Hostname()) || u.String() != a.Endpoint {
		return invalid()
	}
	if port := u.Port(); port != "" {
		n, e := strconv.ParseUint(port, 10, 16)
		if e != nil || n == 0 || strconv.FormatUint(n, 10) != port {
			return invalid()
		}
	}
	return nil
}

type Bundle struct {
	Version    uint32                  `json:"version"`
	Authority  Authority               `json:"authority"`
	KeyID      domain.ID               `json:"key_id"`
	Recipient  string                  `json:"recipient"`
	ExportID   domain.ID               `json:"export_id"`
	RouteID    domain.ID               `json:"route_id"`
	Generation uint64                  `json:"generation"`
	Route      domain.NetworkRoute     `json:"route"`
	Credential *domain.ProxyCredential `json:"credential,omitempty"`
	IssuedAt   time.Time               `json:"issued_at"`
	ExpiresAt  time.Time               `json:"expires_at"`
}

func invalid() *domain.Error {
	return domain.Fail(domain.InvalidArgument, "The Worker network bundle is invalid.", "Use the original X25519 recipient, selected server/pairing authority and separately obtained ciphertext digest.")
}
func recovery() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The protected Worker network cache cannot be verified.", "Preserve its original key and scope; import a current authenticated server export without direct or older-profile fallback.")
}
func (b Bundle) validate(now time.Time, fresh bool) error {
	if b.Version != 1 || b.Authority.Validate() != nil || b.KeyID.Validate() != nil || b.ExportID.Validate() != nil || b.RouteID.Validate() != nil || b.Generation == 0 || b.Generation >= 1<<63 || b.Route.MachineID != b.Authority.MachineID || b.Route.Validate() != nil {
		return invalid()
	}
	if pin := b.Route.Binding; pin != nil && (pin.DeviceID != b.Authority.DeviceID || pin.PairingID != b.Authority.PairingID || pin.KeyID != b.KeyID || pin.Recipient != b.Recipient || pin.Endpoint != b.Authority.Endpoint) {
		return invalid()
	}
	r, err := age.ParseX25519Recipient(b.Recipient)
	if err != nil || r.String() != b.Recipient {
		return invalid()
	}
	if b.IssuedAt.IsZero() || !b.ExpiresAt.After(b.IssuedAt) || b.ExpiresAt.Sub(b.IssuedAt) > 5*time.Minute || b.IssuedAt.After(now.Add(maxTransferClockSkew)) || fresh && !b.ExpiresAt.After(now) {
		return invalid()
	}
	if (b.Credential != nil) != (b.Route.Profile.CredentialGeneration != "") || b.Credential != nil && b.Credential.Validate() != nil {
		return invalid()
	}
	return nil
}
func Digest(ciphertext []byte) string {
	v := sha256.Sum256(ciphertext)
	return hex.EncodeToString(v[:])
}
func Encrypt(ctx context.Context, bundle Bundle) ([]byte, string, error) {
	if ctx.Err() != nil {
		return nil, "", domain.SafeError(ctx.Err())
	}
	if bundle.validate(time.Now().UTC(), true) != nil {
		return nil, "", invalid()
	}
	raw, err := json.Marshal(bundle)
	defer clear(raw)
	if err != nil || len(raw) > MaxPlaintext {
		return nil, "", invalid()
	}
	recipient, err := age.ParseX25519Recipient(bundle.Recipient)
	if err != nil {
		return nil, "", invalid()
	}
	var output bytes.Buffer
	writer, err := age.Encrypt(&output, recipient)
	if err != nil {
		return nil, "", invalid()
	}
	if _, err = writer.Write(raw); err == nil {
		err = writer.Close()
	}
	if err != nil || output.Len() > MaxCiphertext {
		return nil, "", invalid()
	}
	if ctx.Err() != nil {
		return nil, "", domain.SafeError(ctx.Err())
	}
	ciphertext := output.Bytes()
	return ciphertext, Digest(ciphertext), nil
}
func decrypt(ctx context.Context, identity *age.X25519Identity, ciphertext []byte, expected string, authority Authority, keyID domain.ID) (Bundle, error) {
	var bundle Bundle
	if ctx.Err() != nil {
		return bundle, domain.SafeError(ctx.Err())
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != expected || len(ciphertext) == 0 || len(ciphertext) > MaxCiphertext || authority.Validate() != nil {
		return bundle, invalid()
	}
	actual := sha256.Sum256(ciphertext)
	if subtle.ConstantTimeCompare(decoded, actual[:]) != 1 {
		return bundle, invalid()
	}
	reader, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		return bundle, invalid()
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxPlaintext+1))
	defer clear(raw)
	if err != nil || len(raw) > MaxPlaintext || domain.Decode(raw, &bundle) != nil || bundle.validate(time.Now().UTC(), true) != nil || bundle.Authority != authority || bundle.KeyID != keyID || bundle.Recipient != identity.Recipient().String() {
		return Bundle{}, invalid()
	}
	if ctx.Err() != nil {
		return Bundle{}, domain.SafeError(ctx.Err())
	}
	return bundle, nil
}

// sameAuthority ignores transfer lifetime/nonce only. A new ciphertext cannot
// change an already published generation, including protected credentials.
func sameAuthority(a, b Bundle) bool {
	a.ExportID, a.IssuedAt, a.ExpiresAt = "", time.Time{}, time.Time{}
	b.ExportID, b.IssuedAt, b.ExpiresAt = "", time.Time{}, time.Time{}
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	defer clear(left)
	defer clear(right)
	return bytes.Equal(left, right)
}
