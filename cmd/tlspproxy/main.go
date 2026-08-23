// Command tlspproxy terminates legacy TLS (for Windows XP WinHTTP/Wizard)
// and reverse-proxies plain HTTP to LunaPassport.
//
// Requires GODEBUG=tls10server=1,tlsrsakex=1,tls3des=1 (set automatically
// via //go:debug below when built with a supporting toolchain).

//go:debug tls10server=1
//go:debug tlsrsakex=1
//go:debug tls3des=1

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func main() {
	listen := flag.String("listen", ":443", "HTTPS listen address")
	backend := flag.String("backend", "http://127.0.0.1:8080", "LunaPassport HTTP backend")
	certFile := flag.String("cert", "_data/passport-mock.crt", "TLS certificate (PEM, RSA preferred for XP)")
	keyFile := flag.String("key", "_data/passport-mock.key", "TLS private key (PEM)")
	flag.Parse()

	target, err := url.Parse(*backend)
	if err != nil {
		log.Fatalf("backend URL: %v", err)
	}

	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.Fatalf("load cert: %v", err)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) {
			log.Printf("proxy error %s %s: %v", r.Method, r.URL.RequestURI(), e)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS10,
		MaxVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		},
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen %s: %v (need admin for :443 on Windows)", *listen, err)
	}
	tlsLn := tls.NewListener(ln, tlsCfg)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("%s %s host=%s", r.Method, r.URL.RequestURI(), r.Host)
			proxy.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}


	fmt.Printf(`                                                                                                                       
	▄▄                                      ▄▄▄▄▄▄                                                                         
	██                                      ██▀▀▀▀█▄                                                                ██     
	██        ██    ██  ██▄████▄   ▄█████▄  ██    ██   ▄█████▄  ▄▄█████▄  ▄▄█████▄  ██▄███▄    ▄████▄    ██▄████  ███████  
	██        ██    ██  ██▀   ██   ▀ ▄▄▄██  ██████▀    ▀ ▄▄▄██  ██▄▄▄▄ ▀  ██▄▄▄▄ ▀  ██▀  ▀██  ██▀  ▀██   ██▀        ██     
	██        ██    ██  ██    ██  ▄██▀▀▀██  ██        ▄██▀▀▀██   ▀▀▀▀██▄   ▀▀▀▀██▄  ██    ██  ██    ██   ██         ██     
	██▄▄▄▄▄▄  ██▄▄▄███  ██    ██  ██▄▄▄███  ██        ██▄▄▄███  █▄▄▄▄▄██  █▄▄▄▄▄██  ███▄▄██▀  ▀██▄▄██▀   ██         ██▄▄▄  
	▀▀▀▀▀▀▀▀   ▀▀▀▀ ▀▀  ▀▀    ▀▀   ▀▀▀▀ ▀▀  ▀▀         ▀▀▀▀ ▀▀   ▀▀▀▀▀▀    ▀▀▀▀▀▀   ██ ▀▀▀      ▀▀▀▀     ▀▀          ▀▀▀▀
	TLS Proxy listening on https://%s -> %s`, *listen, *backend)
	if err := srv.Serve(tlsLn); err != nil {
		log.Fatal(err)
	}
}
