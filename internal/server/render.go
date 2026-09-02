package server

import (
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
)

type pageData struct {
	Title            string
	Root             string
	Current          string
	CurrentQuery     string
	ParentQuery      string
	CanGoUp          bool
	CanUpload        bool
	CanDownload      bool
	CanZip           bool
	CanManage        bool
	FileSetMode      bool
	Entries          []entry
	Message          string
	Error            string
	UploadNotice     string
	MaxUploadBytes   int64
	UploadNeedsLogin bool
}

func (s *Server) renderHiddenNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
  <meta http-equiv="content-type" content="text/html; charset=utf-8">
  <title>Not Found</title>
</head>
<body>
  <h1>Not Found</h1><p>The requested resource was not found on this server.</p>
</body>
</html>
`))
}

// renderPageError shows the listing shell with a notice in place of entries.
// An empty notice falls back to a sensible default for the status.
func (s *Server) renderPageError(w http.ResponseWriter, status int, rel string, notice string, partial bool) {
	if notice == "" {
		switch status {
		case http.StatusForbidden:
			notice = "No READ/WRITE permissions in this directory"
		case http.StatusNotFound:
			notice = "404, Page Not Found"
		}
	}
	s.renderPage(w, nil, status, rel, nil, "", notice, partial)
}

func isPartialRequest(r *http.Request) bool {
	return r.Header.Get("X-Trasmetto-Partial") == "1"
}

func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, status int, rel string, entries []entry, message string, uploadNotice string, partial bool) {
	data := pageData{
		Title:            "Trasmetto",
		Root:             s.rootDisplay,
		Current:          displayPath(rel),
		CurrentQuery:     filepath.ToSlash(rel),
		ParentQuery:      parentPath(rel),
		CanGoUp:          rel != ".",
		CanUpload:        !s.downloadOnly && uploadNotice == "",
		CanDownload:      !s.uploadOnly,
		CanZip:           !s.uploadOnly && !s.noZip,
		CanManage:        s.fullAccess,
		FileSetMode:      s.fileSetMode,
		Entries:          entries,
		Error:            message,
		UploadNotice:     uploadNotice,
		MaxUploadBytes:   s.maxUploadBytes,
		UploadNeedsLogin: s.writeAuthPending(r),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	var err error
	if partial {
		err = s.pageTemplate.ExecuteTemplate(w, "content", data)
	} else {
		err = s.pageTemplate.Execute(w, data)
	}
	if err != nil && !isClientDisconnect(err) {
		s.logDetail(r, "render page failed: %v", err)
	}
}

func isClientDisconnect(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	message := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"broken pipe",
		"connection reset by peer",
		"forcibly closed by the remote host",
		"use of closed network connection",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}
