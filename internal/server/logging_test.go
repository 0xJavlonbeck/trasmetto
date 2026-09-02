package server

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRequestLoggerFormat(t *testing.T) {
	var out bytes.Buffer
	logger := log.New(&out, "", 0)
	handler := requestLogger(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}), logRoutes{assetPrefix: "/_trasmetto-assets/", archivePrefix: "/_trasmetto-archive/"}, false, nil)

	request := httptest.NewRequest(http.MethodGet, "/home/kalifdsa", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	handler.ServeHTTP(httptest.NewRecorder(), request)

	pattern := regexp.MustCompile(`^\[\d{2}/\d{2}/\d{4} \d{2}:\d{2}:\d{2}\] 127\.0\.0\.1\s+\| 404 \| GET\s+/home/kalifdsa\n$`)
	if !pattern.MatchString(out.String()) {
		t.Fatalf("log line = %q", out.String())
	}
}

func TestRequestLoggerLogsPostStartBeforeCompletion(t *testing.T) {
	sink := newLogSink()
	logger := log.New(sink, "", 0)
	release := make(chan struct{})
	done := make(chan struct{})

	handler := requestLogger(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusNoContent)
	}), logRoutes{assetPrefix: "/_trasmetto-assets/", archivePrefix: "/_trasmetto-archive/"}, false, nil)

	request := httptest.NewRequest(http.MethodPost, "/upload", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}()

	firstLine := sink.nextLine(t)
	if !strings.Contains(firstLine, "| UPL | POST /upload") {
		t.Fatalf("first log line = %q, want POST start", firstLine)
	}

	close(release)
	<-done

	finalLine := sink.nextLine(t)
	if !strings.Contains(finalLine, "| 204 | POST /upload") {
		t.Fatalf("final log line = %q, want POST completion", finalLine)
	}
}

func TestRequestLoggerNoUploadMarkerForArchivePost(t *testing.T) {
	var out bytes.Buffer
	logger := log.New(&out, "", 0)
	handler := requestLogger(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logRoutes{assetPrefix: "/_trasmetto-assets/", archivePrefix: "/_trasmetto-archive/"}, false, nil)

	request := httptest.NewRequest(http.MethodPost, "/_trasmetto-archive/", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	handler.ServeHTTP(httptest.NewRecorder(), request)

	logged := out.String()
	if strings.Contains(logged, "UPL") {
		t.Fatalf("archive POST logged an UPL upload marker:\n%s", logged)
	}
	if !strings.Contains(logged, "| 200 | POST /_trasmetto-archive/") {
		t.Fatalf("archive POST completion line missing:\n%s", logged)
	}
}

func TestHTTPErrorLoggerSuppressesTLSHandshakeErrors(t *testing.T) {
	var out bytes.Buffer
	logger := HTTPErrorLogger(log.New(&out, "", 0))

	logger.Print("http: TLS handshake error from 127.0.0.1:58706: client sent an HTTP request to an HTTPS server")
	logger.Print("http: TLS handshake error from 127.0.0.1:60212: remote error: tls: bad certificate")

	if out.Len() != 0 {
		t.Fatalf("TLS handshake errors were logged: %q", out.String())
	}
}

func TestHTTPErrorLoggerSuppressesHTTP2ConnectionErrors(t *testing.T) {
	var out bytes.Buffer
	logger := HTTPErrorLogger(log.New(&out, "", 0))

	logger.Print("http2: server: error reading preface from client 127.0.0.1:47018: read: connection reset by peer")
	logger.Print("http2: server connection error from 127.0.0.1: protocol error")
	logger.Print("http2: server closing client connection: EOF")

	if out.Len() != 0 {
		t.Fatalf("HTTP/2 connection errors were logged: %q", out.String())
	}
}

func TestHTTPErrorLoggerPreservesOtherServerErrors(t *testing.T) {
	var out bytes.Buffer
	logger := HTTPErrorLogger(log.New(&out, "", 0))

	logger.Print("http: panic serving 127.0.0.1: unexpected failure")

	if !strings.Contains(out.String(), "http: panic serving 127.0.0.1: unexpected failure") {
		t.Fatalf("server error was suppressed: %q", out.String())
	}
}

func TestLogQuotedPreservesWindowsPathSeparators(t *testing.T) {
	path := `C:\Users\itemployee46\tools\notes.txt`

	got := logQuoted(path)
	want := `"C:\Users\itemployee46\tools\notes.txt"`
	if got != want {
		t.Fatalf("logQuoted() = %q, want %q", got, want)
	}
}

func TestLogQuotedEscapesUnsafeCharacters(t *testing.T) {
	got := logQuoted("bad\nname\".txt")
	want := `"bad\nname\".txt"`
	if got != want {
		t.Fatalf("logQuoted() = %q, want %q", got, want)
	}
}

type logSink struct {
	mu    sync.Mutex
	lines chan string
}

func newLogSink() *logSink {
	return &logSink{lines: make(chan string, 8)}
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines <- string(p)
	return len(p), nil
}

func (s *logSink) nextLine(t *testing.T) string {
	t.Helper()
	select {
	case line := <-s.lines:
		return line
	case <-time.After(time.Second):
		t.Fatalf("missing log line")
		return ""
	}
}

var _ io.Writer = (*logSink)(nil)

func TestFileLogSkipsAssetRequests(t *testing.T) {
	var buf bytes.Buffer
	events := NewEventLogger(&buf)
	events.Start("test", "/srv", "http://h:8000/", "none", StartConfig{})

	handler := requestLogger(log.New(io.Discard, "", 0), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logRoutes{assetPrefix: "/_trasmetto-assets/", archivePrefix: "/_trasmetto-archive/"}, false, events)

	for _, path := range []string{
		"/_trasmetto-assets/css/layout/app.css?v=1",
		"/_trasmetto-assets/js/app.js?v=1",
		"/report.pdf",
		"/_trasmetto-archive/",
	} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	events.Close()

	entries := decodeLines(t, buf.Bytes())
	logged := 0
	for _, entry := range entries {
		path, _ := entry["path"].(string)
		if strings.Contains(path, "_trasmetto-assets") {
			t.Errorf("asset request was logged: %s", path)
		}
		if entry["type"] != "START" && entry["type"] != "STOP" {
			logged++
		}
	}
	if logged != 2 {
		t.Fatalf("logged %d entries, want 2 (assets excluded)", logged)
	}
}

func TestHeadRequestIsNotLoggedAsDownload(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srv := newTestServer(t, root, "")

	var buf bytes.Buffer
	events := NewEventLogger(&buf)
	srv.SetEventLogger(events)
	handler := srv.Routes(log.New(io.Discard, "", 0))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodHead, "/a.txt", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/a.txt", nil))
	events.Close()

	var downloads, requests int
	for _, entry := range decodeLines(t, buf.Bytes()) {
		switch entry["type"] {
		case "download":
			downloads++
		case "request":
			requests++
		}
	}
	if downloads != 1 {
		t.Errorf("downloads = %d, want 1 (HEAD transfers no body)", downloads)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1 (the HEAD)", requests)
	}
}

func TestOnlyRealUploadsGetTheUploadMarker(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	routes := logRoutes{
		assetPrefix:   "/_trasmetto-assets/",
		archivePrefix: "/_trasmetto-archive/",
		managePrefix:  "/_trasmetto-manage/",
		loginPath:     "/_trasmetto-login",
	}
	handler := requestLogger(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), routes, false, nil)

	for _, path := range []string{
		"/_trasmetto-login",    // a login is not an upload
		"/_trasmetto-manage/",  // nor is mkdir or delete
		"/_trasmetto-archive/", // nor is a zip selection
		"/",                    // this one is
	} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil))
	}

	if got := strings.Count(buf.String(), "UPL"); got != 1 {
		t.Errorf("UPL appeared %d times, want 1 (the upload only):\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "| UPL | POST /") {
		t.Errorf("the real upload lost its marker:\n%s", buf.String())
	}
}

func TestStatusIsLoggedWhenTheHeaderIsCommitted(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var buf bytes.Buffer
	srv := newTestServer(t, root, "")
	srv.logger = log.New(&buf, "", 0)
	handler := srv.Routes(srv.logger)

	send := func(method, path string, header http.Header) {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		for key, values := range header {
			request.Header[key] = values
		}
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	send(http.MethodGet, "/a.txt", nil)
	send(http.MethodGet, "/", nil)
	send(http.MethodGet, "/nope", nil)
	send(http.MethodGet, "/a.txt", http.Header{"Range": {"bytes=0-0"}})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want one per request:\n%s", len(lines), buf.String())
	}
	// The real status is logged, not an assumed 200: a range request is 206.
	for i, want := range []string{"| 200 | GET  /a.txt", "| 200 | GET  /", "| 404 | GET  /nope", "| 206 | GET  /a.txt"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d = %q, want it to contain %q", i+1, lines[i], want)
		}
	}
}

func TestDetailLinesCarryTheClientIP(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var buf bytes.Buffer
	srv := newTestServer(t, root, "")
	srv.logger = log.New(&buf, "", 0)
	srv.fullAccess = true
	handler := srv.Routes(srv.logger)

	form := func(path, body string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.RemoteAddr = "203.0.113.7:54321"
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	form("/_trasmetto-archive/", "items=a.txt")
	form("/_trasmetto-manage/", "op=mkdir&name=made")
	form("/_trasmetto-manage/", "op=delete&items=made")

	// Every line, status or detail, must name who caused it.
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if !strings.Contains(line, "203.0.113.7") {
			t.Errorf("line has no client IP: %q", line)
		}
	}
	for _, want := range []string{"zipped", "created folder", "deleted"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing a %q line:\n%s", want, buf.String())
		}
	}

	// A detail logged without a request must not panic.
	srv.logDetail(nil, "render page failed: %v", io.EOF)
}
