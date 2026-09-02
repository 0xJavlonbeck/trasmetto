package server

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

func (s *Server) handleFileSet(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/favicon.ico" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	partial := isPartialRequest(r)

	name, ok := s.fileSetName(r)
	if !ok {

		s.renderHiddenNotFound(w)
		return
	}

	if name == "" || name == "." {
		s.renderPage(w, r, http.StatusOK, ".", s.fileEntries, "", "", partial)
		return
	}

	real, found := s.fileSet[name]
	if !found {
		s.renderPageError(w, http.StatusNotFound, ".", "", partial)
		return
	}

	file, err := os.Open(real)
	if err != nil {
		s.renderPageError(w, statusForFileError(err), ".", fileErrorNotice(err), partial)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.renderPageError(w, statusForFileError(err), ".", fileErrorNotice(err), partial)
		return
	}

	clearWriteDeadline(w)
	if r.Method == http.MethodGet {
		logDetailsFrom(r).recordDownload(name)
	}
	w.Header().Set("Content-Disposition", contentDispositionAttachment(name))
	http.ServeContent(s.downloadWriter(w), r, name, info.ModTime(), file)
}

func (s *Server) fileSetName(r *http.Request) (string, bool) {
	path, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/"))
	if err != nil {

		return "", false
	}
	return s.stripPublicPath(path)
}
