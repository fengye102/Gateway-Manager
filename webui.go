package main

import (
	"embed"
	"path"
	"strings"
)

//go:embed webui/*
var webuiFS embed.FS

func readWebUIAsset(reqPath string) ([]byte, string, bool) {
	rel := strings.TrimPrefix(reqPath, "/ui")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		rel = "index.html"
	}
	clean := path.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, "/") {
		return nil, "", false
	}
	data, err := webuiFS.ReadFile("webui/" + clean)
	if err != nil {
		return nil, "", false
	}
	contentType := "application/octet-stream"
	switch path.Ext(clean) {
	case ".html":
		contentType = "text/html; charset=utf-8"
	case ".css":
		contentType = "text/css; charset=utf-8"
	case ".js":
		contentType = "application/javascript; charset=utf-8"
	case ".svg":
		contentType = "image/svg+xml"
	}
	return data, contentType, true
}
