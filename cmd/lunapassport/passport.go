package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const passportSignInMessage = "Please sign in to LunaPassport to link your account with the system and supported applications."
const passportAttemptCookie = "PassportAuthAttempt"

func (s *server) handleNexus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header()["PassportURLs"] = []string{s.passportURLs(strings.Contains(r.UserAgent(), "Microsoft.NET-Passport-Authentication-Service/1.4"))}
	w.Header().Set("Cache-Control", "private")
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

func (s *server) passportURLs(wizard bool) string {
	loginPath := "/login2.srf"
	if wizard {
		loginPath = "/login2.asp"
	}
	return fmt.Sprintf("DARealm=Passport.Net,DALogin=%s%s,DAReg=%s,Properties=%s,Privacy=%s,GeneralRedir=%s,Help=%s,ConfigVersion=17",
		s.domains.loginHost,
		loginPath,
		s.passportURL(s.domains.registerHost, "/defaultwiz.asp"),
		s.memberservicesURL("/ppsecure/MSRV_EditProfile.asp"),
		s.passportURL(s.domains.wwwHost, "/consumer/privacypolicy.asp"),
		s.passportURL(s.domains.nexusrdrHost, "/redir.asp"),
		s.memberservicesURL("/UI/MSRV_UI_Help.asp"),
	)
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := parsePassportAuthorization(r.Header.Get("Authorization"))
	user, password := auth["sign-in"], auth["pwd"]

	if user == "" && password == "" {
		if token := passportTokenFromRequest(r); token != "" {
			if _, ok := s.tokenUser(token); ok {
				s.writePassportTokenResponse(w, r, token, auth)
				return
			}
		}
		cbtxt := strings.ReplaceAll(url.QueryEscape(passportSignInMessage), "+", "%20")
		writePassportChallenge(w, "da-status=failed,srealm=Passport.NET,ts=0,prompt,cburl="+s.loginURL("/passport-logo.gif")+",cbtxt="+cbtxt)
		return
	}

	account, valid, err := s.accounts.authenticate(user, password)
	if err != nil {
		http.Error(w, "account store failure", http.StatusInternalServerError)
		return
	}
	if !valid {
		writePassportChallenge(w, "da-status=failed-noretry,srealm=Passport.NET,ts=-1")
		return
	}

	token, err := randomToken(24)
	if err != nil {
		http.Error(w, "cannot create token", http.StatusInternalServerError)
		return
	}
	if err := s.rememberToken(token, account.SignIn); err != nil {
		http.Error(w, "cannot store token", http.StatusInternalServerError)
		return
	}

	s.writePassportTokenResponse(w, r, token, auth)
}

func (s *server) writePassportTokenResponse(w http.ResponseWriter, r *http.Request, token string, auth map[string]string) {
	fromPP := "t=" + token + "&p=mock-profile"
	returnURL := auth["OrgUrl"]
	if returnURL == "" {
		returnURL = auth["OrgURL"]
	}
	if returnURL == "" {
		returnURL = s.loginURL("/passport-signin.asp")
	}
	authInfo := "Passport1.4 da-status=success,tname=PPAuth,from-PP='" + fromPP + "',ru=" + returnURL
	cookieDomain := s.passportCookieDomain(r)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Connection", "close")
	w.Header().Set("Authentication-Info", authInfo)
	http.SetCookie(w, &http.Cookie{Name: "PPAuth", Value: token, Domain: cookieDomain, Path: "/", Expires: time.Now().Add(passportTokenLifetime), MaxAge: int(passportTokenLifetime / time.Second), Secure: true, HttpOnly: true})
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

func (s *server) passportCookieDomain(r *http.Request) string {
	cookieDomain := s.domains.cookieDomain
	if cookieDomain == "" {
		return ""
	}
	host := r.Host
	if parsedHost, _, err := net.SplitHostPort(r.Host); err == nil {
		host = parsedHost
	}
	baseDomain := strings.TrimPrefix(cookieDomain, ".")
	if host != baseDomain && !strings.HasSuffix(host, "."+baseDomain) {
		cookieDomain = ""
	}
	return cookieDomain
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := passportTokenFromRequest(r)
	if token != "" {
		s.mu.Lock()
		delete(s.tokens, token)
		s.mu.Unlock()
		if err := s.accounts.deleteToken(token); err != nil {
			http.Error(w, "cannot delete token", http.StatusInternalServerError)
			return
		}
	}

	for _, name := range []string{"MSPAuth", "MSPProf", "PPAuth"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Domain:   s.passportCookieDomain(r),
			Path:     "/",
			Expires:  time.Unix(1, 0),
			MaxAge:   -1,
			Secure:   true,
			HttpOnly: name != "MSPProf",
		})
	}
	s.clearPassportAttemptCookie(w, r)
	http.Redirect(w, r, "/static/netpass/index.html", http.StatusFound)
}

func (s *server) handlePassportSignIn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if token := passportTokenFromRequest(r); token != "" {
		if _, ok := s.tokenUser(token); ok {
			s.handlePartnerToken(w, r, token)
			return
		}
	}
	if s.hasValidPassportAuth(r) {
		s.handlePartner(w, r)
		return
	}

	w.Header().Set("Location", s.loginURL("/login2.asp"))
	w.Header().Set("WWW-Authenticate", "Passport1.4 lunapassport=1")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "close")
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusFound)
}

func (s *server) requirePassportAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hasValidPassportAuth(r) {
			if hasCookie(r, passportAttemptCookie) && r.URL.Query().Get("passportretry") != "1" {
				writePassportCancelledPage(w, r)
				return
			}
			s.setPassportAttemptCookie(w, r)
			w.Header().Set("Location", s.loginURL("/login2.srf"))
			w.Header().Set("WWW-Authenticate", "Passport1.4")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusFound)
			return
		}
		s.clearPassportAttemptCookie(w, r)
		if token := passportTokenFromRequest(r); token != "" && !hasCookie(r, "MSPAuth") {
			s.setPartnerTokenCookies(w, r, token)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) setPassportAttemptCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: passportAttemptCookie, Value: "1", Domain: s.passportCookieDomain(r), Path: "/", MaxAge: 300, Secure: true, HttpOnly: true})
}

func (s *server) clearPassportAttemptCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: passportAttemptCookie, Value: "", Domain: s.passportCookieDomain(r), Path: "/", Expires: time.Unix(1, 0), MaxAge: -1, Secure: true, HttpOnly: true})
}

func hasCookie(r *http.Request, name string) bool {
	cookie, err := r.Cookie(name)
	return err == nil && cookie.Value != ""
}

func (s *server) hasValidPassportAuth(r *http.Request) bool {
	if !hasCookie(r, "MSPAuth") && !hasCookie(r, "PPAuth") && r.Header.Get("Authorization") == "" {
		return false
	}

	token := tokenFromRequest(r)
	if token == "" {
		token = passportTokenFromRequest(r)
	}
	if token == "" {
		return false
	}
	_, ok := s.tokenUser(token)
	return ok
}

func (s *server) handlePassportLogo(w http.ResponseWriter, _ *http.Request) {
	const gif = "R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs="
	data, err := base64.StdEncoding.DecodeString(gif)
	if err != nil {
		http.Error(w, "cannot create logo", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *server) handlePartner(w http.ResponseWriter, r *http.Request) {
	token := tokenFromRequest(r)
	_, ok := s.tokenUser(token)
	if !ok {
		w.Header().Set("Location", s.loginURL("/login2.srf"))
		w.Header().Set("WWW-Authenticate", "Passport1.4")
		w.WriteHeader(http.StatusFound)
		return
	}

	s.handlePartnerToken(w, r, token)
}

func (s *server) handlePartnerToken(w http.ResponseWriter, r *http.Request, token string) {
	user, ok := s.tokenUser(token)
	if !ok {
		w.Header().Set("Location", s.loginURL("/login2.srf"))
		w.Header().Set("WWW-Authenticate", "Passport1.4")
		w.WriteHeader(http.StatusFound)
		return
	}

	w.Header().Set("Authentication-Info", "Passport1.4 tname=MSPAuth,tname=MSPProf")
	s.setPartnerTokenCookies(w, r, token)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "authenticated user=%s\n", user)
}

func (s *server) setPartnerTokenCookies(w http.ResponseWriter, r *http.Request, token string) {
	cookieDomain := s.passportCookieDomain(r)
	http.SetCookie(w, &http.Cookie{Name: "MSPAuth", Value: token, Domain: cookieDomain, Path: "/", Secure: true, HttpOnly: true})
	http.SetCookie(w, &http.Cookie{Name: "MSPProf", Value: "mock-profile", Domain: cookieDomain, Path: "/", Secure: true})
}

func writePassportChallenge(w http.ResponseWriter, details string) {
	w.Header().Set("WWW-Authenticate", "Passport1.4 "+details)
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "close")
	body := []byte(`<!DOCTYPE html><html><head><title>Passport</title><script language="JavaScript">function OnBack(){}</script></head><body></body></html>`)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write(body)
}

func tokenFromRequest(r *http.Request) string {
	if cookie, err := r.Cookie("MSPAuth"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	auth := parsePassportAuthorization(r.Header.Get("Authorization"))
	fromPP := auth["from-PP"]
	if strings.HasPrefix(fromPP, "t=") {
		fromPP = strings.TrimPrefix(fromPP, "t=")
		if i := strings.IndexByte(fromPP, '&'); i >= 0 {
			fromPP = fromPP[:i]
		}
	}
	return fromPP
}

func passportTokenFromRequest(r *http.Request) string {
	if cookie, err := r.Cookie("PPAuth"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return tokenFromRequest(r)
}

func parsePassportAuthorization(value string) map[string]string {
	result := make(map[string]string)
	if !strings.HasPrefix(strings.ToLower(value), "passport1.4") {
		return result
	}
	value = strings.TrimSpace(value[len("Passport1.4"):])
	for _, field := range strings.Split(value, ",") {
		field = strings.TrimSpace(field)
		key, raw, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		raw = strings.Trim(strings.TrimSpace(raw), "'\"")
		if decoded, err := url.QueryUnescape(raw); err == nil {
			raw = decoded
		}
		result[key] = raw
	}
	return result
}
