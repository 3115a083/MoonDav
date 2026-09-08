package moondav

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*
var webAssets embed.FS

func (a *App) uiIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := webAssets.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "ui unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (a *App) uiAssets() http.Handler {
	sub, _ := fs.Sub(webAssets, "web")
	return http.StripPrefix("/assets/", http.FileServer(http.FS(sub)))
}
