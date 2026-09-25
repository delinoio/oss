package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Identity is private local bootstrap material, never an RPC/CLI response.
// Paired devices receive separate revocable credentials, not this owner token.
type Identity struct {
	ServerID domain.ID `json:"server_id"`
	Token    string    `json:"token"`
}

func LoadIdentity(root string) (Identity, error) {
	raw, err := ReadPrivate(filepath.Join(root, "owner.json"), 4096)
	if err != nil {
		return Identity{}, err
	}
	var identity Identity
	if err := domain.Decode(raw, &identity); err != nil {
		return Identity{}, err
	}
	if err := identity.ServerID.Validate(); err != nil {
		return Identity{}, err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(identity.Token)
	if err != nil || len(decoded) != 32 {
		return Identity{}, domain.Fail(domain.RecoveryRequired, "The owner credential is invalid.", "Restore the private owner credential; do not reset existing server state.")
	}
	return identity, nil
}
func CreateIdentity(root string) (Identity, error) {
	identity, err := LoadIdentity(root)
	if err == nil {
		return identity, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Identity{}, err
	}
	token, err := RandomToken()
	if err != nil {
		return Identity{}, err
	}
	identity = Identity{ServerID: domain.NewID(), Token: token}
	raw, err := json.Marshal(identity)
	if err != nil {
		return Identity{}, err
	}
	if err := WriteAtomic(filepath.Join(root, "owner.json"), raw); err != nil {
		return Identity{}, err
	}
	return identity, nil
}
func RandomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func EqualToken(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

type Cursor struct {
	Version  int       `json:"v"`
	ServerID domain.ID `json:"s"`
	Scope    string    `json:"k"`
	After    domain.ID `json:"a,omitempty"`
	Sequence uint64    `json:"q,omitempty"`
	Expires  int64     `json:"e"`
}

func (i Identity) EncodeCursor(cursor Cursor) (string, error) {
	cursor.Version = 1
	cursor.ServerID = i.ServerID
	cursor.Expires = time.Now().Add(24 * time.Hour).Unix()
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(i.Token))
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (i Identity) DecodeCursor(token, scope string) (Cursor, error) {
	invalid := func() (Cursor, error) {
		return Cursor{}, domain.Fail(domain.CursorExpired, "The cursor is invalid, expired, or belongs to another scope.", "Fetch a new snapshot or restart pagination.")
	}
	if len(token) > 2048 {
		return invalid()
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return invalid()
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return invalid()
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return invalid()
	}
	mac := hmac.New(sha256.New, []byte(i.Token))
	mac.Write(raw)
	if !hmac.Equal(mac.Sum(nil), signature) {
		return invalid()
	}
	var cursor Cursor
	if err := domain.Decode(raw, &cursor); err != nil {
		return invalid()
	}
	if cursor.Version != 1 || cursor.ServerID != i.ServerID || cursor.Scope != scope || cursor.Expires < time.Now().Unix() {
		return invalid()
	}
	return cursor, nil
}
