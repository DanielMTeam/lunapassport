package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOAuthRegistrationCreatesAccount(t *testing.T) {
	s := newServer("seed@example.com", "seed-password")
	v := url.Values{"email": {"new@example.com"}, "passport_name": {"New Passport"}, "password": {"strong-pass"}, "password_confirm": {"strong-pass"}, "secret_question": {"pet?"}, "secret_answer": {"cat"}}
	r := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("status=%d", w.Code)
	}
	_, ok, err := s.accounts.authenticate("new@example.com", "strong-pass")
	if err != nil || !ok {
		t.Fatal("registered account cannot authenticate")
	}
}
