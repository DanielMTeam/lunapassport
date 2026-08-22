// LunaPassport is a small, local emulator of the HTTP parts of Microsoft
// Passport SSI 1.4. It is intended for protocol research with legacy clients;
// it does not produce tokens accepted by the real, retired Passport service.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
)

const (
	localPassportEmail    = "test@example.com"
	localPassportPassword = "testpass"
	localPassportName     = "Test Passport"
)

func main() {
	httpAddr := flag.String("http", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "accounts.db", "SQLite database path")
	passportHost := flag.String("passport-host", os.Getenv("PASSPORT_HOST"), "Central Passport host for Nexus, login, and Wizard endpoints")
	memberservicesHost := flag.String("memberservices-host", os.Getenv("MEMBERSERVICES_HOST"), "Passport memberservices host")
	cookieDomain := flag.String("passport-cookie-domain", os.Getenv("PASSPORT_COOKIE_DOMAIN"), "Shared Passport cookie domain, for example .alexsyw.me")
	flag.Parse()

	accounts, err := openAccountStore(*dbPath, passportAccount{
		SignIn:       localPassportEmail,
		PassportName: localPassportName,
		Password:     localPassportPassword,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer accounts.close()

	domains := customPassportDomains(*passportHost, *memberservicesHost, *cookieDomain)
	h := newServerWithPassportDomains(localPassportEmail, localPassportPassword, accounts, domains).routes()
	log.Printf("HTTP listening on %s; TLS is handled by the reverse proxy", *httpAddr)
	if err := http.ListenAndServe(*httpAddr, h); err != nil {
		log.Fatal(err)
	}
}
