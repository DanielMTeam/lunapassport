// LunaPassport is a small, local emulator of the HTTP parts of Microsoft
// Passport SSI 1.4. It is intended for protocol research with legacy clients;
// it does not produce tokens accepted by the real, retired Passport service.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

// version is set at link time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	httpAddr := flag.String("http", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "accounts.db", "SQLite database path")
	passportHost := flag.String("passport-host", os.Getenv("PASSPORT_HOST"), "Central Passport host for Nexus, login, and Wizard endpoints")
	memberservicesHost := flag.String("memberservices-host", os.Getenv("MEMBERSERVICES_HOST"), "Passport memberservices host")
	oauthPepper := flag.String("oauth-secret-pepper", os.Getenv("OAUTH_SECRET_PEPPER"), "Pepper used to hash OAuth client secrets")
	encryptionKey := flag.String("account-encryption-key", os.Getenv("ACCOUNT_ENCRYPTION_KEY"), "Base64 32-byte key for encrypted account fields")
	seedEmail := flag.String("seed-account-email", "", "Test-only initial account email")
	seedPassword := flag.String("seed-account-password", "", "Test-only initial account password")
	seedPassportName := flag.String("seed-account-passport-name", "", "Test-only initial Passport name")
	cookieDomain := flag.String("passport-cookie-domain", os.Getenv("PASSPORT_COOKIE_DOMAIN"), "Shared Passport cookie domain, for example .lunastore.app")
	flag.Parse()

	initial := passportAccount{SignIn: *seedEmail, Password: *seedPassword, PassportName: *seedPassportName}
	var accounts *accountStore
	var err error
	if strings.TrimSpace(*encryptionKey) == "" {
		accounts, err = openAccountStoreWithPepper(*dbPath, initial, *oauthPepper)
	} else {
		accounts, err = openAccountStoreWithEncryption(*dbPath, initial, *oauthPepper, *encryptionKey)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer accounts.close()

	domains := customPassportDomains(*passportHost, *memberservicesHost, *cookieDomain)
	h := newServerWithPassportDomains("", "", accounts, domains).routes()
	printStartupBanner()
	log.Printf("HTTP listening on %s; TLS is handled by the reverse proxy", *httpAddr)
	if err := http.ListenAndServe(*httpAddr, h); err != nil {
		log.Fatal(err)
	}
}

func printStartupBanner() {
	fmt.Printf(`                                                                                                                       
 ▄▄                                      ▄▄▄▄▄▄                                                                         
 ██                                      ██▀▀▀▀█▄                                                                ██     
 ██        ██    ██  ██▄████▄   ▄█████▄  ██    ██   ▄█████▄  ▄▄█████▄  ▄▄█████▄  ██▄███▄    ▄████▄    ██▄████  ███████  
 ██        ██    ██  ██▀   ██   ▀ ▄▄▄██  ██████▀    ▀ ▄▄▄██  ██▄▄▄▄ ▀  ██▄▄▄▄ ▀  ██▀  ▀██  ██▀  ▀██   ██▀        ██     
 ██        ██    ██  ██    ██  ▄██▀▀▀██  ██        ▄██▀▀▀██   ▀▀▀▀██▄   ▀▀▀▀██▄  ██    ██  ██    ██   ██         ██     
 ██▄▄▄▄▄▄  ██▄▄▄███  ██    ██  ██▄▄▄███  ██        ██▄▄▄███  █▄▄▄▄▄██  █▄▄▄▄▄██  ███▄▄██▀  ▀██▄▄██▀   ██         ██▄▄▄  
 ▀▀▀▀▀▀▀▀   ▀▀▀▀ ▀▀  ▀▀    ▀▀   ▀▀▀▀ ▀▀  ▀▀         ▀▀▀▀ ▀▀   ▀▀▀▀▀▀    ▀▀▀▀▀▀   ██ ▀▀▀      ▀▀▀▀     ▀▀          ▀▀▀▀
  %s

`, version)
}
