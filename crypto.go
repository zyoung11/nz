package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	nonceSize    = 24
	keySize      = 32
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
)

var globalSalt = []byte("netzero-v1-global-salt-16")

// sharedKey derives a single global key from password, shared by all participants.
func sharedKey(password string) []byte {
	return argon2.IDKey([]byte(password), globalSalt, argonTime, argonMemory, argonThreads, keySize)
}

func genNonce() ([]byte, error) {
	buf := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, fmt.Errorf("genNonce: %w", err)
	}
	return buf, nil
}

func encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	nonce := make([]byte, aesgcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("encrypt nonce: %w", err)
	}
	return aesgcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	ns := aesgcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, fmt.Errorf("decrypt: ciphertext too short")
	}
	return aesgcm.Open(nil, ciphertext[:ns], ciphertext[ns:], nil)
}
