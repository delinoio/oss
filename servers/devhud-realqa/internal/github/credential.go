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
	Rewrap(credential EncryptedCredential) (EncryptedCredential, error)
	ActiveKeyID() string
}

// AESCredentialVault is a fixture/development implementation of the same
// per-record data-key boundary expected from a managed production key wrapper.
// Production key material is externally injected; it is never persisted with
// the resulting credential.
type AESCredentialVault struct {
	activeKeyID string
	keys        map[string][]byte
}

func NewAESCredentialVault(keyID string, key []byte) (*AESCredentialVault, error) {
	return NewAESCredentialVaultWithPreviousKeys(keyID, key, nil)
}

func NewAESCredentialVaultWithPreviousKeys(
	activeKeyID string,
	activeKey []byte,
	previousKeys map[string][]byte,
) (*AESCredentialVault, error) {
	if !validWrappingKey(activeKeyID, activeKey) || len(previousKeys) > 32 {
		return nil, errors.New("realqa github: credential wrapping key is invalid")
	}
	keys := make(map[string][]byte, len(previousKeys)+1)
	keys[activeKeyID] = append([]byte(nil), activeKey...)
	for keyID, key := range previousKeys {
		if keyID == activeKeyID || !validWrappingKey(keyID, key) {
			return nil, errors.New("realqa github: credential wrapping key is invalid")
		}
		keys[keyID] = append([]byte(nil), key...)
	}
	return &AESCredentialVault{activeKeyID: activeKeyID, keys: keys}, nil
}

func (vault *AESCredentialVault) ActiveKeyID() string {
	if vault == nil {
		return ""
	}
	return vault.activeKeyID
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
	activeKey := vault.keys[vault.activeKeyID]
	wrapped, err := sealAES(activeKey, dataKey, []byte(vault.activeKeyID))
	if err != nil {
		return EncryptedCredential{}, err
	}
	return EncryptedCredential{
		Ciphertext: ciphertext, WrappedDataKey: wrapped, KeyID: vault.activeKeyID,
	}, nil
}

func (vault *AESCredentialVault) Open(
	credential EncryptedCredential,
	associatedData []byte,
) ([]byte, error) {
	if vault == nil {
		return nil, errors.New("realqa github: credential key is unavailable")
	}
	key, available := vault.keys[credential.KeyID]
	if !available ||
		len(credential.Ciphertext) == 0 || len(credential.WrappedDataKey) == 0 {
		return nil, errors.New("realqa github: credential key is unavailable")
	}
	dataKey, err := openAES(key, credential.WrappedDataKey, []byte(credential.KeyID))
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

func (vault *AESCredentialVault) Rewrap(
	credential EncryptedCredential,
) (EncryptedCredential, error) {
	if vault == nil {
		return EncryptedCredential{}, errors.New(
			"realqa github: credential key is unavailable")
	}
	key, available := vault.keys[credential.KeyID]
	if !available ||
		len(credential.Ciphertext) == 0 || len(credential.WrappedDataKey) == 0 {
		return EncryptedCredential{}, errors.New(
			"realqa github: credential key is unavailable")
	}
	if credential.KeyID == vault.activeKeyID {
		return credential, nil
	}
	dataKey, err := openAES(key, credential.WrappedDataKey, []byte(credential.KeyID))
	if err != nil {
		return EncryptedCredential{}, errors.New(
			"realqa github: credential unwrap failed")
	}
	defer clear(dataKey)
	wrapped, err := sealAES(
		vault.keys[vault.activeKeyID], dataKey, []byte(vault.activeKeyID))
	if err != nil {
		return EncryptedCredential{}, err
	}
	return EncryptedCredential{
		Ciphertext:     credential.Ciphertext,
		WrappedDataKey: wrapped,
		KeyID:          vault.activeKeyID,
	}, nil
}

func validWrappingKey(keyID string, key []byte) bool {
	return keyID != "" && len(keyID) <= 128 && len(key) == 32
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
