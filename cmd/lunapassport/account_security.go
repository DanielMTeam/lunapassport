package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const encryptedValuePrefix = "enc:v1:"

type accountCipher struct{ gcm cipher.AEAD }

func openAccountStoreWithEncryption(path string, initial passportAccount, pepper, encodedKey string) (*accountStore, error) {
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encodedKey))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("ACCOUNT_ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	c, err := newAccountCipher(key)
	if err != nil {
		return nil, err
	}
	return openAccountStoreWithCipher(path, initial, pepper, c)
}
func newAccountCipher(key []byte) (*accountCipher, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &accountCipher{g}, nil
}
func (c *accountCipher) encrypt(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	n := make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, n); err != nil {
		return "", err
	}
	return encryptedValuePrefix + base64.RawStdEncoding.EncodeToString(append(n, c.gcm.Seal(nil, n, []byte(v), nil)...)), nil
}
func (c *accountCipher) decrypt(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	if !strings.HasPrefix(v, encryptedValuePrefix) {
		return v, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(v, encryptedValuePrefix))
	if err != nil {
		return "", err
	}
	n := c.gcm.NonceSize()
	if len(raw) < n {
		return "", fmt.Errorf("invalid encrypted value")
	}
	plain, err := c.gcm.Open(nil, raw[:n], raw[n:], nil)
	return string(plain), err
}
func testAccountCipher(pepper string) *accountCipher {
	sum := sha256.Sum256([]byte("lunapassport-test:" + pepper))
	c, _ := newAccountCipher(sum[:])
	return c
}
func isBcryptHash(v string) bool { return strings.HasPrefix(v, "$2") }
func (s *accountStore) protectAccount(a *passportAccount) error {
	var err error
	if a.Password != "" && !isBcryptHash(a.Password) {
		h, e := bcrypt.GenerateFromPassword([]byte(a.Password), bcrypt.DefaultCost)
		if e != nil {
			return e
		}
		a.Password = string(h)
	}
	if a.SecretAnswer != "" && !isBcryptHash(a.SecretAnswer) {
		h, e := bcrypt.GenerateFromPassword([]byte(a.SecretAnswer), bcrypt.DefaultCost)
		if e != nil {
			return e
		}
		a.SecretAnswer = string(h)
	}
	if a.SecretQuestion != "" && !strings.HasPrefix(a.SecretQuestion, encryptedValuePrefix) {
		a.SecretQuestion, err = s.cipher.encrypt(a.SecretQuestion)
	}
	return err
}
func (s *accountStore) revealAccount(a *passportAccount) error {
	q, err := s.cipher.decrypt(a.SecretQuestion)
	a.SecretQuestion = q
	return err
}
