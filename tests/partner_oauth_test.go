package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestPartnerOAuthAndClassic(t *testing.T) {
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
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
	}()

	baseURL := "http://127.0.0.1:" + httpPort
	client := waitForServer(t, baseURL+"/healthz")
	redirectClient := *client
	redirectClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	loginForm := url.Values{
		"email":    {"test@example.com"},
		"password": {"testpass"},
		"return_to": {"/partners"},
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/oauth/login", strings.NewReader(loginForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	ppAuth := cookieNamed(resp, "PPAuth")
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || ppAuth == nil {
		t.Fatalf("oauth login: status=%d cookie=%v", resp.StatusCode, resp.Cookies())
	}

	createForm := url.Values{
		"action":          {"create"},
		"name":            {"Demo Site"},
		"redirect_uris":   {"https://app.example.com/oauth/callback\nhttps://app.example.com/passport/return"},
		"classic_enabled": {"1"},
	}
	request, err = http.NewRequest(http.MethodPost, baseURL+"/partners", strings.NewReader(createForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(ppAuth)
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create partner app: status=%d", resp.StatusCode)
	}
	page := string(body)
	clientID := firstMatch(t, page, `client_id:</strong>\s*([a-f0-9]+)`)
	clientSecret := firstMatch(t, page, `client_secret:</strong>\s*([a-f0-9]+)`)
	if clientID == "" || clientSecret == "" {
		t.Fatalf("created credentials missing from partners page: %s", page)
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/oauth/authorize?response_type=code&client_id="+clientID+"&redirect_uri="+url.QueryEscape("https://evil.example/cb")+"&state=xyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuth)
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad redirect_uri must be rejected: status=%d", resp.StatusCode)
	}

	authorizeURL := baseURL + "/oauth/authorize?response_type=code&client_id=" + clientID +
		"&redirect_uri=" + url.QueryEscape("https://app.example.com/oauth/callback") + "&state=abc"
	request, err = http.NewRequest(http.MethodGet, authorizeURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuth)
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	consentBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(consentBody), "Allow Demo Site?") {
		t.Fatalf("consent page: status=%d body=%q", resp.StatusCode, consentBody)
	}

	consentForm := url.Values{
		"client_id":    {clientID},
		"redirect_uri": {"https://app.example.com/oauth/callback"},
		"state":        {"abc"},
		"decision":     {"allow"},
	}
	request, err = http.NewRequest(http.MethodPost, baseURL+"/oauth/authorize/consent", strings.NewReader(consentForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(ppAuth)
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("consent redirect: status=%d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	code := parsed.Query().Get("code")
	if code == "" || parsed.Query().Get("state") != "abc" {
		t.Fatalf("authorize redirect missing code/state: %q", location)
	}

	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"https://app.example.com/oauth/callback"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	resp, err = client.PostForm(baseURL+"/oauth/token", tokenForm)
	if err != nil {
		t.Fatal(err)
	}
	tokenBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token exchange: status=%d body=%s", resp.StatusCode, tokenBody)
	}
	var tokenResp map[string]interface{}
	if err := json.Unmarshal(tokenBody, &tokenResp); err != nil {
		t.Fatal(err)
	}
	accessToken, _ := tokenResp["access_token"].(string)
	if accessToken == "" {
		t.Fatalf("token response missing access_token: %s", tokenBody)
	}

	resp, err = client.PostForm(baseURL+"/oauth/token", tokenForm)
	if err != nil {
		t.Fatal(err)
	}
	reuseBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(reuseBody), "invalid_grant") {
		t.Fatalf("auth code must be one-time: status=%d body=%s", resp.StatusCode, reuseBody)
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/oauth/userinfo", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	infoBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(infoBody), `"sign_in":"test@example.com"`) {
		t.Fatalf("userinfo: status=%d body=%s", resp.StatusCode, infoBody)
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/login2.srf?browser=1&ru="+url.QueryEscape("https://evil.example/return"), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuth)
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusFound && strings.Contains(resp.Header.Get("Location"), "evil.example") {
		t.Fatalf("unknown OrgUrl must not redirect: location=%q", resp.Header.Get("Location"))
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/login2.srf?browser=1&ru="+url.QueryEscape("https://app.example.com/passport/return"), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuth)
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	mspAuth := cookieNamed(resp, "MSPAuth")
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://app.example.com/passport/return" || mspAuth == nil {
		t.Fatalf("classic browser redirect: status=%d location=%q cookies=%v", resp.StatusCode, resp.Header.Get("Location"), resp.Cookies())
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/partner/verify", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(mspAuth)
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	verifyBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(verifyBody), `"authenticated":true`) {
		t.Fatalf("partner verify: status=%d body=%s", resp.StatusCode, verifyBody)
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/login2.srf", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Passport1.4 sign-in=test%40example.com,pwd=testpass,OrgUrl=https://app.example.com/passport/return")
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Authentication-Info"), "da-status=success") {
		t.Fatalf("SSI Authorization path must stay intact: status=%d auth-info=%q", resp.StatusCode, resp.Header.Get("Authentication-Info"))
	}
}

func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == name && cookie.Value != "" {
			return cookie
		}
	}
	return nil
}

func firstMatch(t *testing.T, body, pattern string) string {
	t.Helper()
	re := regexp.MustCompile(pattern)
	match := re.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
