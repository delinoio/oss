// Package security provides Deck's application-layer ciphertext, pseudonym,
// ETag, cursor, and request-digest primitives.
package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"

	"github.com/google/uuid"
)

type Cipher struct {
	wrappingAEAD cipher.AEAD
}

func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("security: encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("security: cipher initialization failed")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("security: authenticated cipher initialization failed")
	}
	return &Cipher{wrappingAEAD: aead}, nil
}

func (c *Cipher) Seal(label string, plaintext []byte) ([]byte, error) {
	if c == nil || c.wrappingAEAD == nil || label == "" {
		return nil, errors.New("security: cipher is unavailable")
	}
	dataKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dataKey); err != nil {
		return nil, errors.New("security: data key generation failed")
	}
	dataBlock, err := aes.NewCipher(dataKey)
	if err != nil {
		return nil, errors.New("security: data cipher initialization failed")
	}
	dataAEAD, err := cipher.NewGCM(dataBlock)
	if err != nil {
		return nil, errors.New("security: data cipher initialization failed")
	}
	wrappingNonce := make([]byte, c.wrappingAEAD.NonceSize())
	dataNonce := make([]byte, dataAEAD.NonceSize())
	if _, err := io.ReadFull(rand.Reader, wrappingNonce); err != nil {
		return nil, errors.New("security: nonce generation failed")
	}
	if _, err := io.ReadFull(rand.Reader, dataNonce); err != nil {
		return nil, errors.New("security: nonce generation failed")
	}
	wrappedKey := c.wrappingAEAD.Seal(nil, wrappingNonce, dataKey,
		[]byte("deck-envelope:"+label))
	output := make([]byte, 1, 1+len(wrappingNonce)+len(wrappedKey)+
		len(dataNonce)+len(plaintext)+dataAEAD.Overhead())
	output[0] = 1
	output = append(output, wrappingNonce...)
	output = append(output, wrappedKey...)
	output = append(output, dataNonce...)
	output = dataAEAD.Seal(output, dataNonce, plaintext, []byte(label))
	return output, nil
}

func (c *Cipher) Open(label string, ciphertext []byte) ([]byte, error) {
	if c == nil || c.wrappingAEAD == nil || label == "" || len(ciphertext) < 1 ||
		ciphertext[0] != 1 {
		return nil, errors.New("security: invalid ciphertext")
	}
	wrappingNonceEnd := 1 + c.wrappingAEAD.NonceSize()
	wrappedKeyEnd := wrappingNonceEnd + 32 + c.wrappingAEAD.Overhead()
	dataNonceEnd := wrappedKeyEnd + 12
	if len(ciphertext) < dataNonceEnd+16 {
		return nil, errors.New("security: invalid ciphertext")
	}
	dataKey, err := c.wrappingAEAD.Open(nil,
		ciphertext[1:wrappingNonceEnd],
		ciphertext[wrappingNonceEnd:wrappedKeyEnd],
		[]byte("deck-envelope:"+label))
	if err != nil {
		return nil, errors.New("security: ciphertext authentication failed")
	}
	dataBlock, err := aes.NewCipher(dataKey)
	if err != nil {
		return nil, errors.New("security: ciphertext authentication failed")
	}
	dataAEAD, err := cipher.NewGCM(dataBlock)
	if err != nil || dataAEAD.NonceSize() != dataNonceEnd-wrappedKeyEnd {
		return nil, errors.New("security: ciphertext authentication failed")
	}
	plaintext, err := dataAEAD.Open(nil,
		ciphertext[wrappedKeyEnd:dataNonceEnd],
		ciphertext[dataNonceEnd:],
		[]byte(label))
	if err != nil {
		return nil, errors.New("security: ciphertext authentication failed")
	}
	return plaintext, nil
}

type Hasher struct {
	key []byte
}

func NewHasher(key []byte) (*Hasher, error) {
	if len(key) < 32 {
		return nil, errors.New("security: hashing key must contain at least 32 bytes")
	}
	return &Hasher{key: append([]byte(nil), key...)}, nil
}

func (h *Hasher) Sum(label, value string) [sha256.Size]byte {
	hash := hmac.New(sha256.New, h.key)
	_, _ = hash.Write([]byte(label))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(value))
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func Digest(value []byte) [sha256.Size]byte { return sha256.Sum256(value) }

func GrantVerifier(grant string) [sha256.Size]byte { return sha256.Sum256([]byte(grant)) }

func NewGrant() (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", errors.New("security: grant generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (h *Hasher) ETag(id uuid.UUID, revision uint64) string {
	var encoded [24]byte
	copy(encoded[:16], id[:])
	binary.BigEndian.PutUint64(encoded[16:], revision)
	sum := h.Sum("etag", string(encoded[:]))
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:18]) + `"`
}

func (h *Hasher) EncodeCursor(kind string, payload []byte) string {
	sum := h.Sum("cursor:"+kind, string(payload))
	value := append(append([]byte(nil), payload...), sum[:16]...)
	return base64.RawURLEncoding.EncodeToString(value)
}

func (h *Hasher) DecodeCursor(kind, cursor string, payloadSize int) ([]byte, error) {
	value, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(value) != payloadSize+16 {
		return nil, errors.New("security: invalid cursor")
	}
	payload := value[:payloadSize]
	sum := h.Sum("cursor:"+kind, string(payload))
	if !hmac.Equal(value[payloadSize:], sum[:16]) {
		return nil, errors.New("security: invalid cursor")
	}
	return append([]byte(nil), payload...), nil
}
