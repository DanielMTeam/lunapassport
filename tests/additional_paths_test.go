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

func loginForPartners(t *testing.T, server testServerProcess) *http.Cookie {
	t.Helper()
	form := url.Values{
		"email":     {"test@example.com"},
		"password":  {"testpass"},
		"return_to": {"/partners"},
	}
	request, err := http.NewRequest(http.MethodPost, server.baseURL+"/oauth/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := server.redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("OAuth partner login: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if cookie := cookieNamed(response, "PPAuth"); cookie != nil {
		return cookie
	}
	t.Fatalf("OAuth partner login did not return PPAuth: cookies=%v", response.Cookies())
	return nil
}

func createTestPartner(t *testing.T, server testServerProcess, ppAuth *http.Cookie, classic bool) (string, string) {
	t.Helper()
	classicValue := ""
	if classic {
		classicValue = "1"
	}
	form := url.Values{
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

func TestOAuthAuthorizeValidatesErrorRedirect(t *testing.T) {
	server := startTestServer(t)
	ppAuth := loginForPartners(t, server)
	clientID, _ := createTestPartner(t, server, ppAuth, false)

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
	ppAuth := loginForPartners(t, server)
	clientID, _ := createTestPartner(t, server, ppAuth, true)

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

	denyForm := url.Values{
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

	managementForm := url.Values{"action": {"disable"}, "client_id": {clientID}}
	request, err = http.NewRequest(http.MethodPost, server.baseURL+"/partners", strings.NewReader(managementForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(ppAuth)
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

	invalidLogin := url.Values{"email": {"test@example.com"}, "password": {"wrong"}, "return_to": {"/partners"}}
	response, err = server.client.PostForm(server.baseURL+"/oauth/login", invalidLogin)
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

	unsafeReturn := url.Values{"email": {"test@example.com"}, "password": {"testpass"}, "return_to": {"//evil.example/collect"}}
	response, err = server.redirectClient.PostForm(server.baseURL+"/oauth/login", unsafeReturn)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "/partners" {
		t.Fatalf("unsafe OAuth login return: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
}
