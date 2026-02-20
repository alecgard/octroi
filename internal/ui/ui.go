package ui

import (
	"embed"
	"net/http"
	"os"
)

//go:embed index.html
var content embed.FS

// Handler returns an http.Handler that serves the admin UI.
// If OCTROI_DEV=1 is set, it reads index.html from disk on each request
// for live reloading. Otherwise it serves the embedded copy.
func Handler() http.Handler {
	if os.Getenv("OCTROI_DEV") == "1" {
		return devHandler()
	}
	return embeddedHandler()
}

func embeddedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := content.ReadFile("index.html")
		if err != nil {
			http.Error(w, "ui not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
}

func devHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile("internal/ui/index.html")
		if err != nil {
			http.Error(w, "ui not found: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(data)
	})
}
