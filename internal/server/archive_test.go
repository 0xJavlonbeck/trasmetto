package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func archiveServer(t *testing.T, root string) *Server {
	t.Helper()
	return &Server{root: root, rootDisplay: root, logger: log.New(io.Discard, "", 0)}
}

func readZip(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("open zip: %v (status %d)", err, rec.Code)
	}
	out := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read entry %s: %v", f.Name, err)
		}
		out[f.Name] = string(data)
	}
	return out
}

func TestHandleArchiveZipsDirectoryTree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("AAA"), 0600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("BBB"), 0600); err != nil {
		t.Fatalf("write b: %v", err)
	}
	s := archiveServer(t, root)

	rec := httptest.NewRecorder()
	s.handleArchive(rec, httptest.NewRequest(http.MethodGet, "/_trasmetto-archive/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type = %q", ct)
	}
	files := readZip(t, rec)
	if files["a.txt"] != "AAA" || files["sub/b.txt"] != "BBB" {
		t.Fatalf("zip contents = %v", files)
	}
}

func TestHandleArchiveExcludesSymlinksAndTempFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET"), 0600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("KEEP"), 0600); err != nil {
		t.Fatalf("write keep: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, uploadTempPrefix+"123"), []byte("TMP"), 0600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "leak")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatalf("symlink: %v", err)
	}
	s := archiveServer(t, root)

	rec := httptest.NewRecorder()
	s.handleArchive(rec, httptest.NewRequest(http.MethodGet, "/_trasmetto-archive/", nil))

	files := readZip(t, rec)
	if files["keep.txt"] != "KEEP" {
		t.Fatalf("expected keep.txt in zip, got %v", files)
	}
	for name, content := range files {
		if content == "TOP-SECRET" {
			t.Fatalf("archive leaked escaping symlink target via %q", name)
		}
	}
	if _, ok := files["leak"]; ok {
		t.Fatalf("symlink was archived")
	}
	if _, ok := files[uploadTempPrefix+"123"]; ok {
		t.Fatalf("internal temp file was archived")
	}
}

func TestHandleArchiveDisabledWithNoZip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("A"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &Server{root: root, rootDisplay: root, noZip: true, logger: log.New(io.Discard, "", 0)}
	rec := httptest.NewRecorder()
	s.handleArchive(rec, httptest.NewRequest(http.MethodGet, "/_trasmetto-archive/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (feature disabled)", rec.Code)
	}
}

func TestHandleArchiveRejectsFolderOverMaxZipSize(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 4; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%d.bin", i)), make([]byte, 4096), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	over := &Server{root: root, rootDisplay: root, maxZipBytes: 3000, logger: log.New(io.Discard, "", 0)}
	rec := httptest.NewRecorder()
	over.handleArchive(rec, httptest.NewRequest(http.MethodGet, "/_trasmetto-archive/", nil))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-limit status = %d, want 413", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "application/zip" {
		t.Fatalf("over-limit response streamed a zip anyway")
	}

	under := &Server{root: root, rootDisplay: root, maxZipBytes: 1 << 20, logger: log.New(io.Discard, "", 0)}
	rec2 := httptest.NewRecorder()
	under.handleArchive(rec2, httptest.NewRequest(http.MethodGet, "/_trasmetto-archive/", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("under-limit status = %d, want 200", rec2.Code)
	}
	if files := readZip(t, rec2); len(files) != 4 {
		t.Fatalf("under-limit archive had %d files, want 4", len(files))
	}
}

func postArchive(t *testing.T, s *Server, items []string, precheck bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	for _, it := range items {
		form.Add("items", it)
	}
	req := httptest.NewRequest(http.MethodPost, "/_trasmetto-archive/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if precheck {
		req.Header.Set("X-Trasmetto-Precheck", "1")
	}
	rec := httptest.NewRecorder()
	s.handleArchive(rec, req)
	return rec
}

func TestHandleArchiveZipsSelectedItems(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("AAA"), 0600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("BBB"), 0600); err != nil {
		t.Fatalf("write b: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "c.txt"), []byte("CCC"), 0600); err != nil {
		t.Fatalf("write c: %v", err)
	}
	s := archiveServer(t, root)

	rec := postArchive(t, s, []string{"a.txt", "docs"}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !regexp.MustCompile(`filename="[a-z]{6}\.zip"`).MatchString(cd) {
		t.Fatalf("selection filename not 6 letters + .zip: %q", cd)
	}
	files := readZip(t, rec)
	if files["a.txt"] != "AAA" || files["docs/c.txt"] != "CCC" {
		t.Fatalf("selection zip = %v", files)
	}
	if _, ok := files["b.txt"]; ok {
		t.Fatalf("unselected file b.txt was included")
	}
}

func TestHandleArchiveSelectionRejectsEscape(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("OK"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := archiveServer(t, root)

	rec := postArchive(t, s, []string{"../../etc/passwd"}, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (no valid items)", rec.Code)
	}
}

func TestHandleArchiveSelectionPrecheckOverLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.bin"), make([]byte, 8192), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &Server{root: root, rootDisplay: root, maxZipBytes: 1000, logger: log.New(io.Discard, "", 0)}

	rec := postArchive(t, s, []string{"big.bin"}, true)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("precheck status = %d, want 413", rec.Code)
	}
	if rec.Header().Get("X-Trasmetto-Zip-Error") == "" {
		t.Fatalf("missing X-Trasmetto-Zip-Error header")
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") == "application/zip" {

		if rec.Header().Get("Content-Type") == "application/zip" {
			t.Fatalf("precheck streamed a zip")
		}
	}
}

func TestHandleArchiveSelectionPrecheckOK(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "small.txt"), []byte("hi"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := archiveServer(t, root)

	rec := postArchive(t, s, []string{"small.txt"}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("precheck status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("precheck returned a body: %q", rec.Body.String())
	}
	if got := rec.Header().Get("X-Trasmetto-Zip-Size"); got != "2" {
		t.Fatalf("X-Trasmetto-Zip-Size = %q, want 2 (bytes of \"hi\")", got)
	}
}

func TestHandleArchiveDisabledWhenUploadOnly(t *testing.T) {
	s := &Server{root: t.TempDir(), uploadOnly: true, logger: log.New(io.Discard, "", 0)}
	rec := httptest.NewRecorder()
	s.handleArchive(rec, httptest.NewRequest(http.MethodGet, "/_trasmetto-archive/", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestArchivePathURL(t *testing.T) {
	tests := []struct {
		publicPath string
		rel        string
		want       string
	}{
		{"", ".", "/_trasmetto-archive/"},
		{"", "Windows", "/_trasmetto-archive/Windows"},
		{"", "a/b c", "/_trasmetto-archive/a/b%20c"},
		{"secret", ".", "/secret/_trasmetto-archive/"},
		{"secret", "Windows", "/secret/_trasmetto-archive/Windows"},
	}
	for _, tc := range tests {
		if got := archivePathURL(tc.publicPath, "_trasmetto-archive", tc.rel); got != tc.want {
			t.Errorf("archivePathURL(%q,%q) = %q, want %q", tc.publicPath, tc.rel, got, tc.want)
		}
	}
}

func TestArchiveFilenameUsesFolderName(t *testing.T) {
	cases := []struct {
		dir, root, want string
	}{
		{"/srv/files", "/srv/files", "files.zip"},         // root: served folder's own name
		{"/srv/files/photos", "/srv/files", "photos.zip"}, // subdirectory
		{"/home/user/My Docs", "/home/user/My Docs", "My Docs.zip"},
		{"/", "/", "archive.zip"}, // filesystem root: no usable name
	}
	for _, tc := range cases {
		if got := archiveFilename(tc.dir, tc.root); got != tc.want {
			t.Errorf("archiveFilename(%q,%q) = %q, want %q", tc.dir, tc.root, got, tc.want)
		}
	}
}

func TestArchiveItemNamesUseRootSlash(t *testing.T) {
	root := t.TempDir()
	srv := &Server{root: root}

	got := srv.archiveItemNames([]zipTarget{
		{real: root},
		{real: filepath.Join(root, "docs")},
		{real: filepath.Join(root, "top.txt")},
	})
	want := []string{"/", "docs", "top.txt"}
	if len(got) != len(want) {
		t.Fatalf("archiveItemNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %q, want %q", i, got[i], want[i])
		}
	}
}
