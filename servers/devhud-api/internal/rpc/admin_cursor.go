package rpc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

const adminCursorLifetime = 24 * time.Hour

type adminCursorCodec struct{ aead cipher.AEAD }

type adminCursorEnvelope struct {
	Kind      string `json:"k"`
	Actor     string `json:"a"`
	Scope     string `json:"s"`
	CreatedAt int64  `json:"c"`
	ID        string `json:"i"`
	ExpiresAt int64  `json:"e"`
}

func newAdminCursorCodec(key []byte) (*adminCursorCodec, error) {
	if len(key) < 32 {
		return nil, errors.New("administrator cursor key must contain at least 32 bytes")
	}
	digest := sha256.Sum256(append([]byte("devhud-admin-cursor-v1\x00"), key...))
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &adminCursorCodec{aead: aead}, nil
}

func (c *adminCursorCodec) encode(kind, actor, scope, id string, createdAt, now time.Time) (string, error) {
	body, err := json.Marshal(adminCursorEnvelope{
		Kind: kind, Actor: actor, Scope: scope, ID: id, CreatedAt: createdAt.UnixNano(),
		ExpiresAt: now.Add(adminCursorLifetime).Unix(),
	})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(c.aead.Seal(nonce, nonce, body, nil)), nil
}

func (c *adminCursorCodec) decode(token, kind, actor, scope string, now time.Time) (time.Time, string, error) {
	if token == "" || len(token) > domain.AdminMaximumTokenSize {
		return time.Time{}, "", errors.New("invalid page token")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(sealed) <= c.aead.NonceSize() {
		return time.Time{}, "", errors.New("invalid page token")
	}
	body, err := c.aead.Open(nil, sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():], nil)
	if err != nil {
		return time.Time{}, "", errors.New("invalid page token")
	}
	var envelope adminCursorEnvelope
	if json.Unmarshal(body, &envelope) != nil {
		return time.Time{}, "", errors.New("invalid page token")
	}
	if envelope.Kind != kind || envelope.Actor != actor || envelope.Scope != scope {
		return time.Time{}, "", errors.New("page token scope mismatch")
	}
	if !now.Before(time.Unix(envelope.ExpiresAt, 0)) {
		return time.Time{}, "", errors.New("page token expired")
	}
	return time.Unix(0, envelope.CreatedAt).UTC(), envelope.ID, nil
}
