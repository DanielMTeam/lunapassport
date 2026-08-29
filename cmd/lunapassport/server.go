package main

import (
	"log"
	"net/http"
	"sync"
	"time"
)

const passportTokenLifetime = 30 * 24 * time.Hour

type server struct {
	username string
	password string
	accounts *accountStore
	domains  passportDomains

	mu     sync.RWMutex
	tokens map[string]string
}

type passportDomains struct {
	nexusHost          string
	loginHost          string
	registerHost       string
	memberservicesHost string
	wwwHost            string
	nexusrdrHost       string
	cookieDomain       string
}

func defaultPassportDomains() passportDomains {
	return passportDomains{
		nexusHost:          "nexus.passport.com",
		loginHost:          "login.passport.com",
		registerHost:       "register.passport.com",
		memberservicesHost: "memberservices.passport.com",
		wwwHost:            "www.passport.com",
		nexusrdrHost:       "nexusrdr.passport.com",
		cookieDomain:       ".passport.com",
	}
}

func customPassportDomains(passportHost, memberservicesHost, cookieDomain string) passportDomains {
	domains := defaultPassportDomains()
	if passportHost != "" {
		domains.nexusHost = passportHost
		domains.loginHost = passportHost
		domains.registerHost = passportHost
		domains.wwwHost = passportHost
		domains.nexusrdrHost = passportHost
	}
	if memberservicesHost != "" {
		domains.memberservicesHost = memberservicesHost
	}
	if cookieDomain != "" {
		domains.cookieDomain = cookieDomain
	}
	return domains
}

func newServer(username, password string) *server {
	accounts, err := openAccountStore(":memory:", passportAccount{
		SignIn:       username,
		PassportName: username,
		Password:     password,
	})
	if err != nil {
		panic(err)
	}
	return newServerWithAccounts(username, password, accounts)
}

func newServerWithAccounts(username, password string, accounts *accountStore) *server {
	return &server{
		username: username,
		password: password,
		accounts: accounts,
		domains:  defaultPassportDomains(),
		tokens:   make(map[string]string),
	}
}

func newServerWithPassportDomains(username, password string, accounts *accountStore, domains passportDomains) *server {
	server := newServerWithAccounts(username, password, accounts)
	server.domains = domains
	return server
}

func (s *server) passportURL(host, path string) string {
	return "https://" + host + path
}

func (s *server) loginURL(path string) string {
	return s.passportURL(s.domains.loginHost, path)
}

func (s *server) memberservicesURL(path string) string {
	return s.passportURL(s.domains.memberservicesHost, path)
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/rdr/pprdr.asp", s.handleNexus)
	mux.HandleFunc("/login2.srf", s.handleLogin)
	mux.HandleFunc("/login2.asp", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/passport-signin.asp", s.handlePassportSignIn)
	mux.HandleFunc("/passport-logo.gif", s.handlePassportLogo)
	mux.HandleFunc("/defaultwiz.asp", s.handleWizard)
	mux.HandleFunc("/uixpwiz.srf", s.handleWizard)
	mux.HandleFunc("/UIXPWiz.srf", s.handleWizard)
	mux.Handle("/ppsecure/MSRV_EditProfile.asp", s.requirePassportAuth(http.HandlerFunc(s.handleProperties)))
	mux.HandleFunc("/partner/verify", s.handlePartnerVerify)
	mux.HandleFunc("/partner/complete", s.handlePartnerComplete)
	mux.HandleFunc("/partner", s.handlePartner)
	mux.HandleFunc("/oauth/authorize", s.handleOAuthAuthorize)
	mux.HandleFunc("/oauth/login", s.handleOAuthLogin)
	mux.HandleFunc("/oauth/register", s.handleOAuthRegister)
	mux.HandleFunc("/oauth/authorize/consent", s.handleOAuthConsent)
	mux.HandleFunc("/oauth/token", s.handleOAuthToken)
	mux.HandleFunc("/oauth/userinfo", s.handleOAuthUserInfo)
	mux.HandleFunc("/oauth/revoke", s.handleOAuthRevoke)
	mux.HandleFunc("/partners", s.handlePartners)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.Handle("/static/", s.staticHandler())
	mux.HandleFunc("/netpass/", s.handleLunaPassportHome)
	mux.HandleFunc("/", s.handleLunaPassportRoot)
	return loggingMiddleware(legacyBrowserOnly(mux))
}

func (s *server) rememberToken(token, passportName string) error {
	s.mu.Lock()
	s.tokens[token] = passportName
	s.mu.Unlock()
	return s.accounts.saveToken(token, passportName, time.Now().Add(passportTokenLifetime))
}

func (s *server) tokenUser(token string) (string, bool) {
	s.mu.RLock()
	passportName, cached := s.tokens[token]
	s.mu.RUnlock()
	if !cached {
		var ok bool
		var err error
		passportName, ok, err = s.accounts.findToken(token, time.Now())
		if err != nil || !ok {
			return "", false
		}
	}
	if _, found, err := s.accounts.findByPassportName(passportName); err != nil || !found {
		_ = s.accounts.deleteToken(token)
		s.mu.Lock()
		delete(s.tokens, token)
		s.mu.Unlock()
		return "", false
	}
	if !cached {
		s.mu.Lock()
		s.tokens[token] = passportName
		s.mu.Unlock()
	}
	return passportName, true
}

func (s *server) rebindToken(token, signIn string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	s.tokens[token] = signIn
	s.mu.Unlock()
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *server) handleLunaPassportHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/netpass/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/static/netpass/index.html", http.StatusFound)
}

func (s *server) handleLunaPassportRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.handleNotFound(w, r)
		return
	}
	http.Redirect(w, r, "/static/netpass/index.html", http.StatusFound)
}

func (s *server) handleNotFound(w http.ResponseWriter, _ *http.Request) {
	http.NotFound(w, nil)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s host=%s ua=%q", r.Method, r.URL.RequestURI(), r.Host, r.UserAgent())
		next.ServeHTTP(w, r)
	})
}
