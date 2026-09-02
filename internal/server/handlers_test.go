package server

import (
	"bytes"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, root, publicPath string) *Server {
	t.Helper()
	tmpl := template.Must(template.New("index.html").Parse(
		`<!doctype html><head></head><body>{{template "content" .}}</body>` +
			`{{define "content"}}<div class="listing">{{range .Entries}}<a>{{.DisplayName}}</a>{{end}}</div>{{end}}`,
	))
	return &Server{
		root:           root,
		rootDisplay:    root,
		publicPath:     publicPath,
		logger:         log.New(io.Discard, "", 0),
		pageTemplate:   tmpl,
		assetSegment:   freeRouteSegment(root, strings.Trim(assetRoutePrefix, "/")),
		archiveSegment: freeRouteSegment(root, strings.Trim(archiveRoutePrefix, "/")),
	}
}

func TestHandlePathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("ok"), 0600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	s := newTestServer(t, root, "")

	req := httptest.NewRequest(http.MethodGet, "/%2e%2e/%2e%2e/etc/passwd", nil)
	rec := httptest.NewRecorder()
	s.handlePath(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("traversal request returned 200: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "root:") {
		t.Fatalf("traversal leaked file contents: %s", rec.Body.String())
	}
}

func TestHandlePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET"), 0600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "leak")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatalf("symlink: %v", err)
	}
	s := newTestServer(t, root, "")

	req := httptest.NewRequest(http.MethodGet, "/leak", nil)
	rec := httptest.NewRecorder()
	s.handlePath(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("symlink escape returned 200: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "TOP-SECRET") {
		t.Fatalf("symlink escape leaked secret: %s", rec.Body.String())
	}
}

func TestHandlePathHiddenPath(t *testing.T) {
	root := t.TempDir()
	s := newTestServer(t, root, "secret")

	wrong := httptest.NewRecorder()
	s.handlePath(wrong, httptest.NewRequest(http.MethodGet, "/wrong", nil))
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("wrong path status = %d, want 404", wrong.Code)
	}
	if strings.Contains(wrong.Body.String(), "listing") || strings.Contains(wrong.Body.String(), "Trasmetto") {
		t.Fatalf("wrong path leaked app content: %s", wrong.Body.String())
	}

	right := httptest.NewRecorder()
	s.handlePath(right, httptest.NewRequest(http.MethodGet, "/secret", nil))
	if right.Code != http.StatusOK {
		t.Fatalf("correct path status = %d, want 200: %s", right.Code, right.Body.String())
	}
}

func TestResponsesSetSecurityHeaders(t *testing.T) {
	s := newTestServer(t, t.TempDir(), "")
	handler := s.Routes(log.New(io.Discard, "", 0))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
}

func TestHandlePathFaviconIsCheap(t *testing.T) {
	s := newTestServer(t, t.TempDir(), "")
	rec := httptest.NewRecorder()
	s.handlePath(rec, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("favicon status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("favicon returned a body: %q", rec.Body.String())
	}
}

func TestReservedNamesDoNotShadowRealFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"_trasmetto-archive", "_trasmetto-assets"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("real "+name), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	srv := newTestServer(t, root, "")
	handler := srv.Routes(log.New(io.Discard, "", 0))

	for _, name := range []string{"_trasmetto-archive", "_trasmetto-assets"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/"+name, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("GET /%s = %d, want 200", name, recorder.Code)
		}
		if got := recorder.Body.String(); got != "real "+name {
			t.Errorf("GET /%s body = %q, want the real file", name, got)
		}
	}

	// The archive route steps aside for them, and must keep working there.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, srv.archiveRoutePrefix(), nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("archive route = %d, want 200", recorder.Code)
	}
	if ct := recorder.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("archive Content-Type = %q, want application/zip", ct)
	}
}

func TestRouteStepsAsideForCollidingDirectory(t *testing.T) {
	root := t.TempDir()
	shadow := filepath.Join(root, "_trasmetto-archive")
	if err := os.Mkdir(shadow, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shadow, "x.txt"), []byte("inner"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := newTestServer(t, root, "")
	if got := srv.archiveRoutePrefix(); got != "/_trasmetto-archive-1/" {
		t.Fatalf("archiveRoutePrefix() = %q, want the route to step aside", got)
	}
	handler := srv.Routes(log.New(io.Discard, "", 0))

	// The real directory keeps its own path, contents included.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/_trasmetto-archive/x.txt", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "inner" {
		t.Errorf("shadowed file = %d %q, want 200 \"inner\"", recorder.Code, recorder.Body.String())
	}

	// And zipping still works on the relocated route.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/_trasmetto-archive-1/", nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("relocated archive route = %d, want 200", recorder.Code)
	}
	if ct := recorder.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
}

func TestUnreadableFileDoesNotLookLikeADownload(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked.txt")
	if err := os.WriteFile(locked, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })

	srv := newTestServer(t, root, "")
	var buf bytes.Buffer
	events := NewEventLogger(&buf)
	srv.SetEventLogger(events)
	handler := srv.Routes(log.New(io.Discard, "", 0))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/locked.txt", nil))
	events.Close()

	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", recorder.Code)
	}
	// No attachment header, or the browser saves the error body as the file.
	if got := recorder.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("Content-Disposition = %q, want none on a failed read", got)
	}
	for _, entry := range decodeLines(t, buf.Bytes()) {
		if entry["type"] == "download" {
			t.Errorf("a file that was never read was logged as a download: %v", entry)
		}
	}
}
