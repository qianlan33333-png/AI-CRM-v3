package wecom

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

// CallbackCrypto validates the WeCom signature and decrypts its callback
// envelope. The AES key is injected; it is never logged or persisted here.
type CallbackCrypto struct {
	token  string
	key    []byte
	corpID string
	now    func() time.Time
}

func NewCallbackCrypto(token, encodingAESKey, corpID string) (*CallbackCrypto, error) {
	if strings.TrimSpace(token) != token || token == "" || strings.TrimSpace(corpID) != corpID || corpID == "" {
		return nil, ErrSignature
	}
	decoded, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil || len(decoded) != 32 {
		return nil, ErrSignature
	}
	return &CallbackCrypto{token: token, key: decoded, corpID: corpID, now: time.Now}, nil
}

func (crypto *CallbackCrypto) VerifyAndDecrypt(signature, timestamp, nonce, encrypted string) ([]byte, error) {
	if crypto == nil || !crypto.validTimestamp(timestamp) || encrypted == "" {
		return nil, ErrCallbackExpired
	}
	expected := callbackSignature(crypto.token, timestamp, nonce, encrypted)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		return nil, ErrSignature
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, ErrMalformedXML
	}
	block, err := aes.NewCipher(crypto.key)
	if err != nil {
		return nil, ErrMalformedXML
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, crypto.key[:aes.BlockSize]).CryptBlocks(plain, ciphertext)
	plain, err = unpad32(plain)
	if err != nil || len(plain) < 20 {
		return nil, ErrMalformedXML
	}
	messageLength := int(binary.BigEndian.Uint32(plain[16:20]))
	if messageLength < 1 || messageLength > len(plain)-20 {
		return nil, ErrMalformedXML
	}
	message := plain[20 : 20+messageLength]
	if subtle.ConstantTimeCompare(plain[20+messageLength:], []byte(crypto.corpID)) != 1 {
		return nil, ErrCorpMismatch
	}
	return message, nil
}

func (crypto *CallbackCrypto) validTimestamp(value string) bool {
	var unix int64
	for _, character := range value {
		if character < '0' || character > '9' || unix > 1<<62/10 {
			return false
		}
		unix = unix*10 + int64(character-'0')
	}
	if value == "" {
		return false
	}
	delta := crypto.now().UTC().Sub(time.Unix(unix, 0).UTC())
	return delta <= 5*time.Minute && delta >= -60*time.Second
}

func callbackSignature(token, timestamp, nonce, encrypted string) string {
	parts := []string{token, timestamp, nonce, encrypted}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(sum[:])
}

func unpad32(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, errors.New("empty padded input")
	}
	padding := int(value[len(value)-1])
	if padding < 1 || padding > 32 || padding > len(value) {
		return nil, errors.New("invalid padding")
	}
	var invalid byte
	for _, item := range value[len(value)-padding:] {
		invalid |= item ^ byte(padding)
	}
	if invalid != 0 {
		return nil, errors.New("invalid padding")
	}
	return value[:len(value)-padding], nil
}
