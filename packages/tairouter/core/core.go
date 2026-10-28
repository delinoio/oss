// SPDX-License-Identifier: Apache-2.0
package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"time"
)

type Role string

const (
	Owner         Role = "owner"
	Administrator Role = "administrator"
	Member        Role = "member"
)

type Identity struct {
	Issuer  string
	Subject string
}
type Scope struct {
	OrganizationID string
	UserID         string
	Generation     int64
}
type StateKind string

const (
	Responses StateKind = "responses"
	Reasoning StateKind = "reasoning"
	Search    StateKind = "search"
)

func (k StateKind) Limits() (time.Duration, int, int64, error) {
	switch k {
	case Responses:
		return 24 * time.Hour, 1000, 1 << 30, nil
	case Reasoning:
		return time.Hour, 64, 256 << 10, nil
	case Search:
		return time.Hour, 64, 512 << 10, nil
	}
	return 0, 0, 0, errors.New("invalid state kind")
}
func ID() string { return uuid.Must(uuid.NewV7()).String() }
func Token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Hash(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

// Envelope authenticates both the key version and the caller's ownership context.
// Keys must be injected separately from the database and retained for old backups.
type Envelope struct {
	active string
	keys   map[string]cipher.AEAD
}
type envelopeValue struct {
	Version string `json:"v"`
	Nonce   []byte `json:"n"`
	Data    []byte `json:"d"`
}

func NewEnvelope(active string, keys map[string][]byte) (*Envelope, error) {
	e := &Envelope{active: active, keys: map[string]cipher.AEAD{}}
	for v, k := range keys {
		if v == "" || len(k) != 32 {
			return nil, errors.New("envelope requires named 256-bit keys")
		}
		b, err := aes.NewCipher(k)
		if err != nil {
			return nil, err
		}
		a, err := cipher.NewGCM(b)
		if err != nil {
			return nil, err
		}
		e.keys[v] = a
	}
	if e.keys[active] == nil {
		return nil, errors.New("active encryption key missing")
	}
	return e, nil
}
func (e *Envelope) Encrypt(plain []byte, context string) ([]byte, error) {
	a := e.keys[e.active]
	n := make([]byte, a.NonceSize())
	if _, err := rand.Read(n); err != nil {
		return nil, err
	}
	return json.Marshal(envelopeValue{e.active, n, a.Seal(nil, n, plain, []byte(e.active+"\x00"+context))})
}
func (e *Envelope) Decrypt(data []byte, context string) ([]byte, error) {
	var v envelopeValue
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, errors.New("invalid encrypted envelope")
	}
	a := e.keys[v.Version]
	if a == nil || len(v.Nonce) != a.NonceSize() {
		return nil, errors.New("unknown key or invalid envelope")
	}
	p, err := a.Open(nil, v.Nonce, v.Data, []byte(v.Version+"\x00"+context))
	if err != nil {
		return nil, fmt.Errorf("envelope authentication failed: %w", err)
	}
	return p, nil
}
