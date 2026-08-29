package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestAccountStoreProtection(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	s, err := openAccountStoreWithEncryption(":memory:", passportAccount{}, "pepper", key)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	a := passportAccount{SignIn: "new@example.com", PassportName: "New User", Password: "password-123", SecretQuestion: "pet?", SecretAnswer: "cat"}
	if err = s.createAccount(a); err != nil {
		t.Fatal(err)
	}
	var raw passportAccount
	if err = s.db.First(&raw, "sign_in = ?", a.SignIn).Error; err != nil {
		t.Fatal(err)
	}
	if raw.Password == a.Password || !strings.HasPrefix(raw.Password, "$2") {
		t.Fatal("password stored unhashed")
	}
	if raw.SecretQuestion == a.SecretQuestion || !strings.HasPrefix(raw.SecretQuestion, encryptedValuePrefix) {
		t.Fatal("question stored unencrypted")
	}
	if raw.SecretAnswer == a.SecretAnswer || !strings.HasPrefix(raw.SecretAnswer, "$2") {
		t.Fatal("answer stored unhashed")
	}
	_, ok, err := s.authenticate(a.SignIn, a.Password)
	if err != nil || !ok {
		t.Fatal("authentication failed")
	}
}
