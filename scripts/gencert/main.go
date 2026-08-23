// Command gencert writes a lab self-signed RSA certificate for local TLS
// termination (Caddy or tlspproxy). RSA is required for XP-era cipher suites
// such as TLS_RSA_WITH_AES_128_CBC_SHA.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

func main() {
	outDir := flag.String("out", "_data", "output directory for passport-mock.crt/key")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal(err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fatal(err)
	}

	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		fatal(err)
	}

	hosts := []string{
		"passport-staging.alexsyw.me",
		"memberservices-staging.alexsyw.me",
		"login.passport.com",
		"nexus.passport.com",
		"register.passport.com",
		"memberservices.passport.com",
		"www.passport.com",
		"nexusrdr.passport.com",
		"passport.com",
		"localhost",
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"LunaPassport Lab"},
			CommonName:   "LunaPassport local",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              hosts,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		fatal(err)
	}

	certPath := filepath.Join(*outDir, "passport-mock.crt")
	keyPath := filepath.Join(*outDir, "passport-mock.key")

	certFile, err := os.Create(certPath)
	if err != nil {
		fatal(err)
	}
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		_ = certFile.Close()
		fatal(err)
	}
	_ = certFile.Close()

	keyFile, err := os.Create(keyPath)
	if err != nil {
		fatal(err)
	}
	if err := pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		_ = keyFile.Close()
		fatal(err)
	}
	_ = keyFile.Close()

	fmt.Printf("wrote %s\nwrote %s\n", certPath, keyPath)
	fmt.Println("Import passport-mock.crt into Trusted Root on the XP lab machine (and this PC if needed).")
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "gencert: %v\n", err)
	os.Exit(1)
}
