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
	activeKeyID  string
	wrappingAEAD map[string]cipher.AEAD
	keyOrder     []string
}

func NewCipher(key []byte) (*Cipher, error) {
	return NewVersionedCipher("v1", map[string][]byte{"v1": key})
}

func NewVersionedCipher(
	activeKeyID string,
	keys map[string][]byte,
) (*Cipher, error) {
	if !validKeyID(activeKeyID) || len(keys) == 0 {
		return nil, errors.New("security: invalid encryption key configuration")
	}
	result := &Cipher{
		activeKeyID:  activeKeyID,
		wrappingAEAD: make(map[string]cipher.AEAD, len(keys)),
	}
	for keyID, key := range keys {
		if !validKeyID(keyID) || len(key) != 32 {
			return nil, errors.New("security: invalid encryption key configuration")
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, errors.New("security: cipher initialization failed")
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, errors.New("security: authenticated cipher initialization failed")
		}
		result.wrappingAEAD[keyID] = aead
	}
	if _, ok := result.wrappingAEAD[activeKeyID]; !ok {
		return nil, errors.New("security: active encryption key is unavailable")
	}
	result.keyOrder = append(result.keyOrder, activeKeyID)
	for keyID := range result.wrappingAEAD {
		if keyID != activeKeyID {
			result.keyOrder = append(result.keyOrder, keyID)
		}
	}
	return result, nil
}

func validKeyID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func (c *Cipher) ActiveKeyID() string {
	if c == nil {
		return ""
	}
	return c.activeKeyID
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
	dataNonce := make([]byte, dataAEAD.NonceSize())
	if _, err := io.ReadFull(rand.Reader, dataNonce); err != nil {
		return nil, errors.New("security: nonce generation failed")
	}
	payload := dataAEAD.Seal(nil, dataNonce, plaintext, []byte(label))
	return c.marshalEnvelope(
		label, c.activeKeyID, dataKey, dataNonce, payload)
}

func (c *Cipher) Open(label string, ciphertext []byte) ([]byte, error) {
	envelope, err := c.openEnvelope(label, ciphertext)
	if err != nil {
		return nil, err
	}
	dataBlock, err := aes.NewCipher(envelope.dataKey)
	if err != nil {
		return nil, errors.New("security: ciphertext authentication failed")
	}
	dataAEAD, err := cipher.NewGCM(dataBlock)
	if err != nil || len(envelope.dataNonce) != dataAEAD.NonceSize() {
		return nil, errors.New("security: ciphertext authentication failed")
	}
	plaintext, err := dataAEAD.Open(
		nil, envelope.dataNonce, envelope.payload, []byte(label))
	if err != nil {
		return nil, errors.New("security: ciphertext authentication failed")
	}
	return plaintext, nil
}

func (c *Cipher) KeyID(ciphertext []byte) (string, error) {
	if len(ciphertext) < 1 {
		return "", errors.New("security: invalid ciphertext")
	}
	if ciphertext[0] == 1 {
		return "", nil
	}
	if ciphertext[0] != 2 || len(ciphertext) < 2 {
		return "", errors.New("security: invalid ciphertext")
	}
	keyIDLength := int(ciphertext[1])
	if keyIDLength == 0 || len(ciphertext) < 2+keyIDLength {
		return "", errors.New("security: invalid ciphertext")
	}
	keyID := string(ciphertext[2 : 2+keyIDLength])
	if !validKeyID(keyID) {
		return "", errors.New("security: invalid ciphertext")
	}
	return keyID, nil
}

func (c *Cipher) Rewrap(
	label string,
	ciphertext []byte,
) ([]byte, bool, error) {
	envelope, err := c.openEnvelope(label, ciphertext)
	if err != nil {
		return nil, false, err
	}
	if envelope.version == 2 && envelope.keyID == c.activeKeyID {
		return append([]byte(nil), ciphertext...), false, nil
	}
	rewrapped, err := c.marshalEnvelope(
		label, c.activeKeyID, envelope.dataKey,
		envelope.dataNonce, envelope.payload)
	return rewrapped, err == nil, err
}

type envelope struct {
	version   byte
	keyID     string
	dataKey   []byte
	dataNonce []byte
	payload   []byte
}

func (c *Cipher) openEnvelope(
	label string,
	ciphertext []byte,
) (envelope, error) {
	if c == nil || c.wrappingAEAD == nil || label == "" || len(ciphertext) < 1 {
		return envelope{}, errors.New("security: invalid ciphertext")
	}
	switch ciphertext[0] {
	case 1:
		for _, keyID := range c.keyOrder {
			decoded, err := openEnvelopeWithAEAD(
				label, ciphertext, 1, keyID, c.wrappingAEAD[keyID])
			if err == nil {
				return decoded, nil
			}
		}
	case 2:
		keyID, err := c.KeyID(ciphertext)
		if err != nil {
			return envelope{}, err
		}
		wrappingAEAD, ok := c.wrappingAEAD[keyID]
		if !ok {
			return envelope{}, errors.New("security: encryption key is unavailable")
		}
		return openEnvelopeWithAEAD(
			label, ciphertext, 2+len(keyID), keyID, wrappingAEAD)
	}
	return envelope{}, errors.New("security: ciphertext authentication failed")
}

func openEnvelopeWithAEAD(
	label string,
	ciphertext []byte,
	offset int,
	keyID string,
	wrappingAEAD cipher.AEAD,
) (envelope, error) {
	wrappingNonceEnd := offset + wrappingAEAD.NonceSize()
	wrappedKeyEnd := wrappingNonceEnd + 32 + wrappingAEAD.Overhead()
	dataNonceEnd := wrappedKeyEnd + 12
	if len(ciphertext) < dataNonceEnd+16 {
		return envelope{}, errors.New("security: invalid ciphertext")
	}
	dataKey, err := wrappingAEAD.Open(nil,
		ciphertext[offset:wrappingNonceEnd],
		ciphertext[wrappingNonceEnd:wrappedKeyEnd],
		[]byte("deck-envelope:"+label))
	if err != nil {
		return envelope{}, errors.New("security: ciphertext authentication failed")
	}
	return envelope{
		version: ciphertext[0], keyID: keyID, dataKey: dataKey,
		dataNonce: append([]byte(nil), ciphertext[wrappedKeyEnd:dataNonceEnd]...),
		payload:   append([]byte(nil), ciphertext[dataNonceEnd:]...),
	}, nil
}

func (c *Cipher) marshalEnvelope(
	label string,
	keyID string,
	dataKey []byte,
	dataNonce []byte,
	payload []byte,
) ([]byte, error) {
	wrappingAEAD, ok := c.wrappingAEAD[keyID]
	if !ok || len(dataKey) != 32 || len(dataNonce) != 12 {
		return nil, errors.New("security: cipher is unavailable")
	}
	wrappingNonce := make([]byte, wrappingAEAD.NonceSize())
	if _, err := io.ReadFull(rand.Reader, wrappingNonce); err != nil {
		return nil, errors.New("security: nonce generation failed")
	}
	wrappedKey := wrappingAEAD.Seal(nil, wrappingNonce, dataKey,
		[]byte("deck-envelope:"+label))
	output := make([]byte, 0, 2+len(keyID)+len(wrappingNonce)+
		len(wrappedKey)+len(dataNonce)+len(payload))
	output = append(output, 2, byte(len(keyID)))
	output = append(output, keyID...)
	output = append(output, wrappingNonce...)
	output = append(output, wrappedKey...)
	output = append(output, dataNonce...)
	output = append(output, payload...)
	return output, nil
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
