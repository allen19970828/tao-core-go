package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encryptedPrefix = "enc:v1:"

type SecretCipher struct {
	aead cipher.AEAD
}

func NewSecretCipher(encodedKey string) (*SecretCipher, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedKey))
	if err != nil {
		return nil, fmt.Errorf("APP_ENCRYPTION_KEY 必須是有效的 base64: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("APP_ENCRYPTION_KEY 解碼後必須正好是 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretCipher{aead: aead}, nil
}

func (c *SecretCipher) Encrypt(plaintext string) (string, error) {
	if c == nil {
		return "", errors.New("秘密加密器未初始化")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, sealed...)
	return encryptedPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (c *SecretCipher) Decrypt(ciphertext string) (string, error) {
	if c == nil {
		return "", errors.New("秘密加密器未初始化")
	}
	if !strings.HasPrefix(ciphertext, encryptedPrefix) {
		return "", errors.New("拒絕讀取未加密的舊版秘密")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ciphertext, encryptedPrefix))
	if err != nil {
		return "", errors.New("加密秘密格式無效")
	}
	if len(payload) < c.aead.NonceSize() {
		return "", errors.New("加密秘密長度無效")
	}
	nonce, sealed := payload[:c.aead.NonceSize()], payload[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", errors.New("無法解密秘密")
	}
	return string(plaintext), nil
}
