package server

import (
	"net/http"
	"path"
	"strings"
)

const assetRoutePrefix = "/_trasmetto-assets/"

func (s *Server) assetRoutePrefix() string {
	segment := s.assetSegment
	if segment == "" {
		segment = strings.Trim(assetRoutePrefix, "/")
	}
	if s.publicPath == "" {
		return "/" + segment + "/"
	}
	return "/" + encodedURLPath(s.publicPath) + "/" + segment + "/"
}

func (s *Server) handleStaticAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, s.assetRoutePrefix())
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	if name == "." || name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}

	switch path.Ext(name) {
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
	case ".js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
	case ".woff2":
		w.Header().Set("Content-Type", "font/woff2")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case ".otf":
		w.Header().Set("Content-Type", "font/otf")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("Content-Encoding", "gzip")
		name += ".gz"
	case ".png":
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		http.NotFound(w, r)
		return
	}

	http.ServeFileFS(w, r, s.staticFiles, "static/"+name)
}
