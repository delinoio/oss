package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

type EncryptedPayload struct {
	Ciphertext   []byte
	WrappedDEK   []byte
	PayloadNonce []byte
	DEKNonce     []byte
}

func encryptPayload(masterKey []byte, plaintext []byte) (EncryptedPayload, error) {
	if len(masterKey) != 32 {
		return EncryptedPayload{}, errors.New("master key must be 32 bytes")
	}

	dek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return EncryptedPayload{}, fmt.Errorf("read dek: %w", err)
	}

	payloadBlock, err := aes.NewCipher(dek)
	if err != nil {
		return EncryptedPayload{}, fmt.Errorf("create payload cipher: %w", err)
	}
	payloadGCM, err := cipher.NewGCM(payloadBlock)
	if err != nil {
		return EncryptedPayload{}, fmt.Errorf("create payload gcm: %w", err)
	}
	payloadNonce := make([]byte, payloadGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, payloadNonce); err != nil {
		return EncryptedPayload{}, fmt.Errorf("read payload nonce: %w", err)
	}
	ciphertext := payloadGCM.Seal(nil, payloadNonce, plaintext, nil)

	masterBlock, err := aes.NewCipher(masterKey)
	if err != nil {
		return EncryptedPayload{}, fmt.Errorf("create master cipher: %w", err)
	}
	masterGCM, err := cipher.NewGCM(masterBlock)
	if err != nil {
		return EncryptedPayload{}, fmt.Errorf("create master gcm: %w", err)
	}
	dekNonce := make([]byte, masterGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, dekNonce); err != nil {
		return EncryptedPayload{}, fmt.Errorf("read dek nonce: %w", err)
	}
	wrappedDEK := masterGCM.Seal(nil, dekNonce, dek, nil)

	return EncryptedPayload{
		Ciphertext:   ciphertext,
		WrappedDEK:   wrappedDEK,
		PayloadNonce: payloadNonce,
		DEKNonce:     dekNonce,
	}, nil
}

func decryptPayload(masterKey []byte, encrypted EncryptedPayload) ([]byte, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("master key must be 32 bytes")
	}

	masterBlock, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("create master cipher: %w", err)
	}
	masterGCM, err := cipher.NewGCM(masterBlock)
	if err != nil {
		return nil, fmt.Errorf("create master gcm: %w", err)
	}
	dek, err := masterGCM.Open(nil, encrypted.DEKNonce, encrypted.WrappedDEK, nil)
	if err != nil {
		return nil, fmt.Errorf("unwrap dek: %w", err)
	}

	payloadBlock, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("create payload cipher: %w", err)
	}
	payloadGCM, err := cipher.NewGCM(payloadBlock)
	if err != nil {
		return nil, fmt.Errorf("create payload gcm: %w", err)
	}
	plaintext, err := payloadGCM.Open(nil, encrypted.PayloadNonce, encrypted.Ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt payload: %w", err)
	}
	return plaintext, nil
}
