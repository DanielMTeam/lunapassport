package main

import (
	"io/fs"
	"net/http"
	"strings"
)

func legacyBrowserOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLunaPassportPage(r.URL.Path) && isBrowser(r.UserAgent()) && !isLegacyInternetExplorer(r.UserAgent()) {
			writeLegacyCompatibilityNotice(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeLegacyCompatibilityNotice(w http.ResponseWriter) {
	page, err := fs.ReadFile(staticFiles, "static/legacy-browser.html")
	if err != nil {
		http.Error(w, "cannot read legacy browser notice", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write(page)
}

func isLegacyInternetExplorer(userAgent string) bool {
	return strings.Contains(userAgent, "MSIE ") || strings.Contains(userAgent, "Trident/")
}

func isBrowser(userAgent string) bool {
	return strings.Contains(userAgent, "Mozilla/")
}

func isLunaPassportPage(path string) bool {
	switch path {
	case "/", "/netpass/", "/static/netpass/index.html", "/defaultwiz.asp", "/uixpwiz.srf", "/UIXPWiz.srf", "/ppsecure/MSRV_EditProfile.asp":
		return true
	default:
		return false
	}
}
