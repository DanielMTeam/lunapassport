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
	if !strings.Contains(response.Body.String(), "Internet Explorer 6") || !strings.Contains(response.Body.String(), ".NET Passport") {
		t.Fatalf("modern browser must receive the legacy compatibility notice: %q", response.Body.String())
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
