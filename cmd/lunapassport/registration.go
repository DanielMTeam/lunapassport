package main

import (
	"io/fs"
	"net/http"
	"strings"
)

func (s *server) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.renderRegistration(w)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", 405)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	if r.Form.Get("password") != r.Form.Get("password_confirm") {
		http.Error(w, "passwords do not match", 400)
		return
	}
	err := s.accounts.createAccount(passportAccount{SignIn: strings.TrimSpace(r.Form.Get("email")), PassportName: strings.TrimSpace(r.Form.Get("passport_name")), Password: r.Form.Get("password"), SecretQuestion: strings.TrimSpace(r.Form.Get("secret_question")), SecretAnswer: r.Form.Get("secret_answer")})
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	a, _, err := s.accounts.find(strings.TrimSpace(r.Form.Get("email")))
	if err != nil {
		http.Error(w, "account store failure", 500)
		return
	}
	token, err := randomToken(24)
	if err != nil {
		http.Error(w, "cannot create token", 500)
		return
	}
	if err = s.rememberToken(token, a.PassportName); err != nil {
		http.Error(w, "cannot store token", 500)
		return
	}
	s.setPPAuthCookie(w, r, token)
	http.Redirect(w, r, "/partners", 302)
}
func (s *server) renderRegistration(w http.ResponseWriter) {
	b, err := fs.ReadFile(staticFiles, "static/oauth_register.html")
	if err != nil {
		http.Error(w, "cannot read registration page", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}
