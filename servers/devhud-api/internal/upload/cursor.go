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
	Actor      string  `json:"a,omitempty"`
	States     []int16 `json:"s,omitempty"`
	Submission string  `json:"u,omitempty"`
	Group      string  `json:"g,omitempty"`
	CreatedAt  int64   `json:"c"`
	UploadID   string  `json:"i"`
	ExpiresAt  int64   `json:"e"`
}

func (c *CursorCodec) EncodeAdministrator(actor string, filters domain.AdminUploadFilters, cursor domain.UploadCursor, now time.Time) (string, error) {
	return c.encode(cursorEnvelope{
		Owner: filters.OwnerUserID, Actor: actor, States: stateValues(filters.States),
		Submission: filters.SubmissionID, Group: filters.UploadGroupID,
		CreatedAt: cursor.CreatedAt.UnixNano(), UploadID: cursor.UploadID, ExpiresAt: now.Add(cursorLifetime).Unix(),
	})
}

func (c *CursorCodec) DecodeAdministrator(token, actor string, filters domain.AdminUploadFilters, now time.Time) (domain.UploadCursor, error) {
	envelope, err := c.decode(token, now)
	if err != nil {
		return domain.UploadCursor{}, err
	}
	if envelope.Actor != actor || envelope.Owner != filters.OwnerUserID || envelope.Submission != filters.SubmissionID ||
		envelope.Group != filters.UploadGroupID || !sameStates(envelope.States, filters.States) {
		return domain.UploadCursor{}, errors.New("page token scope mismatch")
	}
	return domain.UploadCursor{CreatedAt: time.Unix(0, envelope.CreatedAt).UTC(), UploadID: envelope.UploadID}, nil
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
	return c.encode(cursorEnvelope{Owner: owner, States: stateValues(states), Submission: submission, CreatedAt: cursor.CreatedAt.UnixNano(), UploadID: cursor.UploadID, ExpiresAt: now.Add(cursorLifetime).Unix()})
}

func (c *CursorCodec) encode(envelope cursorEnvelope) (string, error) {
	body, err := json.Marshal(envelope)
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
	envelope, err := c.decode(token, now)
	if err != nil {
		return domain.UploadCursor{}, err
	}
	if envelope.Owner != owner || envelope.Actor != "" || envelope.Submission != submission || envelope.Group != "" || !sameStates(envelope.States, states) {
		return domain.UploadCursor{}, errors.New("page token scope mismatch")
	}
	return domain.UploadCursor{CreatedAt: time.Unix(0, envelope.CreatedAt).UTC(), UploadID: envelope.UploadID}, nil
}

func (c *CursorCodec) decode(token string, now time.Time) (cursorEnvelope, error) {
	if token == "" || len(token) > domain.UploadMaximumPageTokenBytes {
		return cursorEnvelope{}, errors.New("invalid page token")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(sealed) <= c.aead.NonceSize() {
		return cursorEnvelope{}, errors.New("invalid page token")
	}
	body, err := c.aead.Open(nil, sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():], nil)
	if err != nil {
		return cursorEnvelope{}, errors.New("invalid page token")
	}
	var envelope cursorEnvelope
	if json.Unmarshal(body, &envelope) != nil {
		return cursorEnvelope{}, errors.New("invalid page token")
	}
	if !now.Before(time.Unix(envelope.ExpiresAt, 0)) {
		return cursorEnvelope{}, errors.New("page token expired")
	}
	return envelope, nil
}

func stateValues(states []domain.UploadState) []int16 {
	values := make([]int16, len(states))
	for index, state := range states {
		values[index] = int16(state)
	}
	return values
}

func sameStates(values []int16, states []domain.UploadState) bool {
	if len(values) != len(states) {
		return false
	}
	for index, state := range states {
		if values[index] != int16(state) {
			return false
		}
	}
	return true
}
