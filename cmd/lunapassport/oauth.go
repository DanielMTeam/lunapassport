package main

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type oauthLoginPageData struct {
	ClientID    string
	ClientName  string
	RedirectURI string
	State       string
	ReturnTo    string
	Email       string
	ErrorText   string
}

type oauthConsentPageData struct {
	ClientID    string
	ClientName  string
	RedirectURI string
	State       string
	SignIn      string
	ErrorText   string
}

var (
	oauthLoginTemplate   = template.Must(template.ParseFS(staticFiles, "static/oauth_login.html"))
	oauthConsentTemplate = template.Must(template.ParseFS(staticFiles, "static/oauth_consent.html"))
)

func (s *server) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	redirectURI := strings.TrimSpace(r.URL.Query().Get("redirect_uri"))
	state := r.URL.Query().Get("state")
	responseType := strings.TrimSpace(r.URL.Query().Get("response_type"))
	if responseType == "" {
		responseType = "code"
	}

	account, found, err := s.accounts.findByPassportName(passportName)
	if err != nil || !found {
		http.Error(w, "account store failure", http.StatusInternalServerError)
		return
	}
	signIn := account.SignIn

	client, found, err := s.accounts.findOAuthClient(clientID)
	if err != nil {
		http.Error(w, "account store failure", http.StatusInternalServerError)
		return
	}
	if !found || !client.Enabled {
		http.Error(w, "unknown or disabled client", http.StatusBadRequest)
		return
	}
	if !client.allowsRedirectURI(redirectURI) {
		http.Error(w, "redirect_uri is not registered for this client", http.StatusBadRequest)
		return
	}
	if responseType != "code" {
		s.writeOAuthAuthorizeError(w, redirectURI, state, "unsupported_response_type", "Only response_type=code is supported")
		return
	}

	if signIn, ok := s.browserPassportUser(r); ok {
		s.renderOAuthConsent(w, oauthConsentPageData{
			ClientID:    client.ClientID,
			ClientName:  client.Name,
			RedirectURI: redirectURI,
			State:       state,
			SignIn:      signIn,
		})
		return
	}

	s.renderOAuthLogin(w, oauthLoginPageData{
		ClientID:    client.ClientID,
		ClientName:  client.Name,
		RedirectURI: redirectURI,
		State:       state,
	})
}

func (s *server) handleOAuthLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.renderOAuthLogin(w, oauthLoginPageData{
			ClientID:    strings.TrimSpace(r.URL.Query().Get("client_id")),
			RedirectURI: strings.TrimSpace(r.URL.Query().Get("redirect_uri")),
			State:       r.URL.Query().Get("state"),
			ReturnTo:    strings.TrimSpace(r.URL.Query().Get("return_to")),
			Email:       strings.TrimSpace(r.URL.Query().Get("email")),
		})
		return
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		email := strings.TrimSpace(r.Form.Get("email"))
		password := r.Form.Get("password")
		clientID := strings.TrimSpace(r.Form.Get("client_id"))
		redirectURI := strings.TrimSpace(r.Form.Get("redirect_uri"))
		state := r.Form.Get("state")
		returnTo := strings.TrimSpace(r.Form.Get("return_to"))

		account, valid, err := s.accounts.authenticate(email, password)
		if err != nil {
			http.Error(w, "account store failure", http.StatusInternalServerError)
			return
		}
		if !valid {
			data := oauthLoginPageData{
				ClientID:    clientID,
				RedirectURI: redirectURI,
				State:       state,
				ReturnTo:    returnTo,
				Email:       email,
				ErrorText:   "Invalid e-mail or password.",
			}
			if clientID != "" {
				if client, found, _ := s.accounts.findOAuthClient(clientID); found {
					data.ClientName = client.Name
				}
			}
			w.WriteHeader(http.StatusUnauthorized)
			s.renderOAuthLogin(w, data)
			return
		}

		token, err := randomToken(24)
		if err != nil {
			http.Error(w, "cannot create token", http.StatusInternalServerError)
			return
		}
		if err := s.rememberToken(token, account.PassportName); err != nil {
			http.Error(w, "cannot store token", http.StatusInternalServerError)
			return
		}
		s.setPPAuthCookie(w, r, token)

		if returnTo != "" && isSafeLocalReturn(returnTo) {
			http.Redirect(w, r, returnTo, http.StatusFound)
			return
		}
		if clientID == "" || redirectURI == "" {
			http.Redirect(w, r, "/partners", http.StatusFound)
			return
		}

		client, found, err := s.accounts.findOAuthClient(clientID)
		if err != nil {
			http.Error(w, "account store failure", http.StatusInternalServerError)
			return
		}
		if !found || !client.Enabled || !client.allowsRedirectURI(redirectURI) {
			http.Error(w, "invalid OAuth client or redirect_uri", http.StatusBadRequest)
			return
		}
		s.renderOAuthConsent(w, oauthConsentPageData{
			ClientID:    client.ClientID,
			ClientName:  client.Name,
			RedirectURI: redirectURI,
			State:       state,
			SignIn:      account.SignIn,
		})
		return
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleOAuthConsent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	clientID := strings.TrimSpace(r.Form.Get("client_id"))
	redirectURI := strings.TrimSpace(r.Form.Get("redirect_uri"))
	state := r.Form.Get("state")
	decision := strings.TrimSpace(r.Form.Get("decision"))

	passportName, ok := s.browserPassportUser(r)
	if !ok {
		q := url.Values{}
		q.Set("client_id", clientID)
		q.Set("redirect_uri", redirectURI)
		q.Set("state", state)
		q.Set("response_type", "code")
		http.Redirect(w, r, "/oauth/authorize?"+q.Encode(), http.StatusFound)
		return
	}

	client, found, err := s.accounts.findOAuthClient(clientID)
	if err != nil {
		http.Error(w, "account store failure", http.StatusInternalServerError)
		return
	}
	if !found || !client.Enabled || !client.allowsRedirectURI(redirectURI) {
		http.Error(w, "invalid OAuth client or redirect_uri", http.StatusBadRequest)
		return
	}

	if decision != "allow" {
		s.redirectOAuthError(w, r, redirectURI, state, "access_denied", "The user denied the request")
		return
	}

	code, err := randomToken(24)
	if err != nil {
		http.Error(w, "cannot create authorization code", http.StatusInternalServerError)
		return
	}
	if err := s.accounts.saveOAuthAuthCode(code, client.ClientID, signIn, redirectURI, time.Now().Add(oauthAuthCodeLifetime)); err != nil {
		http.Error(w, "cannot store authorization code", http.StatusInternalServerError)
		return
	}

	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := target.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.writeOAuthJSONError(w, http.StatusBadRequest, "invalid_request", "Unable to parse form body")
		return
	}

	grantType := strings.TrimSpace(r.Form.Get("grant_type"))
	if grantType != "authorization_code" {
		s.writeOAuthJSONError(w, http.StatusBadRequest, "unsupported_grant_type", "Only authorization_code is supported")
		return
	}

	clientID, clientSecret := r.Form.Get("client_id"), r.Form.Get("client_secret")
	if clientID == "" || clientSecret == "" {
		if user, pass, ok := r.BasicAuth(); ok {
			clientID, clientSecret = user, pass
		}
	}
	client, ok, err := s.accounts.authenticateOAuthClient(clientID, clientSecret)
	if err != nil {
		http.Error(w, "account store failure", http.StatusInternalServerError)
		return
	}
	if !ok {
		s.writeOAuthJSONError(w, http.StatusUnauthorized, "invalid_client", "Client authentication failed")
		return
	}

	code := strings.TrimSpace(r.Form.Get("code"))
	redirectURI := strings.TrimSpace(r.Form.Get("redirect_uri"))
	authCode, found, err := s.accounts.consumeOAuthAuthCode(code, client.ClientID, redirectURI, time.Now())
	if err != nil {
		http.Error(w, "account store failure", http.StatusInternalServerError)
		return
	}
	if !found {
		s.writeOAuthJSONError(w, http.StatusBadRequest, "invalid_grant", "Authorization code is invalid, expired, or already used")
		return
	}

	accessToken, err := randomToken(24)
	if err != nil {
		http.Error(w, "cannot create access token", http.StatusInternalServerError)
		return
	}
	expiresIn := int(oauthAccessTokenLifetime / time.Second)
	if err := s.accounts.saveOAuthAccessToken(accessToken, client.ClientID, authCode.AccountSignIn, time.Now().Add(oauthAccessTokenLifetime)); err != nil {
		http.Error(w, "cannot store access token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   expiresIn,
	})
}

func (s *server) handleOAuthUserInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := bearerToken(r)
	if token == "" {
		s.writeOAuthJSONError(w, http.StatusUnauthorized, "invalid_token", "Bearer access token required")
		return
	}
	record, found, err := s.accounts.findOAuthAccessToken(token, time.Now())
	if err != nil {
		http.Error(w, "account store failure", http.StatusInternalServerError)
		return
	}
	if !found {
		s.writeOAuthJSONError(w, http.StatusUnauthorized, "invalid_token", "Access token is invalid or expired")
		return
	}

	account, found, err := s.accounts.find(record.AccountSignIn)
	if err != nil {
		http.Error(w, "account store failure", http.StatusInternalServerError)
		return
	}
	if !found {
		s.writeOAuthJSONError(w, http.StatusUnauthorized, "invalid_token", "Account no longer exists")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"sign_in":       account.SignIn,
		"passport_name": account.PassportName,
		"sub":           account.SignIn,
	})
}

func (s *server) handleOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.writeOAuthJSONError(w, http.StatusBadRequest, "invalid_request", "Unable to parse form body")
		return
	}
	token := strings.TrimSpace(r.Form.Get("token"))
	if token == "" {
		token = bearerToken(r)
	}
	if token == "" {
		s.writeOAuthJSONError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	if err := s.accounts.deleteOAuthAccessToken(token); err != nil {
		http.Error(w, "account store failure", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *server) browserPassportUser(r *http.Request) (string, bool) {
	token := passportTokenFromRequest(r)
	if token == "" {
		return "", false
	}
	return s.tokenUser(token)
}

func (s *server) setPPAuthCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "PPAuth",
		Value:    token,
		Domain:   s.passportCookieDomain(r),
		Path:     "/",
		Expires:  time.Now().Add(passportTokenLifetime),
		MaxAge:   int(passportTokenLifetime / time.Second),
		Secure:   true,
		HttpOnly: true,
	})
}

func (s *server) renderOAuthLogin(w http.ResponseWriter, data oauthLoginPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = oauthLoginTemplate.Execute(w, data)
}

func (s *server) renderOAuthConsent(w http.ResponseWriter, data oauthConsentPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = oauthConsentTemplate.Execute(w, data)
}

func (s *server) writeOAuthAuthorizeError(w http.ResponseWriter, redirectURI, state, code, description string) {
	if redirectURI == "" {
		http.Error(w, description, http.StatusBadRequest)
		return
	}
	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, description, http.StatusBadRequest)
		return
	}
	q := target.Query()
	q.Set("error", code)
	q.Set("error_description", description)
	if state != "" {
		q.Set("state", state)
	}
	target.RawQuery = q.Encode()
	w.Header().Set("Location", target.String())
	w.WriteHeader(http.StatusFound)
}

func (s *server) redirectOAuthError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, description string) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, description, http.StatusBadRequest)
		return
	}
	q := target.Query()
	q.Set("error", code)
	q.Set("error_description", description)
	if state != "" {
		q.Set("state", state)
	}
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *server) writeOAuthJSONError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) < 8 || !strings.EqualFold(auth[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(auth[7:])
}

func isSafeLocalReturn(path string) bool {
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return false
	}
	if strings.Contains(path, "://") {
		return false
	}
	return true
}
