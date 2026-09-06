package tests

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type testServerProcess struct {
	baseURL        string
	client         *http.Client
	redirectClient *http.Client
}

func startTestServer(t *testing.T) testServerProcess {
	t.Helper()
	root := repositoryRoot(t)
	workDir := t.TempDir()
	binary := filepath.Join(workDir, "lunapassport")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/lunapassport")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lunapassport: %v\n%s", err, output)
	}

	httpPort := freePort(t)
	cmd := exec.Command(binary,
		"-http", "127.0.0.1:"+httpPort,
		"-db", filepath.Join(workDir, "accounts.db"),
		"-seed-account-email", "test@example.com",
		"-seed-account-password", "testpass",
		"-seed-account-passport-name", "Test Passport",
		"-oauth-secret-pepper", "test-pepper",
	)
	cmd.Dir = root
	logFile, err := os.Create(filepath.Join(workDir, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
	})

	baseURL := "http://127.0.0.1:" + httpPort
	client := waitForServer(t, baseURL+"/healthz")
	redirectClient := *client
	redirectClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return testServerProcess{baseURL: baseURL, client: client, redirectClient: &redirectClient}
}

func loginForPartners(t *testing.T, server testServerProcess) (*http.Cookie, *http.Cookie) {
	t.Helper()
	response, err := server.redirectClient.Get(server.baseURL + "/oauth/login?return_to=" + url.QueryEscape("/partners"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("OAuth login page: status=%d", response.StatusCode)
	}
	csrfToken := firstMatch(t, string(body), `name="csrf_token" value="([^"]+)"`)
	if csrfToken == "" {
		t.Fatal("login page missing csrf_token")
	}
	csrfCookie := cookieNamed(response, "LPCsrf")
	if csrfCookie == nil {
		csrfCookie = &http.Cookie{Name: "LPCsrf", Value: csrfToken}
	}

	form := url.Values{
		"csrf_token": {csrfToken},
		"email":      {"test@example.com"},
		"password":   {"testpass"},
		"return_to":  {"/partners"},
	}
	request, err := http.NewRequest(http.MethodPost, server.baseURL+"/oauth/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(csrfCookie)
	response, err = server.redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("OAuth partner login: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if newCSRF := cookieNamed(response, "LPCsrf"); newCSRF != nil {
		csrfCookie = newCSRF
	}
	if cookie := cookieNamed(response, "PPAuth"); cookie != nil {
		return cookie, csrfCookie
	}
	t.Fatalf("OAuth partner login did not return PPAuth: cookies=%v", response.Cookies())
	return nil, nil
}

func createTestPartner(t *testing.T, server testServerProcess, ppAuth, csrf *http.Cookie, classic bool) (string, string) {
	t.Helper()
	classicValue := ""
	if classic {
		classicValue = "1"
	}
	form := url.Values{
		"csrf_token":      {csrf.Value},
		"action":          {"create"},
		"name":            {"Additional Test App"},
		"redirect_uris":   {"https://app.example.com/oauth/callback\nhttps://app.example.com/passport/return"},
		"classic_enabled": {classicValue},
	}
	request, err := http.NewRequest(http.MethodPost, server.baseURL+"/partners", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(ppAuth)
	request.AddCookie(csrf)
	response, err := server.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create partner: status=%d body=%s", response.StatusCode, body)
	}
	page := string(body)
	clientID := firstMatch(t, page, `client_id:</strong>\s*([a-f0-9]+)`)
	clientSecret := firstMatch(t, page, `client_secret:</strong>\s*([a-f0-9]+)`)
	if clientID == "" || clientSecret == "" {
		t.Fatalf("created partner credentials missing: %s", page)
	}
	return clientID, clientSecret
}

func fetchCSRF(t *testing.T, server testServerProcess, ppAuth *http.Cookie) (*http.Cookie, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, server.baseURL+"/partners", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuth)
	response, err := server.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	token := firstMatch(t, string(body), `name="csrf_token" value="([^"]+)"`)
	if token == "" {
		t.Fatal("partners page missing csrf_token")
	}
	csrf := cookieNamed(response, "LPCsrf")
	if csrf == nil {
		csrf = &http.Cookie{Name: "LPCsrf", Value: token}
	}
	return csrf, token
}

func TestOAuthAuthorizeValidatesErrorRedirect(t *testing.T) {
	server := startTestServer(t)
	ppAuth, csrf := loginForPartners(t, server)
	clientID, _ := createTestPartner(t, server, ppAuth, csrf, false)

	request, err := http.NewRequest(http.MethodGet, server.baseURL+"/oauth/authorize?response_type=token&client_id="+url.QueryEscape(clientID)+"&redirect_uri="+url.QueryEscape("https://evil.example/callback")+"&state=private", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuth)
	response, err := server.redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || response.Header.Get("Location") != "" {
		t.Fatalf("unvalidated OAuth error redirect: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	request, err = http.NewRequest(http.MethodGet, server.baseURL+"/oauth/authorize?response_type=token&client_id="+url.QueryEscape(clientID)+"&redirect_uri="+url.QueryEscape("https://app.example.com/oauth/callback")+"&state=private", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuth)
	response, err = server.redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	redirect, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusFound || redirect.Host != "app.example.com" || redirect.Query().Get("error") != "unsupported_response_type" || redirect.Query().Get("state") != "private" {
		t.Fatalf("validated OAuth error redirect: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
}

func TestOAuthAndPartnerFailurePaths(t *testing.T) {
	server := startTestServer(t)
	ppAuth, csrf := loginForPartners(t, server)
	clientID, _ := createTestPartner(t, server, ppAuth, csrf, true)

	response, err := server.client.PostForm(server.baseURL+"/oauth/token", url.Values{"grant_type": {"password"}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), `"error":"unsupported_grant_type"`) {
		t.Fatalf("unsupported OAuth grant: status=%d body=%s", response.StatusCode, body)
	}

	request, err := http.NewRequest(http.MethodGet, server.baseURL+"/oauth/userinfo", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = server.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), `"error":"invalid_token"`) {
		t.Fatalf("missing OAuth bearer token: status=%d body=%s", response.StatusCode, body)
	}

	authorizeURL := server.baseURL + "/oauth/authorize?response_type=code&client_id=" + url.QueryEscape(clientID) +
		"&redirect_uri=" + url.QueryEscape("https://app.example.com/oauth/callback") + "&state=state-value"
	request, err = http.NewRequest(http.MethodGet, authorizeURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuth)
	response, err = server.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	consentBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	consentCSRF := firstMatch(t, string(consentBody), `name="csrf_token" value="([^"]+)"`)
	if consentCSRF == "" {
		t.Fatal("consent page missing csrf_token")
	}
	csrfCookie := cookieNamed(response, "LPCsrf")
	if csrfCookie == nil {
		csrfCookie = &http.Cookie{Name: "LPCsrf", Value: consentCSRF}
	}

	denyForm := url.Values{
		"csrf_token":   {consentCSRF},
		"client_id":    {clientID},
		"redirect_uri": {"https://app.example.com/oauth/callback"},
		"state":        {"state-value"},
		"decision":     {"deny"},
	}
	request, err = http.NewRequest(http.MethodPost, server.baseURL+"/oauth/authorize/consent", strings.NewReader(denyForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(ppAuth)
	request.AddCookie(csrfCookie)
	response, err = server.redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	redirect, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusFound || redirect.Query().Get("error") != "access_denied" || redirect.Query().Get("state") != "state-value" {
		t.Fatalf("denied consent: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}

	csrf, csrfToken := fetchCSRF(t, server, ppAuth)
	managementForm := url.Values{"csrf_token": {csrfToken}, "action": {"disable"}, "client_id": {clientID}}
	request, err = http.NewRequest(http.MethodPost, server.baseURL+"/partners", strings.NewReader(managementForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(ppAuth)
	request.AddCookie(csrf)
	response, err = server.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disable partner: status=%d", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodGet, server.baseURL+"/oauth/authorize?client_id="+url.QueryEscape(clientID)+"&redirect_uri="+url.QueryEscape("https://app.example.com/oauth/callback"), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuth)
	response, err = server.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("disabled partner was authorized: status=%d", response.StatusCode)
	}

	loginPage, err := server.client.Get(server.baseURL + "/oauth/login?return_to=/partners")
	if err != nil {
		t.Fatal(err)
	}
	loginBody, err := io.ReadAll(loginPage.Body)
	loginPage.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	loginCSRF := firstMatch(t, string(loginBody), `name="csrf_token" value="([^"]+)"`)
	loginCSRFCookie := cookieNamed(loginPage, "LPCsrf")
	if loginCSRFCookie == nil {
		loginCSRFCookie = &http.Cookie{Name: "LPCsrf", Value: loginCSRF}
	}

	invalidLogin := url.Values{"csrf_token": {loginCSRF}, "email": {"test@example.com"}, "password": {"wrong"}, "return_to": {"/partners"}}
	request, err = http.NewRequest(http.MethodPost, server.baseURL+"/oauth/login", strings.NewReader(invalidLogin.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(loginCSRFCookie)
	response, err = server.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "Invalid e-mail or password.") {
		t.Fatalf("invalid OAuth login: status=%d body=%s", response.StatusCode, body)
	}
	if tok := firstMatch(t, string(body), `name="csrf_token" value="([^"]+)"`); tok != "" {
		loginCSRF = tok
	}

	unsafeReturn := url.Values{"csrf_token": {loginCSRF}, "email": {"test@example.com"}, "password": {"testpass"}, "return_to": {"//evil.example/collect"}}
	request, err = http.NewRequest(http.MethodPost, server.baseURL+"/oauth/login", strings.NewReader(unsafeReturn.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(loginCSRFCookie)
	response, err = server.redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "/partners" {
		t.Fatalf("unsafe OAuth login return: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
}
