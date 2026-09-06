# Partner SSO guide

How to sign users into **your** website with LunaPassport.

LunaPassport still emulates classic .NET Passport SSI 1.4 for Windows XP.
On top of that it exposes a small partner surface:

| Path | When to use |
| --- | --- |
| **OAuth 2.0 Authorization Code** | Any modern site on any domain |
| **Classic Passport partner** | Sites that share `PASSPORT_COOKIE_DOMAIN` with LunaPassport |

This is a lab IdP. Tokens are not accepted by real Microsoft services.

## Choose a path

```mermaid
flowchart TD
  start[Need_sign_in_on_my_site]
  start --> q{Same_parent_cookie_domain?}
  q -->|yes_optional| classic[Classic_MSPAuth_cookies]
  q -->|no_or_any_domain| oauth[OAuth_Authorization_Code]
  classic --> partners[/partners_register_return_URL]
  oauth --> partners2[/partners_register_redirect_URI]
```

- **Different domain** (`shop.example.com` vs `passport.lab`) → **OAuth only**.
- **Same parent domain** (`app.lunastore.app` + `passport-staging.lunastore.app` with cookie domain `.lunastore.app`) → classic cookies **or** OAuth.

## Register an application

1. Start LunaPassport and open `/partners`.
2. Sign in with a Passport account (seeded lab user: `test@example.com` / `testpass`).
3. Create an application:
   - **Name** — shown on the consent screen
   - **Redirect URIs** — one exact URI per line
   - **Classic partner** — check if you need `MSPAuth` redirects on the shared cookie domain
4. Copy `client_id` and `client_secret` immediately. The secret is shown once (rotate later from the same page).

Secrets are stored hashed with `OAUTH_SECRET_PEPPER` / `-oauth-secret-pepper`.
Only the account that created an application can list, rotate, disable, or delete it.

## Compatibility (OAuth 2.0 vs OIDC)

LunaPassport is a **confidential-client OAuth 2.0 Authorization Code** IdP.
It is **not** a full OpenID Connect provider.

| Feature | Supported |
| --- | --- |
| Authorization Code (`response_type=code`) | Yes |
| Client secret (form body or HTTP Basic) | Yes |
| Exact `redirect_uri` allowlist | Yes |
| Opaque Bearer access token | Yes |
| `/oauth/userinfo` (`sub`, `sign_in`, `passport_name`) | Yes |
| Token revoke | Yes |
| `state` (validated by **your** site) | Pass-through |
| OIDC discovery (`.well-known/openid-configuration`) | No |
| `id_token` / JWKS | No |
| PKCE | No (not needed for Django + secret) |
| Refresh tokens / scopes | No |

**Django:** use Authlib / `requests-oauthlib`, or a custom django-allauth
`OAuth2Provider` with manual authorize/token/userinfo URLs. Auto-OIDC discovery
(as with Google) will not work.

## OAuth 2.0 (any website)

### Flow

```mermaid
sequenceDiagram
  participant Browser
  participant YourSite
  participant LunaPassport

  Browser->>YourSite: Click_Sign_in_with_Passport
  YourSite->>Browser: Redirect_/oauth/authorize
  Browser->>LunaPassport: Login_and_Allow
  LunaPassport->>Browser: Redirect_callback?code_and_state
  Browser->>YourSite: GET_callback
  YourSite->>LunaPassport: POST_/oauth/token
  LunaPassport->>YourSite: access_token
  YourSite->>LunaPassport: GET_/oauth/userinfo
  LunaPassport->>YourSite: sign_in_and_passport_name
```

### Endpoints

| Method | Path | Role |
| --- | --- | --- |
| `GET` | `/oauth/authorize` | Start login; query: `response_type=code`, `client_id`, `redirect_uri`, `state` |
| `GET`/`POST` | `/oauth/login` | HTML email/password form (also used by `/partners`) |
| `POST` | `/oauth/authorize/consent` | Allow / Deny → redirect with `code` or `error` |
| `POST` | `/oauth/token` | Exchange code for `access_token` |
| `GET` | `/oauth/userinfo` | Bearer token → profile JSON |
| `POST` | `/oauth/revoke` | Invalidate an access token |

### Authorize

```text
GET https://PASSPORT_HOST/oauth/authorize
  ?response_type=code
  &client_id=CLIENT_ID
  &redirect_uri=https://yoursite.example/callback
  &state=RANDOM_CSRF_VALUE
```

- If the browser already has a valid `PPAuth` cookie, LunaPassport skips the password form (SSO) and shows consent.
- `redirect_uri` must match a registered URI **exactly** (scheme, host, path, no extra slash games).

Success redirect:

```text
https://yoursite.example/callback?code=...&state=...
```

Denial / error redirect:

```text
https://yoursite.example/callback?error=access_denied&error_description=...&state=...
```

### Token exchange

```http
POST /oauth/token
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code
&code=AUTH_CODE
&redirect_uri=https://yoursite.example/callback
&client_id=CLIENT_ID
&client_secret=CLIENT_SECRET
```

Client credentials may also be sent via HTTP Basic (`client_id:client_secret`).

Response:

```json
{
  "access_token": "...",
  "token_type": "Bearer",
  "expires_in": 3600
}
```

Authorization codes are single-use and expire in about five minutes. Access tokens expire in about one hour.

### Userinfo

```http
GET /oauth/userinfo
Authorization: Bearer ACCESS_TOKEN
```

```json
{
  "sub": "test@example.com",
  "sign_in": "test@example.com",
  "passport_name": "Test Passport"
}
```

### Revoke

```http
POST /oauth/revoke
Content-Type: application/x-www-form-urlencoded

token=ACCESS_TOKEN
```

### Django (Authlib) — recommended for modern sites

Register this exact redirect URI on `/partners`:

```text
https://yourdjango.example/accounts/passport/callback/
```

Install Authlib and wire two views (sketch):

```python
# pip install Authlib requests
import secrets
from authlib.integrations.requests_client import OAuth2Session
from django.conf import settings
from django.contrib.auth import get_user_model, login
from django.http import HttpResponseBadRequest
from django.shortcuts import redirect

PASSPORT = settings.LUNAPASSPORT_BASE  # e.g. https://passport-staging.alexsyw.me
CLIENT_ID = settings.LUNAPASSPORT_CLIENT_ID
CLIENT_SECRET = settings.LUNAPASSPORT_CLIENT_SECRET
REDIRECT_URI = settings.LUNAPASSPORT_REDIRECT_URI
AUTHORIZE_URL = f"{PASSPORT}/oauth/authorize"
TOKEN_URL = f"{PASSPORT}/oauth/token"
USERINFO_URL = f"{PASSPORT}/oauth/userinfo"


def passport_login(request):
    state = secrets.token_urlsafe(24)
    request.session["oauth_state"] = state
    client = OAuth2Session(CLIENT_ID, CLIENT_SECRET, redirect_uri=REDIRECT_URI, state=state)
    uri, _ = client.create_authorization_url(AUTHORIZE_URL)
    return redirect(uri)


def passport_callback(request):
    if request.GET.get("error"):
        return HttpResponseBadRequest(request.GET.get("error_description", "oauth error"))
    if request.GET.get("state") != request.session.get("oauth_state"):
        return HttpResponseBadRequest("state mismatch")

    client = OAuth2Session(CLIENT_ID, CLIENT_SECRET, redirect_uri=REDIRECT_URI)
    token = client.fetch_token(
        TOKEN_URL,
        authorization_response=request.build_absolute_uri(),
        client_secret=CLIENT_SECRET,
    )
    resp = client.get(USERINFO_URL)
    resp.raise_for_status()
    profile = resp.json()  # sub / sign_in / passport_name

    User = get_user_model()
    user, _ = User.objects.get_or_create(
        username=profile["sign_in"],
        defaults={"email": profile["sign_in"]},
    )
    login(request, user)
    return redirect("/")
```

Keep `client_secret` only in Django settings / env — never in browser JS.

**django-allauth:** add a custom `OAuth2Provider` with
`authorize_url`, `access_token_url`, and `profile_url` pointing at the three
endpoints above; map `sub` / `sign_in` to the local user. Do not enable OIDC
auto-discovery for LunaPassport.

### Security notes (partner sites)

- Always generate and verify `state` in your app session (CSRF on the OAuth round-trip).
- Register the redirect URI **exactly** (scheme, host, path, trailing slash).
- Treat LunaPassport as a **lab IdP** — isolate the network; do not reuse tokens with real Microsoft services.
- Store `client_secret` server-side only.
- IdP login/consent/`/partners` forms use a same-site CSRF cookie (`LPCsrf`); your Django site still needs its own CSRF/`state` handling.

### Minimal Go callback sample

```go
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

const (
	passportBase = "https://passport-staging.lunastore.app"
	clientID     = "YOUR_CLIENT_ID"
	clientSecret = "YOUR_CLIENT_SECRET"
	redirectURI  = "https://yoursite.example/callback"
)

func handleLogin(w http.ResponseWriter, r *http.Request) {
	state := "replace-with-csrf-token"
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	http.Redirect(w, r, passportBase+"/oauth/authorize?"+q.Encode(), http.StatusFound)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("error") != "" {
		http.Error(w, r.URL.Query().Get("error_description"), http.StatusBadRequest)
		return
	}
	// Compare r.URL.Query().Get("state") to the value you stored in the session.

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", r.URL.Query().Get("code"))
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	tokenResp, err := http.PostForm(passportBase+"/oauth/token", form)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer tokenResp.Body.Close()
	var token struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.NewDecoder(tokenResp.Body).Decode(&token)

	req, _ := http.NewRequest(http.MethodGet, passportBase+"/oauth/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	infoResp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer infoResp.Body.Close()
	body, _ := io.ReadAll(infoResp.Body)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func main() {
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/callback", handleCallback)
	_ = http.ListenAndServe(":3000", nil)
}
```

Register `https://yoursite.example/callback` on `/partners` before testing.

## Classic Passport partner (shared cookie domain)

### Requirements

- Your site host is under the same parent as `PASSPORT_COOKIE_DOMAIN` (example: `.lunastore.app`).
- The return URL is registered on `/partners` with **classic Passport partner** enabled.

Cookies `MSPAuth` / `MSPProf` / `PPAuth` are scoped to that parent domain. They will **not** appear on unrelated domains — use OAuth there.

### Browser flow

1. Send the user to:

```text
GET https://PASSPORT_HOST/login2.srf?browser=1&ru=https://app.lunastore.app/passport/return
```

2. After login (HTML path / existing `PPAuth`), LunaPassport:
   - sets `PPAuth` and partner cookies
   - redirects to `ru` **only if** it is allowlisted

Alternative after an existing browser session:

```text
GET https://PASSPORT_HOST/partner/complete?ru=https://app.lunastore.app/passport/return
```

### Verify session

```http
GET /partner/verify
Cookie: MSPAuth=TOKEN
```

```json
{"authenticated":true,"sign_in":"test@example.com","passport_name":"Test Passport"}
```

Lab helper that prints the signed-in user as plain text:

```text
GET /partner
```

### WinHTTP / SSI (unchanged)

Windows XP and WinHTTP still use:

```http
Authorization: Passport1.4 sign-in=...&pwd=...&OrgUrl=...
```

Successful SSI responses return `Authentication-Info` with `from-PP` and `ru=` **without** an HTTP redirect. That path is for protocol clients, not modern browsers.

## Cookies and tokens

| Name | Kind | Lifetime (approx.) | Purpose |
| --- | --- | --- | --- |
| `PPAuth` | Cookie | 30 days | Passport session on the shared cookie domain |
| `MSPAuth` | Cookie | session-style partner cookie | Classic partner site auth |
| `MSPProf` | Cookie | partner profile blob | Mock profile (`mock-profile`) |
| OAuth auth code | Server | 5 minutes, one-time | Browser → site handoff |
| OAuth access token | Opaque Bearer | 1 hour | Site → `/oauth/userinfo` |

## Admin URLs

| URL | Purpose |
| --- | --- |
| `/partners` | Create / list / rotate / disable / delete apps |
| `/oauth/login` | Browser sign-in |
| `/ppsecure/MSRV_EditProfile.asp` | Edit Passport profile |
| `/logout` | Clear Passport cookies / session |
| `/static/netpass/index.html` | Lab landing page |

## Troubleshooting

| Symptom | Likely cause |
| --- | --- |
| `redirect_uri is not registered` | URI string differs from `/partners` (http vs https, trailing slash, port) |
| Classic redirect rejected | Classic checkbox off, or URI not allowlisted |
| Cookies missing on partner site | Host not under `PASSPORT_COOKIE_DOMAIN`, or TLS / Secure cookie mismatch |
| Consent skipped password but fails | Stale `PPAuth` — use `/logout` and sign in again |
| `invalid_grant` on token | Code already used, expired, or `redirect_uri` mismatch |
| XP SSI broken after partner changes | Unlikely if you did not change Authorization responses; re-run `go test ./...` |

## Related README sections

- Quick start and Docker: repository [README](../README.md)
- Windows XP hosts / registry: README “Windows XP”
- Russian guide: [partner-sso.ru.md](partner-sso.ru.md)
