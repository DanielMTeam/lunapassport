package main

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/text/encoding/charmap"
)

type wizardPageData struct {
	Lang            string
	Charset         string
	Copy            wizardCopy
	Step            string
	StepNumber      int
	Back            int
	Last            int
	Email           string
	PassportName    string
	Ticket          string
	ErrorText       string
	FallbackVisible bool
}

var wizardTemplate = template.Must(template.New("wizard.html").Funcs(template.FuncMap{
	"wizardTitle": wizardTitle,
}).ParseFS(staticFiles, "static/wizard.html"))

type propertiesPageData struct {
	Username        string
	PassportName    string
	SecretQuestion  string
	HasSecretAnswer bool
	ErrorText       string
	Saved           bool
}

var propertiesTemplate = template.Must(template.ParseFS(staticFiles, "static/properties.html"))

// handleWizard serves the server-side portion of the XP Passport Wizard.
// The native first page is rendered by netplwiz.dll; XP then hosts these HTML
// pages and calls the WebWizardHost methods through window.external.
func (s *server) handleWizard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	step := wizardValue(r, "step", "1")
	if step != "1" && step != "2" && step != "3" {
		step = "1"
	}
	email := wizardValue(r, "email", s.username)
	language := wizardLanguage(r)
	ticket := ""
	if step == "3" {
		token := passportTokenFromRequest(r)
		if token == "" {
			token = tokenFromRequest(r)
		}
		if user, ok := s.tokenUser(token); ok {
			if email == "" || email == s.username {
				email = user
			}
			profile := s.profileBlobForToken(token, email)
			ticket = "t=" + token + "&p=" + url.QueryEscape(profile)
			w.Header().Set("Authentication-Info", "Passport1.4 da-status=success,tname=MSPAuth,tname=MSPProf,MemberName="+url.QueryEscape(email)+",from-PP='"+ticket+"'")
			s.setPartnerTokenCookies(w, r, token)
		}
		if !s.hasValidPassportAuth(r) && ticket == "" {
			location := "/defaultwiz.asp?step=1&passportrequired=1&email=" + url.QueryEscape(email)
			http.Redirect(w, r, location, http.StatusFound)
			return
		}
	}
	passportName := s.passportName(email)
	errorText := ""
	if step == "2" && r.URL.Query().Get("nativeerror") == "1" {
		errorText = copyForLanguage(language).NativeUnavailable
	}
	if step == "2" && r.URL.Query().Get("passportrequired") == "1" {
		errorText = copyForLanguage(language).PassportRequired
	}

	if step == "2" && r.Method == http.MethodPost && r.Form.Get("fallback") == "1" {
		account, valid, err := s.accounts.authenticate(email, r.Form.Get("password"))
		if err != nil {
			http.Error(w, "account store failure", http.StatusInternalServerError)
			return
		}
		if !valid {
			s.writeWizard(w, step, email, copyForLanguage(language).InvalidCredentials, language, passportName, "")
			return
		}
		passportName = account.PassportName

		token, err := randomToken(24)
		if err != nil {
			http.Error(w, "cannot create wizard token", http.StatusInternalServerError)
			return
		}
		if err := s.rememberToken(token, account.PassportName); err != nil {
			http.Error(w, "cannot store wizard token", http.StatusInternalServerError)
			return
		}
		s.setPPAuthCookie(w, r, token)
		s.setPartnerTokenCookies(w, r, token)
		http.SetCookie(w, &http.Cookie{Name: "PassportWizardAuth", Value: token, Path: "/", HttpOnly: true})
		location := "/defaultwiz.asp?step=3&email=" + url.QueryEscape(email) + "&fallback=1"
		http.Redirect(w, r, location, http.StatusFound)
		return
	}

	s.writeWizard(w, step, email, errorText, language, passportName, ticket)
}

func wizardLanguage(r *http.Request) string {
	return detectWizardLanguage(
		wizardValue(r, "lcid", ""),
		wizardValue(r, "langid", ""),
		r.Header.Get("Accept-Language"),
	)
}

func (s *server) passportName(email string) string {
	account, ok, err := s.accounts.find(email)
	if err == nil && ok && account.PassportName != "" {
		return account.PassportName
	}
	return email
}

func wizardValue(r *http.Request, key, fallback string) string {
	if value := r.URL.Query().Get(key); value != "" {
		return value
	}
	if value := r.Form.Get(key); value != "" {
		return value
	}
	return fallback
}

func writeWizard(w http.ResponseWriter, step, email, errorText, language, passportName, ticket string) {
	writeWizardBody(w, wizardHTMLForLanguage(step, email, errorText, language, passportName, ticket), language)
}

func (s *server) writeWizard(w http.ResponseWriter, step, email, errorText, language, passportName, ticket string) {
	body := wizardHTMLForLanguage(step, email, errorText, language, passportName, ticket)
	body = strings.ReplaceAll(body, "__PASSPORT_LOGIN_URL__", s.loginURL(""))
	writeWizardBody(w, body, language)
}

func writeWizardBody(w http.ResponseWriter, body, language string) {
	w.Header().Set("Cache-Control", "no-cache")
	payload := []byte(body)
	contentType := "text/html; charset=utf-8"
	// Russian XP hosts the Wizard in a legacy WebBrowser control that often
	// interprets pages as the system ANSI code page (windows-1251). UTF-8
	// Cyrillic then corrupts the inline JavaScript and FinalNext never runs.
	if language == "ru" {
		if encoded, err := charmap.Windows1251.NewEncoder().Bytes(payload); err == nil {
			payload = encoded
			contentType = "text/html; charset=windows-1251"
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
	_, _ = w.Write(payload)
}

func wizardHTML(step, email, errorText string) string {
	return wizardHTMLForLanguage(step, email, errorText, "en", email, "")
}

func wizardHTMLForLanguage(step, email, errorText, language string, extras ...string) string {
	stepNumber := 1
	if step == "2" {
		stepNumber = 2
	} else if step == "3" {
		stepNumber = 3
	}
	passportName := email
	ticket := ""
	if len(extras) > 0 && extras[0] != "" {
		passportName = extras[0]
	}
	if len(extras) > 1 {
		ticket = extras[1]
	}
	charset := "utf-8"
	if language == "ru" {
		charset = "windows-1251"
	}
	data := wizardPageData{
		Lang:            language,
		Charset:         charset,
		Copy:            copyForLanguage(language),
		Step:            step,
		StepNumber:      stepNumber,
		Back:            wizardBack(step),
		Last:            wizardLast(step),
		Email:           email,
		PassportName:    passportName,
		Ticket:          ticket,
		ErrorText:       errorText,
		FallbackVisible: errorText != "",
	}
	var body strings.Builder
	if err := wizardTemplate.Execute(&body, data); err != nil {
		panic(err)
	}
	return body.String()
}

func wizardBack(step string) int {
	if step == "1" {
		return 0
	}
	return 1
}

func wizardLast(step string) int {
	if step == "3" {
		return 1
	}
	return 0
}

func wizardTitle(step string) string {
	switch step {
	case "2":
		return "Verify your .NET Passport"
	case "3":
		return "Your .NET Passport is ready"
	default:
		return "Enter your .NET Passport address"
	}
}

func (s *server) handleProperties(w http.ResponseWriter, r *http.Request) {
	account, ok, err := s.passportAccountFromRequest(r)
	if err != nil {
		http.Error(w, "account store failure", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "Passport account not found", http.StatusNotFound)
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		updated := account
		updated.SignIn = strings.TrimSpace(r.Form.Get("email"))
		updated.PassportName = strings.TrimSpace(r.Form.Get("passport_name"))
		updated.SecretQuestion = strings.TrimSpace(r.Form.Get("secret_question"))
		if password := r.Form.Get("password"); password != "" {
			updated.Password = password
		}
		if !strings.Contains(updated.SignIn, "@") {
			s.renderProperties(w, account, "Enter a valid e-mail address.", false)
			return
		}
		if updated.PassportName == "" {
			s.renderProperties(w, account, "Passport name is required.", false)
			return
		}
		if updated.SecretQuestion == "" {
			s.renderProperties(w, account, "Secret question is required.", false)
			return
		}
		if answer := r.Form.Get("secret_answer"); answer != "" {
			updated.SecretAnswer = answer
		} else if account.SecretAnswer == "" || updated.SecretQuestion != account.SecretQuestion {
			s.renderProperties(w, account, "Secret answer is required when setting a question.", false)
			return
		}
		if err := s.accounts.updateAccount(account.SignIn, updated); err != nil {
			s.renderProperties(w, account, err.Error(), false)
			return
		}
		s.rebindToken(tokenFromRequest(r), updated.SignIn)
		http.Redirect(w, r, "/ppsecure/MSRV_EditProfile.asp?saved=1", http.StatusFound)
		return
	}

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.renderProperties(w, account, "", r.URL.Query().Get("saved") == "1")
}

func (s *server) passportAccountFromRequest(r *http.Request) (passportAccount, bool, error) {
	token := tokenFromRequest(r)
	if token == "" {
		token = passportTokenFromRequest(r)
	}
	identity, ok := s.tokenUser(token)
	if !ok {
		return passportAccount{}, false, nil
	}
	account, found, err := s.accounts.find(identity)
	if err != nil || found {
		return account, found, err
	}
	return s.accounts.findByPassportName(identity)
}

func (s *server) renderProperties(w http.ResponseWriter, account passportAccount, errorText string, saved bool) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := propertiesPageData{
		Username:        account.SignIn,
		PassportName:    account.PassportName,
		SecretQuestion:  account.SecretQuestion,
		HasSecretAnswer: account.SecretAnswer != "",
		ErrorText:       errorText,
		Saved:           saved,
	}
	if err := propertiesTemplate.Execute(w, data); err != nil {
		http.Error(w, "cannot render properties page", http.StatusInternalServerError)
	}
}

func writePassportCancelledPage(w http.ResponseWriter, r *http.Request) {
	language := wizardLanguage(r)
	title := "Sign-in cancelled"
	message := "Passport sign-in was cancelled. You can try again or return to LunaPassport."
	retry := "Try signing in again"
	back := "Return to LunaPassport"
	if language == "ru" {
		title = "Вход отменён"
		message = "Вход через .NET Passport был отменён. Можно попробовать ещё раз или вернуться в LunaPassport."
		retry = "Повторить вход"
		back = "Вернуться в LunaPassport"
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="%s"><head><meta http-equiv="Content-Type" content="text/html; charset=utf-8"><title>LunaPassport</title><link rel="stylesheet" type="text/css" href="/static/css/main_api.css"></head>
<body><div class="container"><div class="header"><div class="rl_l"><h1 class="txtr">LunaPassport</h1><div class="subtitle txtr">Connect through .NET Passport</div></div></div><div class="headerline"></div><div class="main"><div class="content"><div class="info_splash"><b>%s</b><p>%s</p><p><a class="box_action_button" href="/ppsecure/MSRV_EditProfile.asp?passportretry=1">%s</a></p><p><a href="/static/netpass/index.html">%s</a></p></div></div></div></div></body></html>`, language, title, message, retry, back)
}
