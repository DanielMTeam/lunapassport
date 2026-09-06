# LunaPassport behind Traefik

This is a small **lab-only** deployment guide for LunaPassport behind Traefik. Keep the service private: it emulates an authentication flow and should be reachable only from explicitly approved test IPs. Do not publish the app port or the Traefik dashboard.

The example names below are placeholders:

- `passport.lunastore.app`
- `memberservices.lunastore.app`

Replace them consistently with the two test hostnames for your environment.

## What is required

Run the app in Docker and place it on the same external Docker network as Traefik (for example, `traefik`). The app should listen only inside Docker on port `8080`; Traefik is the only component exposing ports `80` and `443`.

Use `mirror.gcr.io` for all proxy and Dockerfile base images. With Docker Engine 29, use Traefik **v3.6.6 or newer**. Older builds can fail to discover containers with this error:

```text
client version 1.24 is too old
```

Before starting the app, create DNS `A` records for both hosts pointing to the VM. Traefik cannot issue a Let’s Encrypt certificate until DNS resolves to that server.

```dns
passport.lunastore.app.       A  <server-public-ip>
memberservices.lunastore.app. A  <server-public-ip>
```

## Traefik essentials

Configure the Docker provider with `exposedByDefault: false`, connect Traefik to the shared `traefik` network, and configure an ACME resolver. Store its state in a persistent `acme.json` file with mode `600`.

The application requires two HTTPS routers, one for each hostname. Each router must use the Let’s Encrypt resolver and the same IP allowlist middleware. The service target remains internal:

```yaml
labels:
  traefik.enable: "true"
  traefik.docker.network: traefik
  traefik.http.middlewares.app-allowlist.ipallowlist.sourcerange: >-
    <test-ip-1>/32,<test-ip-2>/32

  traefik.http.routers.passport.rule: Host(`passport.lunastore.app`)
  traefik.http.routers.passport.entrypoints: websecure
  traefik.http.routers.passport.tls: "true"
  traefik.http.routers.passport.tls.certresolver: letsencrypt
  traefik.http.routers.passport.middlewares: app-allowlist

  traefik.http.routers.memberservices.rule: Host(`memberservices.lunastore.app`)
  traefik.http.routers.memberservices.entrypoints: websecure
  traefik.http.routers.memberservices.tls: "true"
  traefik.http.routers.memberservices.tls.certresolver: letsencrypt
  traefik.http.routers.memberservices.middlewares: app-allowlist

  traefik.http.services.app.loadbalancer.server.port: "8080"
```

Never add a host `ports:` mapping for `8080`. A request outside the allowlist should return `403` at Traefik and must never reach the application.

## Environment

Keep runtime values in an untracked `.env` file with mode `600`:

```dotenv
PASSPORT_HOST=passport.lunastore.app
MEMBERSERVICES_HOST=memberservices.lunastore.app
PASSPORT_COOKIE_DOMAIN=.lunastore.app
OAUTH_SECRET_PEPPER=<long-random-secret>
```

Build and start using a host-local production Compose file:

```bash
docker compose -f docker-compose.production.yml build --pull
docker compose -f docker-compose.production.yml up -d
```

## Legacy IE6 / Windows XP support

This is intentionally weak crypto. Enable it only on an IP-restricted compatibility lab, preferably on a dedicated Traefik instance or IP.

IE6 uses TLS 1.0-era crypto and does not send SNI. Modern Traefik runs on Go, which defaults to TLS 1.2. Set this environment variable on the Traefik container:

```yaml
environment:
  GODEBUG: tls10server=1,tls3des=1,tlsrsakex=1
```

Then configure the **default** TLS option in Traefik’s dynamic file provider:

```yaml
tls:
  options:
    default:
      minVersion: VersionTLS10
      cipherSuites:
        - TLS_RSA_WITH_AES_128_CBC_SHA
        - TLS_RSA_WITH_AES_256_CBC_SHA
        - TLS_RSA_WITH_3DES_EDE_CBC_SHA
        - TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA
        - TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA
  stores:
    default:
      defaultGeneratedCert:
        resolver: letsencrypt
        domain:
          main: passport.lunastore.app
          sans:
            - memberservices.lunastore.app
```

The default certificate needs both names because IE6 has no SNI. SNI-capable clients may still receive individual certificates for each router.

Do **not** set a separate `tls.options` value on an app router. On a no-SNI request Traefik uses `default` for the handshake; a different router option makes Traefik respond with `421 Misdirected Request`.

## Verify

Modern clients:

```bash
curl -fsS https://passport.lunastore.app/healthz
curl -fsS https://memberservices.lunastore.app/healthz
```

Legacy-style test (TLS 1.0, no SNI):

```bash
printf 'GET /healthz HTTP/1.0\r\nHost: memberservices.lunastore.app\r\n\r\n' \
  | openssl s_client -quiet -tls1 -cipher 'AES128-SHA:@SECLEVEL=0' \
      -connect memberservices.lunastore.app:443 -noservername
```

A successful test returns `HTTP/1.0 200 OK` and `ok`.

## Windows XP

Add to the XP `hosts` file:

```text
<server-ip> passport-staging.pidoras.top memberservices-staging.pidoras.top
```

Import the Let's Encrypt CA into Trusted Root Certification Authorities, then import [`tools/passport-test.reg`](../tools/passport-test.reg).

## Fast troubleshooting

- **ACME/DNS error:** point both `A` records at the VM and wait for propagation.
- **`client version 1.24 is too old`:** upgrade Traefik to v3.6.6+.
- **IE6 never reaches HTTP logs:** TLS negotiation failed; check `GODEBUG`, `VersionTLS10`, and the cipher list.
- **`421 Misdirected Request`:** remove per-router `tls.options`; use only `tls.options.default` for this legacy setup.
- **`403 Forbidden`:** add the current tester’s exact public IP as `/32` to the allowlist, redeploy the app, and keep the allowlist narrow.
