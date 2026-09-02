package server

import (
	"errors"
	"net/http"
	"os"
)

func (s *Server) handlePath(w http.ResponseWriter, r *http.Request) {

	if r.URL.Path == "/favicon.ico" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Method == http.MethodPost {
			s.handleUpload(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	partial := isPartialRequest(r)

	current, rel, err := s.resolveURLPath(r)
	if err != nil {
		if errors.Is(err, errOutsidePublicPath) {
			s.renderHiddenNotFound(w)
			return
		}
		if os.IsNotExist(err) {
			s.renderPageError(w, http.StatusNotFound, ".", "", partial)
			return
		}
		s.renderPageError(w, http.StatusBadRequest, ".", "Invalid path", partial)
		return
	}

	if _, err := os.Stat(current); err != nil {
		if os.IsNotExist(err) {
			s.renderPageError(w, http.StatusNotFound, rel, "", partial)
			return
		}
		s.renderPageError(w, statusForFileError(err), rel, "Invalid path", partial)
		return
	}

	realCurrent, err := s.realPath(current)
	if err != nil {
		s.renderPageError(w, statusForFileError(err), rel, "Invalid path", partial)
		return
	}

	info, err := os.Stat(realCurrent)
	if err != nil {
		s.renderPageError(w, statusForFileError(err), rel, "", partial)
		return
	}

	if !info.IsDir() {
		if s.uploadOnly {
			http.Error(w, "downloads are disabled", http.StatusForbidden)
			return
		}
		// Opening a FIFO blocks until a writer appears, and sockets and devices
		// are not transferable content, so refuse anything but a regular file.
		if !info.Mode().IsRegular() {
			s.renderPageError(w, http.StatusForbidden, rel, "This is not a regular file", partial)
			return
		}
		file, err := os.Open(realCurrent)
		if err != nil {
			s.renderPageError(w, statusForFileError(err), rel, fileErrorNotice(err), partial)
			return
		}
		defer file.Close()

		clearWriteDeadline(w)
		if r.Method == http.MethodGet {
			logDetailsFrom(r).recordDownload(relFromAbs(realCurrent, s.root))
		}
		w.Header().Set("Content-Disposition", contentDispositionAttachment(info.Name()))
		http.ServeContent(s.downloadWriter(w), r, info.Name(), info.ModTime(), file)
		return
	}

	entries, err := readEntries(realCurrent, s.root)
	if err != nil {
		s.renderPageError(w, statusForFileError(err), rel, "", partial)
		return
	}

	uploadNotice := ""
	if !s.downloadOnly {
		if err := checkWritableDir(realCurrent); err != nil {
			uploadNotice = "No WRITE permissions in this directory"
		}
	}

	s.renderPage(w, r, http.StatusOK, rel, entries, "", uploadNotice, partial)
}

// fileErrorNotice keeps the wording about the file, not the directory it sits in.
func fileErrorNotice(err error) string {
	if os.IsPermission(err) {
		return "No READ permissions for this file"
	}
	return ""
}

func statusForFileError(err error) int {
	if os.IsPermission(err) {
		return http.StatusForbidden
	}
	if os.IsNotExist(err) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}
