package main

import (
	"net/http"
	"strings"
)

const legacyCompatibilityNotice = `<!doctype html>
<html><head><meta charset="utf-8"><title>Legacy browser required</title></head>
<body><h1>Legacy browser required</h1>
<p>This LunaPassport test site works only with Internet Explorer 6 and the Windows XP .NET Passport service.</p>
<p>Please open it from the supported legacy environment.</p></body></html>`

func legacyBrowserOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLunaPassportPage(r.URL.Path) && isModernBrowser(r.UserAgent()) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(legacyCompatibilityNotice))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLegacyInternetExplorer(userAgent string) bool {
	return strings.Contains(userAgent, "MSIE ") || strings.Contains(userAgent, "Trident/")
}

func isModernBrowser(userAgent string) bool {
	if isLegacyInternetExplorer(userAgent) {
		return false
	}
	for _, marker := range []string{"Chrome/", "Chromium/", "Firefox/", "Safari/", "Edg/", "OPR/", "Opera/"} {
		if strings.Contains(userAgent, marker) {
			return true
		}
	}
	return false
}

func isLunaPassportPage(path string) bool {
	switch path {
	case "/", "/netpass/", "/static/netpass/index.html", "/defaultwiz.asp", "/uixpwiz.srf", "/UIXPWiz.srf", "/ppsecure/MSRV_EditProfile.asp", "/partner", "/partners":
		return true
	default:
		return false
	}
}
