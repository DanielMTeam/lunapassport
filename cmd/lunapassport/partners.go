package main

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

type partnersClientView struct {
	ClientID        string
	Name            string
	Enabled         bool
	ClassicEnabled  bool
	RedirectURIList []string
}

type partnersPageData struct {
	SignIn              string
	Clients             []partnersClientView
	ErrorText           string
	StatusText          string
	CreatedClientID     string
	CreatedClientSecret string
	RotatedClientID     string
	RotatedClientSecret string
	FormName            string
	FormRedirectURIs    string
	FormClassic         bool
	CSRFToken           string
}

var partnersTemplate = template.Must(template.ParseFS(staticFiles, "static/partners.html"))

func (s *server) handlePartners(w http.ResponseWriter, r *http.Request) {
	signIn, ok := s.browserPassportUser(r)
	if !ok {
		login := "/oauth/login?return_to=" + url.QueryEscape("/partners")
		http.Redirect(w, r, login, http.StatusFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.renderPartners(w, r, signIn, partnersPageData{})
		return
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if !s.validateCSRF(r) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		action := strings.TrimSpace(r.Form.Get("action"))
		switch action {
		case "create":
			s.handlePartnerCreate(w, r, signIn)
		case "rotate":
			s.handlePartnerRotate(w, r, signIn)
		case "disable":
			s.handlePartnerToggle(w, r, signIn, false)
		case "enable":
			s.handlePartnerToggle(w, r, signIn, true)
		case "delete":
			s.handlePartnerDelete(w, r, signIn)
		default:
			http.Error(w, "unknown action", http.StatusBadRequest)
		}
		return
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handlePartnerCreate(w http.ResponseWriter, r *http.Request, signIn string) {
	name := strings.TrimSpace(r.Form.Get("name"))
	redirectURIs := splitRedirectURIs(r.Form.Get("redirect_uris"))
	classic := r.Form.Get("classic_enabled") == "1"
	secret, err := randomToken(24)
	if err != nil {
		http.Error(w, "cannot create client secret", http.StatusInternalServerError)
		return
	}
	client, err := s.accounts.createOAuthClient(signIn, name, secret, redirectURIs, classic)
	if err != nil {
		s.renderPartners(w, r, signIn, partnersPageData{
			ErrorText:        err.Error(),
			FormName:         name,
			FormRedirectURIs: r.Form.Get("redirect_uris"),
			FormClassic:      classic,
		})
		return
	}
	s.renderPartners(w, r, signIn, partnersPageData{
		CreatedClientID:     client.ClientID,
		CreatedClientSecret: secret,
	})
}

func (s *server) handlePartnerRotate(w http.ResponseWriter, r *http.Request, signIn string) {
	clientID := strings.TrimSpace(r.Form.Get("client_id"))
	secret, err := randomToken(24)
	if err != nil {
		http.Error(w, "cannot create client secret", http.StatusInternalServerError)
		return
	}
	if err := s.accounts.updateOAuthClientSecret(clientID, signIn, secret); err != nil {
		s.renderPartners(w, r, signIn, partnersPageData{ErrorText: err.Error()})
		return
	}
	s.renderPartners(w, r, signIn, partnersPageData{
		RotatedClientID:     clientID,
		RotatedClientSecret: secret,
	})
}

func (s *server) handlePartnerToggle(w http.ResponseWriter, r *http.Request, signIn string, enabled bool) {
	clientID := strings.TrimSpace(r.Form.Get("client_id"))
	if err := s.accounts.setOAuthClientEnabled(clientID, signIn, enabled); err != nil {
		s.renderPartners(w, r, signIn, partnersPageData{ErrorText: err.Error()})
		return
	}
	status := "Application disabled."
	if enabled {
		status = "Application enabled."
	}
	s.renderPartners(w, r, signIn, partnersPageData{StatusText: status})
}

func (s *server) handlePartnerDelete(w http.ResponseWriter, r *http.Request, signIn string) {
	clientID := strings.TrimSpace(r.Form.Get("client_id"))
	if err := s.accounts.deleteOAuthClient(clientID, signIn); err != nil {
		s.renderPartners(w, r, signIn, partnersPageData{ErrorText: err.Error()})
		return
	}
	s.renderPartners(w, r, signIn, partnersPageData{StatusText: "Application deleted."})
}

func (s *server) renderPartners(w http.ResponseWriter, r *http.Request, signIn string, data partnersPageData) {
	clients, err := s.accounts.listOAuthClientsForOwner(signIn)
	if err != nil {
		http.Error(w, "account store failure", http.StatusInternalServerError)
		return
	}
	data.SignIn = signIn
	data.CSRFToken = s.ensureCSRFToken(w, r)
	data.Clients = make([]partnersClientView, 0, len(clients))
	for _, client := range clients {
		data.Clients = append(data.Clients, partnersClientView{
			ClientID:        client.ClientID,
			Name:            client.Name,
			Enabled:         client.Enabled,
			ClassicEnabled:  client.ClassicEnabled,
			RedirectURIList: client.redirectURIList(),
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = partnersTemplate.Execute(w, data)
}
