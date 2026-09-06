package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModernBrowserGetsLegacyCompatibilityNotice(t *testing.T) {
	handler := newServer("test@example.com", "testpass").routes()
	request := httptest.NewRequest(http.MethodGet, "/static/netpass/index.html", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120.0 Safari/537.36")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("modern browser status = %d, want %d", response.Code, http.StatusForbidden)
	}
	page := response.Body.String()
	for _, required := range []string{"Internet Explorer", ".NET Passport", "https://lunastore.app", "href=\"/partners\"", "/static/img/iewarn.png"} {
		if !strings.Contains(page, required) {
			t.Fatalf("modern browser notice must include %q: %q", required, page)
		}
	}
}

func TestModernBrowserCanOpenPartners(t *testing.T) {
	handler := newServer("test@example.com", "testpass").routes()
	request := httptest.NewRequest(http.MethodGet, "/partners", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120.0 Safari/537.36")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code == http.StatusForbidden {
		t.Fatal("modern browser must not be blocked from partner SSO")
	}
	if response.Code != http.StatusFound || !strings.HasPrefix(response.Header().Get("Location"), "/oauth/login?return_to=%2Fpartners") {
		t.Fatalf("partners response = %d location=%q, want OAuth login redirect", response.Code, response.Header().Get("Location"))
	}
}

func TestNonIEBrowserGetsLegacyCompatibilityNotice(t *testing.T) {
	handler := newServer("test@example.com", "testpass").routes()
	request := httptest.NewRequest(http.MethodGet, "/static/netpass/index.html", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ExperimentalBrowser/1.0)")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("non-IE browser status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestIE6CanOpenLunaPassportHome(t *testing.T) {
	handler := newServer("test@example.com", "testpass").routes()
	request := httptest.NewRequest(http.MethodGet, "/static/netpass/index.html", nil)
	request.Header.Set("User-Agent", "Mozilla/4.0 (compatible; MSIE 6.0; Windows NT 5.1; SV1)")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("IE6 status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestNonBrowserClientCanReachLunaPassportHome(t *testing.T) {
	handler := newServer("test@example.com", "testpass").routes()
	request := httptest.NewRequest(http.MethodGet, "/static/netpass/index.html", nil)
	request.Header.Set("User-Agent", "Go-http-client/1.1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("non-browser status = %d, want %d", response.Code, http.StatusOK)
	}
}
