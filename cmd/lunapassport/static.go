package main

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static/*
var staticFiles embed.FS

func (s *server) staticHandler() http.Handler {
	root, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(root)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/static/netpass/index.html" || r.URL.Path == "/static/netpass/" {
			s.handleNetpassIndex(w)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *server) handleNetpassIndex(w http.ResponseWriter) {
	data, err := fs.ReadFile(staticFiles, "static/netpass/index.html")
	if err != nil {
		http.Error(w, "cannot read Passport page", http.StatusInternalServerError)
		return
	}
	page := strings.ReplaceAll(string(data), "__MEMBERSERVICES_URL__", s.passportURL(s.domains.memberservicesHost, ""))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, page)
}
