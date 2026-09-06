package main

import (
	"crypto/subtle"
	"net/http"
)

const (
	csrfCookieName = "LPCsrf"
	csrfFormField  = "csrf_token"
)

// ensureCSRFToken returns the browser CSRF token, setting a cookie when needed.
func (s *server) ensureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	token, err := randomToken(16)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return token
}

// validateCSRF reports whether the form token matches the CSRF cookie.
func (s *server) validateCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	formToken := r.FormValue(csrfFormField)
	if formToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(formToken)) == 1
}
