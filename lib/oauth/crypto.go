// Package oauth handles Google OAuth flows, token storage, and at-rest sealing.
package oauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// Sealer wraps refresh tokens with AES-256-GCM before they hit Postgres.
type Sealer struct {
	gcm cipher.AEAD
}

// NewSealer returns a Sealer initialised with the given 32-byte AES-256 key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("oauth: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("oauth: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("oauth: new gcm: %w", err)
	}
	return &Sealer{gcm: gcm}, nil
}

// Seal encrypts plaintext with a fresh random nonce prepended to the output.
func (s *Sealer) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("oauth: nonce: %w", err)
	}
	return s.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal: it strips the nonce prefix and decrypts the body.
func (s *Sealer) Open(ciphertext []byte) ([]byte, error) {
	ns := s.gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("oauth: ciphertext too short")
	}
	nonce, body := ciphertext[:ns], ciphertext[ns:]
	return s.gcm.Open(nil, nonce, body, nil)
}
