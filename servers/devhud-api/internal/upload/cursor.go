package upload

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

const cursorLifetime = 24 * time.Hour

type CursorCodec struct{ aead cipher.AEAD }

type cursorEnvelope struct {
	Owner      string  `json:"o"`
	States     []int16 `json:"s,omitempty"`
	Submission string  `json:"u,omitempty"`
	CreatedAt  int64   `json:"c"`
	UploadID   string  `json:"i"`
	ExpiresAt  int64   `json:"e"`
}

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	digest := sha256.Sum256(append([]byte("devhud-upload-cursor-v1\x00"), key...))
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CursorCodec{aead: aead}, nil
}

func (c *CursorCodec) Encode(owner string, states []domain.UploadState, submission string, cursor domain.UploadCursor, now time.Time) (string, error) {
	stateValues := make([]int16, len(states))
	for index, state := range states {
		stateValues[index] = int16(state)
	}
	body, err := json.Marshal(cursorEnvelope{Owner: owner, States: stateValues, Submission: submission, CreatedAt: cursor.CreatedAt.UnixNano(), UploadID: cursor.UploadID, ExpiresAt: now.Add(cursorLifetime).Unix()})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, body, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *CursorCodec) Decode(token, owner string, states []domain.UploadState, submission string, now time.Time) (domain.UploadCursor, error) {
	if token == "" || len(token) > domain.UploadMaximumPageTokenBytes {
		return domain.UploadCursor{}, errors.New("invalid page token")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(sealed) <= c.aead.NonceSize() {
		return domain.UploadCursor{}, errors.New("invalid page token")
	}
	body, err := c.aead.Open(nil, sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():], nil)
	if err != nil {
		return domain.UploadCursor{}, errors.New("invalid page token")
	}
	var envelope cursorEnvelope
	if json.Unmarshal(body, &envelope) != nil || envelope.Owner != owner || envelope.Submission != submission || len(envelope.States) != len(states) {
		return domain.UploadCursor{}, errors.New("page token scope mismatch")
	}
	for index, state := range states {
		if envelope.States[index] != int16(state) {
			return domain.UploadCursor{}, errors.New("page token scope mismatch")
		}
	}
	if !now.Before(time.Unix(envelope.ExpiresAt, 0)) {
		return domain.UploadCursor{}, errors.New("page token expired")
	}
	return domain.UploadCursor{CreatedAt: time.Unix(0, envelope.CreatedAt).UTC(), UploadID: envelope.UploadID}, nil
}
