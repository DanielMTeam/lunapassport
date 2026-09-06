# Repository Guidelines

## Project Overview

LunaPassport is a Go laboratory for the Windows XP .NET Passport SSI 1.4
protocol. It emulates the retired Passport endpoints and browser/Wizard flow
for isolated testing. It does not issue tokens accepted by real Microsoft
services.

The Go service is HTTP-only. Traefik or Nginx terminates TLS in front of it.

## Project Structure

- `cmd/lunapassport/` — LunaPassport HTTP emulator, Wizard handlers, account
  settings, embedded static assets, and GORM persistence.
- `cmd/lunapassport/accounts.go` — GORM models, SQLite initialization,
  migrations, account CRUD, and Passport session persistence.
- `tests/lunapassport_test.go` — black-box integration test that builds and
  exercises the server as a real process.
- `docker-compose.yml` — LunaPassport service for an external Traefik instance;
  LunaPassport listens on port 8080 inside the external `traefik` network.
- `docker-compose.production.yml` — bundled Traefik + LunaPassport lab stack
  with Let's Encrypt and IE6 TLS support. See `docs/traefik-runbook.md`.
- `dynamic/tls.yml` — Traefik dynamic TLS options for Windows XP / IE6.
- `.env.example` — custom Passport and memberservices host configuration.
- `tools/passport-test.reg` — ready-to-import WinXP WinHTTP Passport Test registry
  configuration for the staging login host.
- `tools/passport-test.reg.example` — editable registry template for another login
  host.

Generated binaries, certificates, keys, databases, packet captures, and logs
belong to local experiments and must not be committed.

## Build, Test, and Run

```powershell
go test -count=1 ./...
go build ./cmd/lunapassport
go run .\cmd\lunapassport -http :8080
```

The application creates a local SQLite database on first start and seeds the
test account `test@example.com` / `testpass`. The database path is controlled
by `-db`; account profile fields can be changed through
`/ppsecure/MSRV_EditProfile.asp` after authentication.

For the full reverse-proxy environment, use the production Compose stack:

```powershell
docker network create traefik
Copy-Item .env.example .env
docker compose -f docker-compose.production.yml build --pull
docker compose -f docker-compose.production.yml up -d
docker compose -f docker-compose.production.yml ps
```

See `docs/traefik-runbook.md` for DNS, ACME, and IE6 TLS details. For an
external Traefik instance instead, use `docker-compose.yml` after Traefik is
already running on the `traefik` network:

The Compose service mounts `_data` at `/data`, stores the database at
`/data/accounts.db`, and passes custom-domain values from `.env` to the Go
process. TLS and certificate files belong to the external Traefik deployment,
not this repository.

## Coding Style and Naming

Use standard Go formatting and idioms:

- run `gofmt -w` on changed Go files;
- use GoDoc for exported identifiers;
- keep handlers small and move persistence into `accountStore` methods;
- use clear lowerCamelCase names for internal helpers;
- preserve legacy protocol spellings and paths such as `PassportURLs`,
  `login2.asp`, `login2.srf`, `dastatus`, and `MSRV_EditProfile.asp`.

Database code uses GORM models and methods. Do not add hand-written SQL for
ordinary CRUD or schema changes. Prefer `AutoMigrate`, model conditions,
transactions, and explicit repository methods. Keep legacy SQLite column names
in GORM tags when compatibility requires them, and document any unavoidable
raw SQL used for protocol or migration compatibility.

Keep domain configuration data-driven. Do not add new hardcoded custom hosts to
handlers or HTML; use `PASSPORT_HOST`, `MEMBERSERVICES_HOST`,
`PASSPORT_COOKIE_DOMAIN`, or the corresponding flags.

## Testing Guidelines

Tests use Go's built-in `testing` package. The integration suite covers:

- Nexus `PassportURLs` headers;
- SSI challenges, Authorization, tokens, and cookies;
- logout and cancelled-login behavior;
- GORM/SQLite account persistence and profile updates;
- language selection and Wizard output;
- custom domain URL generation.

Name tests `Test<Behavior>`. Run `go test -count=1 ./...` before handing off
changes. Manually verify Windows XP after TLS, Passport challenge, registry,
cookie, or Wizard HTML changes.

## Git and Deployment

Use short imperative commit messages, for example:

```text
feat: preserve Passport session after profile update
fix: keep XP Passport challenge spelling
refactor: use GORM for Passport storage
```

The deployment target is the isolated server project at
`/home/alexsyw/infra/passport`. After server-side deployment, verify
`docker compose ps`, `/healthz`, and at least one Passport challenge/login
request. Never commit real credentials, private keys, certificates, or
runtime databases.

## Security and Scope

Use this project only with test accounts, local hosts mappings, and an isolated
network. Do not expose the mock publicly or reuse its tokens in production.

Passwords and secret answers are local lab data, not production-grade account
storage. The secret-question fields are currently editable and persisted, but
there is no password-recovery flow yet.

## Reference Documentation

Partner website integration (OAuth and classic Passport partner):

- [docs/partner-sso.md](docs/partner-sso.md) (English)
- [docs/partner-sso.ru.md](docs/partner-sso.ru.md) (Русский)

Traefik production deployment:

- [docs/traefik-runbook.md](docs/traefik-runbook.md)

The protocol and Windows integration are based on these Microsoft references:

- [Passport Authentication in WinHTTP](https://learn.microsoft.com/en-us/windows/win32/winhttp/passport-authentication-in-winhttp) — Nexus configuration, login flow, and XP credential storage.
- [MS-PASS: Authentication Server Challenge](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-pass/a059aaaf-2d4a-40c6-ad96-7175c379ffd7) — `WWW-Authenticate: Passport1.4` challenge syntax.
- [MS-PASS: Protocol Examples](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-pass/2c80637d-438c-4d4b-adc5-903170a779f3) — request, challenge, token, and cookie exchanges.
- [NewWDEvents.PassportAuthenticate](https://learn.microsoft.com/en-us/windows/win32/shell/inewwdevents-passportauthenticate) — XP Wizard authentication callback and return value.
- [WebWizardHost](https://learn.microsoft.com/en-us/windows/win32/shell/webwizardhost) — `FinalNext`, `FinalBack`, `Cancel`, and Wizard page integration.
