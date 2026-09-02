package server

import (
	"bytes"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestHandleUploadStreamsFile(t *testing.T) {
	root := t.TempDir()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("files", "large.txt")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	if _, err := io.Copy(part, strings.NewReader(strings.Repeat("data\n", 1024))); err != nil {
		t.Fatalf("write multipart body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	s := &Server{
		root:           root,
		rootDisplay:    root,
		maxUploadBytes: 0,
		logger:         log.New(io.Discard, "", 0),
	}
	request := httptest.NewRequest(http.MethodPost, "/", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()

	s.handleUpload(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "large.txt"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if len(got) != 5*1024 {
		t.Fatalf("uploaded size = %d, want %d", len(got), 5*1024)
	}
}

func TestHandleUploadEnforcesConfiguredLimitWhileStreaming(t *testing.T) {
	root := t.TempDir()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("files", "too-large.txt")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	if _, err := part.Write([]byte("exceeds")); err != nil {
		t.Fatalf("write multipart body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	s := &Server{
		root:           root,
		rootDisplay:    root,
		maxUploadBytes: 3,
		logger:         log.New(io.Discard, "", 0),
	}
	request := httptest.NewRequest(http.MethodPost, "/", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()

	s.handleUpload(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "too-large.txt")); !os.IsNotExist(err) {
		t.Fatalf("partial upload remains on disk: %v", err)
	}
}

func TestHandleUploadKeepsExistingFileByDefault(t *testing.T) {
	root := t.TempDir()
	existingPath := filepath.Join(root, "same.txt")
	if err := os.WriteFile(existingPath, []byte("old"), 0600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	recorder := uploadTestFile(t, &Server{
		root:        root,
		rootDisplay: root,
		logger:      log.New(io.Discard, "", 0),
	}, "same.txt", "new")

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}
	assertFileContent(t, existingPath, "old")
	assertFileContent(t, filepath.Join(root, "same(1).txt"), "new")
}

func TestHandleUploadCanReplaceExistingFile(t *testing.T) {
	root := t.TempDir()
	existingPath := filepath.Join(root, "same.txt")
	if err := os.WriteFile(existingPath, []byte("old"), 0600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	recorder := uploadTestFile(t, &Server{
		root:         root,
		rootDisplay:  root,
		allowReplace: true,
		logger:       log.New(io.Discard, "", 0),
	}, "same.txt", "new")

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}
	assertFileContent(t, existingPath, "new")
	if _, err := os.Stat(filepath.Join(root, "same(1).txt")); !os.IsNotExist(err) {
		t.Fatalf("rename-on-collision file exists in replace mode: %v", err)
	}
}

func TestHandleUploadConcurrentSameNameNeverCollides(t *testing.T) {
	root := t.TempDir()
	s := &Server{
		root:        root,
		rootDisplay: root,
		logger:      log.New(io.Discard, "", 0),
	}

	const clients = 24
	var wg sync.WaitGroup
	codes := make([]int, clients)
	wg.Add(clients)
	for i := 0; i < clients; i++ {
		go func(i int) {
			defer wg.Done()
			codes[i] = uploadTestFile(t, s, "same.txt", "payload").Code
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusNoContent {
			t.Fatalf("client %d status = %d, want %d", i, code, http.StatusNoContent)
		}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(entries) != clients {
		t.Fatalf("saved %d files, want %d (concurrent uploads collided)", len(entries), clients)
	}
}

func TestHandleUploadReplaceDoesNotCorruptOnFailure(t *testing.T) {
	root := t.TempDir()
	existingPath := filepath.Join(root, "same.txt")
	if err := os.WriteFile(existingPath, []byte("ORIGINAL"), 0600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	recorder := uploadTestFile(t, &Server{
		root:           root,
		rootDisplay:    root,
		allowReplace:   true,
		maxUploadBytes: 3,
		logger:         log.New(io.Discard, "", 0),
	}, "same.txt", "REPLACEMENT-THAT-EXCEEDS-LIMIT")

	if recorder.Code == http.StatusNoContent {
		t.Fatalf("oversized replace upload unexpectedly succeeded")
	}

	assertFileContent(t, existingPath, "ORIGINAL")
}

func TestHandleUploadSanitizesTraversalFilename(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s := &Server{root: root, rootDisplay: root, logger: log.New(io.Discard, "", 0)}

	rec := uploadTestFileTo(t, s, "/sub", "../../evil.txt", "x")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(sub, "evil.txt")); err != nil {
		t.Fatalf("expected sub/evil.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "evil.txt")); err == nil {
		t.Fatalf("file escaped the target directory")
	}
}

func uploadTestFile(t *testing.T, s *Server, filename string, content string) *httptest.ResponseRecorder {
	return uploadTestFileTo(t, s, "/", filename, content)
}

func uploadTestFileTo(t *testing.T, s *Server, target, filename, content string) *httptest.ResponseRecorder {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("files", filename)
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("write multipart body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, target, body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	s.handleUpload(recorder, request)
	return recorder
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}

func TestMultiFileUploadLogsPerFileSizes(t *testing.T) {
	root := t.TempDir()
	srv := newTestServer(t, root, "")

	var buf bytes.Buffer
	events := NewEventLogger(&buf)
	srv.SetEventLogger(events)
	handler := srv.Routes(log.New(io.Discard, "", 0))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	sizes := map[string]int{"small.bin": 10, "big.bin": 5000}
	for name, size := range sizes {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write(bytes.Repeat([]byte("x"), size)); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	handler.ServeHTTP(httptest.NewRecorder(), request)
	events.Close()

	seen := map[string]int64{}
	for _, entry := range decodeLines(t, buf.Bytes()) {
		if entry["type"] != "upload" {
			continue
		}
		to, _ := entry["to"].(string)
		bytesLogged, _ := entry["bytes"].(float64)
		seen[to] = int64(bytesLogged)
	}
	if len(seen) != len(sizes) {
		t.Fatalf("logged %d uploads, want %d: %v", len(seen), len(sizes), seen)
	}
	for name, size := range sizes {
		// Each file must report its own size, not the whole request body.
		if seen[name] != int64(size) {
			t.Errorf("%s logged %d bytes, want %d", name, seen[name], size)
		}
	}
}

func TestUploadErrorDoesNotLeakServerPaths(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "nowrite")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	srv := newTestServer(t, root, "")
	handler := srv.Routes(log.New(io.Discard, "", 0))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("files", "x.txt")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("data")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/nowrite", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", recorder.Code)
	}
	response := recorder.Body.String()
	if strings.Contains(response, root) {
		t.Errorf("response discloses the server path: %q", response)
	}
	if !strings.Contains(response, "no write permission") {
		t.Errorf("response should explain the failure, got %q", response)
	}
}

func TestReplacedUploadIsReported(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "doc.txt"), []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srv := newTestServer(t, root, "")
	srv.allowReplace = true

	var buf bytes.Buffer
	events := NewEventLogger(&buf)
	srv.SetEventLogger(events)
	handler := srv.Routes(log.New(io.Discard, "", 0))

	post := func(name string) {
		t.Helper()
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write([]byte("new")); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		request := httptest.NewRequest(http.MethodPost, "/", body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	post("doc.txt")   // overwrites
	post("fresh.txt") // brand new
	events.Close()

	seen := map[string]bool{}
	for _, entry := range decodeLines(t, buf.Bytes()) {
		if entry["type"] != "upload" {
			continue
		}
		to, _ := entry["to"].(string)
		replaced, _ := entry["replaced"].(bool)
		seen[to] = replaced
	}
	if !seen["doc.txt"] {
		t.Error("overwriting an existing file should be marked replaced")
	}
	if _, ok := seen["fresh.txt"]; !ok {
		t.Fatal("the new file was not logged")
	}
	if seen["fresh.txt"] {
		t.Error("a brand new file must not be marked replaced")
	}
}
