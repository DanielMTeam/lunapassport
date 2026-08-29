package tests

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"
)

func TestLunaPassportEndToEnd(t *testing.T) {
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

	request, err := http.NewRequest(http.MethodGet, baseURL+"/rdr/pprdr.asp", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("User-Agent", "Microsoft.NET-Passport-Authentication-Service/1.4")
	resp, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	passportURLs := resp.Header.Get("PassportURLs")
	if resp.StatusCode != http.StatusOK || !strings.Contains(passportURLs, "DALogin=login.passport.com/login2.asp") || !strings.Contains(passportURLs, "ConfigVersion=17") {
		t.Fatalf("XP Passport configuration: status=%d header=%q", resp.StatusCode, passportURLs)
	}

	redirectClient := *client
	redirectClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err = redirectClient.Get(baseURL + "/ppsecure/MSRV_EditProfile.asp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://login.passport.com/login2.srf" || resp.Header.Get("WWW-Authenticate") != "Passport1.4" {
		t.Fatalf("properties page must require Passport auth: status=%d location=%q challenge=%q", resp.StatusCode, resp.Header.Get("Location"), resp.Header.Get("WWW-Authenticate"))
	}
	var passportAttemptCookie *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "PassportAuthAttempt" {
			passportAttemptCookie = cookie
			break
		}
	}
	if passportAttemptCookie == nil || passportAttemptCookie.MaxAge <= 0 {
		t.Fatalf("Passport challenge must mark the pending sign-in attempt: cookies=%v", resp.Cookies())
	}
	request, err = http.NewRequest(http.MethodGet, baseURL+"/ppsecure/MSRV_EditProfile.asp", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(passportAttemptCookie)
	request.Header.Set("Accept-Language", "ru-RU")
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	cancelledBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(cancelledBody), "Вход отменён") || !strings.Contains(string(cancelledBody), "passportretry=1") {
		t.Fatalf("cancelled Passport sign-in page: status=%d", resp.StatusCode)
	}
	request, err = http.NewRequest(http.MethodGet, baseURL+"/ppsecure/MSRV_EditProfile.asp?passportretry=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(passportAttemptCookie)
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("WWW-Authenticate") != "Passport1.4" {
		t.Fatalf("Passport retry must reopen native authentication: status=%d challenge=%q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	resp, err = client.Get(baseURL + "/static/netpass/index.html")
	if err != nil {
		t.Fatal(err)
	}
	netpassBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	netpassPage := string(netpassBody)
	if !strings.Contains(netpassPage, `href="https://memberservices.passport.com/ppsecure/MSRV_EditProfile.asp"`) || !strings.Contains(netpassPage, "Войти с помощью .NET Passport") || strings.Contains(netpassPage, `type="password"`) {
		t.Fatalf("LunaPassport page must expose the Passport sign-in button without local password fields")
	}

	resp, err = client.Get(baseURL + "/defaultwiz.asp?step=1")
	if err != nil {
		t.Fatal(err)
	}
	stepOneBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stepOneBody), `id="email"`) || !strings.Contains(string(stepOneBody), "PassportAuthenticate") {
		t.Fatalf("Wizard first step must rely on native Passport sign-in without an email field")
	}

	resp, err = client.Get(baseURL + "/defaultwiz.asp?step=1&email=test%40example.com&lcid=1049")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	contentType := resp.Header.Get("Content-Type")
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(contentType), "windows-1251") {
		t.Fatalf("Russian Wizard pages must use windows-1251 for XP IE: Content-Type=%q", contentType)
	}
	decoded, err := charmap.Windows1251.NewDecoder().Bytes(body)
	if err != nil {
		t.Fatalf("decode windows-1251 wizard page: %v", err)
	}
	page := string(decoded)
	for _, marker := range []string{`<html lang="ru">`, "charset=windows-1251", "Войдите в LunaPassport", "Нажмите «Далее»", "FinalNext", `Property("passportname")`} {
		if !strings.Contains(page, marker) {
			t.Fatalf("Wizard response missing %q", marker)
		}
	}
	if strings.Contains(page, `id="password"`) || strings.Contains(page, `id="fallback"`) {
		t.Fatalf("Wizard must not expose the local password fallback")
	}

	form := url.Values{
		"step":     {"2"},
		"fallback": {"1"},
		"email":    {"test@example.com"},
		"password": {"testpass"},
	}
	request, err = http.NewRequest(http.MethodPost, baseURL+"/defaultwiz.asp?step=2", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "step=3") {
		t.Fatalf("Wizard fallback response: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, err = redirectClient.Get(baseURL + "/defaultwiz.asp?step=3&email=test%40example.com")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "step=1") || !strings.Contains(resp.Header.Get("Location"), "passportrequired=1") {
		t.Fatalf("Wizard step 3 must require Passport auth: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/login2.asp", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("User-Agent", "Mozilla/4.0 (compatible; MSIE 6.0; Windows NT 5.1; SV1)")
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	challenge := resp.Header.Get("WWW-Authenticate")
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(challenge, "Passport1.4 da-status=failed") || !strings.Contains(challenge, "prompt") {
		t.Fatalf("browser must receive the native Passport challenge: status=%d header=%q", resp.StatusCode, challenge)
	}

	resp, err = redirectClient.Get(baseURL + "/passport-signin.asp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://login.passport.com/login2.asp" || resp.Header.Get("WWW-Authenticate") != "Passport1.4 lunapassport=1" {
		t.Fatalf("Passport sign-in redirect: status=%d location=%q challenge=%q", resp.StatusCode, resp.Header.Get("Location"), resp.Header.Get("WWW-Authenticate"))
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/passport-signin.asp", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Cookie", "PassportWizardAuth=stale")
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://login.passport.com/login2.asp" {
		t.Fatalf("stale Wizard cookie must not change Passport sign-in redirect: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/passport-signin.asp", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Passport1.4 from-PP='t=stale-token&p=mock-profile'")
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://login.passport.com/login2.asp" {
		t.Fatalf("stale Passport authorization must trigger a new sign-in: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, err = client.Get(baseURL + "/login2.srf")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "Passport1.4") {
		t.Fatalf("Passport challenge: status=%d header=%q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/login2.srf", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Passport1.4 sign-in=test%40example.com,pwd=testpass")
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var ppAuthCookie *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "PPAuth" {
			ppAuthCookie = cookie
			break
		}
	}
	if ppAuthCookie == nil || ppAuthCookie.MaxAge <= 0 {
		t.Fatalf("Passport success must return a persistent PPAuth cookie: cookies=%v", resp.Cookies())
	}
	resp.Body.Close()
	authInfo := resp.Header.Get("Authentication-Info")
	if resp.StatusCode != http.StatusOK || !strings.Contains(authInfo, "da-status=success") {
		t.Fatalf("Passport success: status=%d auth-info=%q", resp.StatusCode, authInfo)
	}
	if !strings.Contains(authInfo, "MemberName=test%40example.com") && !strings.Contains(authInfo, "MemberName=test@example.com") {
		t.Fatalf("Passport success must include MemberName for XP CredMan: %q", authInfo)
	}
	request, err = http.NewRequest(http.MethodGet, baseURL+"/login2.srf", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuthCookie)
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Authentication-Info"), "da-status=success") {
		t.Fatalf("Passport must reuse a persistent PPAuth token without credentials: status=%d auth-info=%q", resp.StatusCode, resp.Header.Get("Authentication-Info"))
	}
	authInfo = resp.Header.Get("Authentication-Info")
	request, err = http.NewRequest(http.MethodGet, baseURL+"/passport-signin.asp", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuthCookie)
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://login.passport.com/login2.asp" {
		t.Fatalf("Wizard passport-signin must not cookie-short-circuit: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	request, err = http.NewRequest(http.MethodGet, baseURL+"/ppsecure/MSRV_EditProfile.asp", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(ppAuthCookie)
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("properties page must accept a valid PPAuth token: status=%d", resp.StatusCode)
	}
	fromPPStart := strings.Index(authInfo, "from-PP='")
	if fromPPStart < 0 {
		t.Fatalf("Passport success did not return from-PP: %q", authInfo)
	}
	fromPPStart += len("from-PP='")
	fromPPEnd := strings.IndexByte(authInfo[fromPPStart:], '\'')
	if fromPPEnd < 0 {
		t.Fatalf("Passport success returned malformed from-PP: %q", authInfo)
	}
	request, err = http.NewRequest(http.MethodGet, baseURL+"/ppsecure/MSRV_EditProfile.asp", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Passport1.4 from-PP='"+authInfo[fromPPStart:fromPPStart+fromPPEnd]+"'")
	passportAuthorization := request.Header.Get("Authorization")
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	propertiesBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated properties page: status=%d", resp.StatusCode)
	}
	propertiesPage := string(propertiesBody)
	for _, marker := range []string{"/static/css/main_api.css", "/static/properties.css", "LunaPassport", "test@example.com", "Test Passport", "required=\"required\""} {
		if !strings.Contains(propertiesPage, marker) {
			t.Fatalf("authenticated properties page missing %q", marker)
		}
	}

	requiredForm := url.Values{
		"email":         {"test@example.com"},
		"passport_name": {"Test Passport"},
	}
	request, err = http.NewRequest(http.MethodPost, baseURL+"/ppsecure/MSRV_EditProfile.asp", strings.NewReader(requiredForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", passportAuthorization)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	requiredBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(requiredBody), "Secret question is required.") {
		t.Fatalf("required Passport fields validation: status=%d body=%q", resp.StatusCode, requiredBody)
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/defaultwiz.asp?step=3&email=test%40example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", passportAuthorization)
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated Wizard step 3: status=%d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Test Passport") {
		t.Fatalf("authenticated Wizard step 3 does not show Passport name")
	}

	editForm := url.Values{
		"email":           {"updated@example.com"},
		"passport_name":   {"Updated Passport"},
		"password":        {"updatedpass"},
		"secret_question": {"What was your first pet?"},
		"secret_answer":   {"Mittens"},
	}
	request, err = http.NewRequest(http.MethodPost, baseURL+"/ppsecure/MSRV_EditProfile.asp", strings.NewReader(editForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", passportAuthorization)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ppsecure/MSRV_EditProfile.asp?saved=1" {
		t.Fatalf("update Passport account: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	passportAuthorization = "Passport1.4 sign-in=updated%40example.com,pwd=updatedpass"
	updatedClient := *client
	updatedClient.Jar = nil

	request, err = http.NewRequest(http.MethodGet, baseURL+"/ppsecure/MSRV_EditProfile.asp?saved=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", passportAuthorization)
	resp, err = updatedClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	updatedProperties, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	updatedPage := string(updatedProperties)
	for _, marker := range []string{"updated@example.com", "Updated Passport", "What was your first pet?", "Account settings saved."} {
		if !strings.Contains(updatedPage, marker) {
			t.Fatalf("updated properties page missing %q", marker)
		}
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/login2.srf", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Passport1.4 sign-in=updated%40example.com,pwd=updatedpass")
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Authentication-Info"), "da-status=success") {
		t.Fatalf("updated Passport credentials must authenticate: status=%d auth-info=%q", resp.StatusCode, resp.Header.Get("Authentication-Info"))
	}

	request, err = http.NewRequest(http.MethodPost, baseURL+"/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", passportAuthorization)
	resp, err = redirectClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/static/netpass/index.html" {
		t.Fatalf("Passport logout: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, err = redirectClient.Get(baseURL + "/ppsecure/MSRV_EditProfile.asp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://login.passport.com/login2.srf" {
		t.Fatalf("fully logged out Passport session must require sign-in: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	return filepath.Dir(filepath.Dir(file))
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
}

func waitForServer(t *testing.T, endpoint string) *http.Client {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(endpoint)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return client
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server did not become ready: %s", endpoint)
	return nil
}
