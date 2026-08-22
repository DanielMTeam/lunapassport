# LunaPassport

Локальная лаборатория для исследования HTTP-потока Microsoft .NET Passport
SSI 1.4 в Windows XP/Internet Explorer 6. Проект не подключается к реальному
Microsoft Passport: токены и учётные данные действуют только внутри этой
лаборатории.

## Текущее состояние

LunaPassport реализует:

- Nexus endpoint `GET /rdr/pprdr.asp` с динамическим заголовком `PassportURLs`;
- Passport Wizard на `/defaultwiz.asp`, `/uixpwiz.srf` и `/UIXPWiz.srf`;
- SSI login endpoints `/login2.srf` и `/login2.asp`;
- challenge `WWW-Authenticate: Passport1.4` и разбор `Authorization`;
- локальные Passport-токены, `PPAuth`, `MSPAuth` и `MSPProf` cookies;
- защищённую страницу `/ppsecure/MSRV_EditProfile.asp`;
- изменение email, имени, пароля, секретного вопроса и ответа;
- logout и страницу отмены входа с возможностью повторить авторизацию;
- `/partner` для проверки созданной локальной сессии;
- SQLite-хранилище через GORM с автоматической миграцией схемы.

Go-сервис слушает только HTTP. TLS завершается на Traefik или Nginx, поэтому
сертификаты не генерируются и не загружаются LunaPassport.

## Структура проекта

- `cmd/lunapassport/main.go` — запуск HTTP-сервера и конфигурация;
- `cmd/lunapassport/server.go` — маршруты, состояние сессий и healthcheck;
- `cmd/lunapassport/passport.go` — Nexus, SSI challenge, токены и cookies;
- `cmd/lunapassport/wizard.go` — Wizard, профиль аккаунта и обработка настроек;
- `cmd/lunapassport/accounts.go` — модели GORM, SQLite и миграции;
- `cmd/lunapassport/static/` — Wizard и LunaPassport HTML/CSS/изображения;
- `tests/lunapassport_test.go` — black-box тест, который собирает и запускает
  сервер как отдельный процесс.

## Локальный запуск

Требуется современный Go и Docker не нужен:

```powershell
go run .\cmd\lunapassport -http :8080
```

При первом запуске создаётся `accounts.db` с локальной тестовой учёткой:

```text
Email:    test@example.com
Password: testpass
Name:     Test Passport
```

Файл базы можно изменить через `-db`:

```powershell
go run .\cmd\lunapassport -http :8080 -db .\accounts.db
```

Настройки аккаунта редактируются после входа на странице:

```text
/ppsecure/MSRV_EditProfile.asp
```

Секретный вопрос и ответ сохраняются в локальной базе, но отдельный сценарий
восстановления пароля через них пока не реализован.

## Конфигурация доменов

Для Docker Compose по умолчанию используются staging-домены проекта. При
необходимости скопируй `.env.example` в `.env` и укажи свои значения:

```text
PASSPORT_HOST=passport-staging.alexsyw.me
MEMBERSERVICES_HOST=memberservices-staging.alexsyw.me
PASSPORT_COOKIE_DOMAIN=.alexsyw.me
```

`PASSPORT_HOST` используется для Nexus, login, регистрации, redirect и Wizard.
`MEMBERSERVICES_HOST` используется для профиля и help-страницы.
`PASSPORT_COOKIE_DOMAIN` должен быть общим родительским доменом обоих хостов,
иначе cookies не будут передаваться между ними.

При запуске Go напрямую без флагов остаются исторические значения
`*.passport.com`; для такого запуска передай флаги ниже или задай переменные
окружения.

Доступные флаги сервера:

```text
-http                    HTTP listen address, по умолчанию :8080
-db                      путь к SQLite-файлу, по умолчанию accounts.db
-passport-host           центральный Passport host
-memberservices-host     host профиля и help-страницы
-passport-cookie-domain  общий домен cookies
```

## Docker Compose и внешний Traefik

Compose запускает только Go-сервис по HTTP на внутреннем порту `8080`.
Traefik должен быть запущен отдельно и подключён к внешней Docker-сети
`traefik`:

```powershell
docker network create traefik
docker compose up --build -d
docker compose ps
```

Сеть `traefik` должна существовать до запуска Compose. Внешний Traefik должен
иметь Docker provider, entrypoints `web` и `websecure`, а также сам загружать
TLS-сертификаты и `traefik/dynamic.yml`. Этот Compose не публикует порты и не
запускает второй экземпляр Traefik.

База и состояние аккаунта лежат в `_data/accounts.db`. Сертификат и ключ
должны соответствовать путям из `traefik/dynamic.yml`:

```text
_data/passport-mock.crt
_data/passport-mock.key
```

Сертификат должен содержать все домены, через которые будет обращаться XP.
Traefik маршрутизирует изолированный lab-сервис по правилу `PathPrefix(/)`, поэтому
имя хоста определяется hosts-файлом и сертификатом.

Остановить окружение:

```powershell
docker compose down
```

## Windows XP

Для staging-конфигурации добавь в `hosts` XP:

```text
192.168.67.1 passport-staging.alexsyw.me memberservices-staging.alexsyw.me
```

IP замени на адрес машины с Traefik. Сертификат подписывающего CA нужно один
раз импортировать в Trusted Root Certification Authorities.

Для WinHTTP Passport Test можно импортировать:

```text
passport-test.reg
```

Файл настраивает готовые Passport URL в `Internet Settings\Passport`, которые
читает сам Wizard. Поэтому
на чистой системе `RegistrationUrl`, `LoginServerUrl`, `Properties`, `Help`,
`Privacy` и `GeneralRedir` сразу указывают на staging-домены.
Для уже использовавшейся XP старые Passport URL могут оставаться в кэше;
перезапуск приложения или чистый профиль нужен только для сброса этого кэша.
Для других доменов используй `passport-test.reg.example` и замени значения хостов.

Если используются исторические домены по умолчанию:

```text
192.168.67.1 nexus.passport.com login.passport.com register.passport.com
192.168.67.1 memberservices.passport.com www.passport.com nexusrdr.passport.com
```

## Проверка

Полный набор тестов:

```powershell
go test -count=1 ./...
```

Сборка:

```powershell
go build ./cmd/lunapassport
```

Простой HTTP smoke test:

```powershell
curl.exe -i http://127.0.0.1:8080/healthz
curl.exe -i http://127.0.0.1:8080/rdr/pprdr.asp
curl.exe -i http://127.0.0.1:8080/login2.srf
curl.exe -i -H "Authorization: Passport1.4 sign-in=test%40example.com,pwd=testpass" http://127.0.0.1:8080/login2.srf
```

Для XP-теста полезно зафиксировать последовательность запросов:
`/rdr/pprdr.asp`, затем `/login2.srf`, затем запрос с
`Authorization: Passport1.4`.

## Ограничения и безопасность

Проект предназначен только для изолированной лаборатории. Не публикуй его в
интернете, не используй реальные пароли и не рассчитывай на эти mock-токены
для доступа к настоящим сервисам.
