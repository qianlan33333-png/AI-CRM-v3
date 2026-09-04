package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

const KeyVersion int16 = 1

type ContactCipher struct{ aead cipher.AEAD }

func NewContactCipher(encoded string) (*ContactCipher, error) {
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("order contact data key must be 32 raw-base64 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &ContactCipher{aead: aead}, nil
}

func (c *ContactCipher) Encrypt(value string) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, errors.New("order contact cipher unavailable")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(value), []byte("order-contact-phone:v1")), nil
}

func (c *ContactCipher) KeyVersion() int16 { return KeyVersion }
