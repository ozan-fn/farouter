package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
)

func serveSPA(r chi.Router, embeddedFS embed.FS, targetDir string) {
	contentFS, err := fs.Sub(embeddedFS, targetDir)
	if err != nil {
		log.Fatal(err)
	}

	fileServer := http.FileServer(http.FS(contentFS))

	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		filePath := path.Clean(r.URL.Path)
		filePath = strings.TrimPrefix(filePath, "/")

		if filePath == "" {
			if acceptsBrotli(r) {
				if f, err := contentFS.Open("index.html.br"); err == nil {
					f.Close()
					w.Header().Set("Content-Encoding", "br")
					w.Header().Set("Content-Type", "text/html")
					http.ServeFileFS(w, r, contentFS, "index.html.br")
					return
				}
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		if acceptsBrotli(r) {
			if f, err := contentFS.Open(filePath + ".br"); err == nil {
				f.Close()
				w.Header().Set("Content-Encoding", "br")
				w.Header().Set("Content-Type", mimeTypeByExt(filePath))
				http.ServeFileFS(w, r, contentFS, filePath+".br")
				return
			}
		}

		f, err := contentFS.Open(filePath)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		if acceptsBrotli(r) {
			if f, err := contentFS.Open("index.html.br"); err == nil {
				f.Close()
				w.Header().Set("Content-Encoding", "br")
				w.Header().Set("Content-Type", "text/html")
				http.ServeFileFS(w, r, contentFS, "index.html.br")
				return
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFileFS(w, r, contentFS, "index.html")
	})
}

func mimeTypeByExt(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html"
	case strings.HasSuffix(name, ".css"):
		return "text/css"
	case strings.HasSuffix(name, ".js"):
		return "application/javascript"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".ico"):
		return "image/x-icon"
	default:
		return ""
	}
}

func acceptsBrotli(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept-Encoding") {
		for _, e := range strings.Split(v, ",") {
			if strings.TrimSpace(e) == "br" {
				return true
			}
		}
	}
	return false
}
