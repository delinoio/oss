package github

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
)

type EncryptedCredential struct {
	Ciphertext     []byte
	WrappedDataKey []byte
	KeyID          string
}

type CredentialVault interface {
	Seal(plaintext []byte, associatedData []byte) (EncryptedCredential, error)
	Open(credential EncryptedCredential, associatedData []byte) ([]byte, error)
}

// AESCredentialVault is a fixture/development implementation of the same
// per-record data-key boundary expected from a managed production key wrapper.
// Production key material is externally injected; it is never persisted with
// the resulting credential.
type AESCredentialVault struct {
	keyID string
	key   []byte
}

func NewAESCredentialVault(keyID string, key []byte) (*AESCredentialVault, error) {
	if keyID == "" || len(keyID) > 128 || len(key) != 32 {
		return nil, errors.New("realqa github: credential wrapping key is invalid")
	}
	return &AESCredentialVault{keyID: keyID, key: append([]byte(nil), key...)}, nil
}

func (vault *AESCredentialVault) Seal(
	plaintext []byte,
	associatedData []byte,
) (EncryptedCredential, error) {
	if vault == nil || len(plaintext) == 0 || len(plaintext) > 16*1024 {
		return EncryptedCredential{}, errors.New("realqa github: credential cannot be encrypted")
	}
	dataKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dataKey); err != nil {
		return EncryptedCredential{}, errors.New("realqa github: credential encryption failed")
	}
	defer clear(dataKey)
	ciphertext, err := sealAES(dataKey, plaintext, associatedData)
	if err != nil {
		return EncryptedCredential{}, err
	}
	wrapped, err := sealAES(vault.key, dataKey, []byte(vault.keyID))
	if err != nil {
		return EncryptedCredential{}, err
	}
	return EncryptedCredential{
		Ciphertext: ciphertext, WrappedDataKey: wrapped, KeyID: vault.keyID,
	}, nil
}

func (vault *AESCredentialVault) Open(
	credential EncryptedCredential,
	associatedData []byte,
) ([]byte, error) {
	if vault == nil || credential.KeyID != vault.keyID ||
		len(credential.Ciphertext) == 0 || len(credential.WrappedDataKey) == 0 {
		return nil, errors.New("realqa github: credential key is unavailable")
	}
	dataKey, err := openAES(vault.key, credential.WrappedDataKey, []byte(vault.keyID))
	if err != nil {
		return nil, errors.New("realqa github: credential unwrap failed")
	}
	defer clear(dataKey)
	plaintext, err := openAES(dataKey, credential.Ciphertext, associatedData)
	if err != nil {
		return nil, errors.New("realqa github: credential decrypt failed")
	}
	return plaintext, nil
}

func sealAES(key, plaintext, associatedData []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("realqa github: encryption key is invalid")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("realqa github: encryption setup failed")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, errors.New("realqa github: encryption nonce failed")
	}
	return aead.Seal(nonce, nonce, plaintext, associatedData), nil
}

func openAES(key, value, associatedData []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(value) < aead.NonceSize() {
		return nil, errors.New("invalid ciphertext")
	}
	return aead.Open(nil, value[:aead.NonceSize()], value[aead.NonceSize():], associatedData)
}
