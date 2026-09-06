package tests

import (
	"database/sql"
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
	"time"

	_ "modernc.org/sqlite"
)

func TestPartnerOAuthAndClassic(t *testing.T) {
	root := repositoryRoot(t)
	workDir := t.TempDir()
	dbPath := filepath.Join(workDir, "accounts.db")
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
		"-db", dbPath,
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

	ppAuth, csrf := oauthLogin(t, &redirectClient, baseURL, "test@example.com", "testpass", "/partners")

	createForm := url.Values{
		"csrf_token":      {csrf.Value},
		"action":          {"create"},
		"name":            {"Demo Site"},
		"redirect_uris":   {"https://app.example.com/oauth/callback\nhttps://app.example.com/passport/return"},
		"classic_enabled": {"1"},
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/partners", strings.NewReader(createForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(ppAuth)
	request.AddCookie(csrf)
	resp, err := client.Do(request)
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

	// Unsupported response_type must not open-redirect to an unregistered URI.
	request, err = http.NewRequest(http.MethodGet, baseURL+"/oauth/authorize?response_type=token&client_id="+clientID+"&redirect_uri="+url.QueryEscape("https://evil.example/cb")+"&state=xyz", nil)
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
		t.Fatalf("unsupported response_type must not redirect to unregistered URI: location=%q", resp.Header.Get("Location"))
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unregistered redirect_uri with bad response_type: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
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
	consentCSRF := firstMatch(t, string(consentBody), `name="csrf_token" value="([^"]+)"`)
	if consentCSRF == "" {
		t.Fatal("consent page missing csrf_token")
	}
	csrfCookie := cookieNamed(resp, "LPCsrf")
	if csrfCookie == nil {
		csrfCookie = &http.Cookie{Name: "LPCsrf", Value: consentCSRF}
	}

	// Consent without CSRF must be rejected.
	badConsent := url.Values{
		"client_id":    {clientID},
		"redirect_uri": {"https://app.example.com/oauth/callback"},
		"state":        {"abc"},
		"decision":     {"allow"},
	}
	request, err = http.NewRequest(http.MethodPost, baseURL+"/oauth/authorize/consent", strings.NewReader(badConsent.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(ppAuth)
	request.AddCookie(csrfCookie)
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("consent without csrf_token: status=%d", resp.StatusCode)
	}

	consentForm := url.Values{
		"csrf_token":   {consentCSRF},
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
	request.AddCookie(csrfCookie)
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

	// Second user must not rotate or delete another owner's app.
	insertExtraPassportAccount(t, dbPath)
	otherAuth, otherCSRF := oauthLogin(t, &redirectClient, baseURL, "other@example.com", "otherpass", "/partners")
	rotateForm := url.Values{
		"csrf_token": {otherCSRF.Value},
		"action":     {"rotate"},
		"client_id":  {clientID},
	}
	request, err = http.NewRequest(http.MethodPost, baseURL+"/partners", strings.NewReader(rotateForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(otherAuth)
	request.AddCookie(otherCSRF)
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	rotateBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(rotateBody), "OAuth client not found") {
		t.Fatalf("non-owner rotate must fail: status=%d body=%s", resp.StatusCode, rotateBody)
	}
	if tok := firstMatch(t, string(rotateBody), `name="csrf_token" value="([^"]+)"`); tok != "" {
		otherCSRF = &http.Cookie{Name: "LPCsrf", Value: tok}
	}

	deleteForm := url.Values{
		"csrf_token": {otherCSRF.Value},
		"action":     {"delete"},
		"client_id":  {clientID},
	}
	request, err = http.NewRequest(http.MethodPost, baseURL+"/partners", strings.NewReader(deleteForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(otherAuth)
	request.AddCookie(otherCSRF)
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	deleteBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(deleteBody), "OAuth client not found") {
		t.Fatalf("non-owner delete must fail: status=%d body=%s", resp.StatusCode, deleteBody)
	}
	if strings.Contains(string(deleteBody), "Application deleted") {
		t.Fatal("non-owner must not delete foreign application")
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

func oauthLogin(t *testing.T, redirectClient *http.Client, baseURL, email, password, returnTo string) (*http.Cookie, *http.Cookie) {
	t.Helper()
	resp, err := redirectClient.Get(baseURL + "/oauth/login?return_to=" + url.QueryEscape(returnTo))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("oauth login page: status=%d", resp.StatusCode)
	}
	csrfToken := firstMatch(t, string(body), `name="csrf_token" value="([^"]+)"`)
	if csrfToken == "" {
		t.Fatal("login page missing csrf_token")
	}
	csrfCookie := cookieNamed(resp, "LPCsrf")
	if csrfCookie == nil {
		csrfCookie = &http.Cookie{Name: "LPCsrf", Value: csrfToken}
	}

	loginForm := url.Values{
		"csrf_token": {csrfToken},
		"email":      {email},
		"password":   {password},
		"return_to":  {returnTo},
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/oauth/login", strings.NewReader(loginForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(csrfCookie)
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	ppAuth := cookieNamed(resp, "PPAuth")
	if newCSRF := cookieNamed(resp, "LPCsrf"); newCSRF != nil {
		csrfCookie = newCSRF
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || ppAuth == nil {
		t.Fatalf("oauth login: status=%d cookie=%v", resp.StatusCode, resp.Cookies())
	}
	return ppAuth, csrfCookie
}

func insertExtraPassportAccount(t *testing.T, dbPath string) {
	t.Helper()
	// Allow the running server to finish any open write.
	time.Sleep(50 * time.Millisecond)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO passport_accounts (sign_in, passport_name, password, secret_question, secret_answer, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"other@example.com", "Other User", "otherpass", "", "", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert second account: %v", err)
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
