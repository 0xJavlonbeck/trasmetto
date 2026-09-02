package server

import (
	"archive/zip"
	"bytes"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"trasmetto/internal/config"
)

func fileSetServer(t *testing.T, publicPath string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	report := filepath.Join(dir, "report.pdf")
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(report, []byte("REPORT"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, []byte("NOTES"), 0600); err != nil {
		t.Fatal(err)
	}
	tmpl := template.Must(template.New("index.html").Parse(`LIST{{range .Entries}} [{{.Name}}]{{end}}`))
	return &Server{
		fileSetMode:  true,
		downloadOnly: true,
		noZip:        true,
		publicPath:   publicPath,
		fileSet:      map[string]string{"report.pdf": report, "notes.txt": notes},
		fileEntries:  []entry{{Name: "report.pdf", RelPath: "report.pdf"}, {Name: "notes.txt", RelPath: "notes.txt"}},
		pageTemplate: tmpl,
		logger:       log.New(io.Discard, "", 0),
	}, dir
}

func TestFileSetListingAndDownload(t *testing.T) {
	s, _ := fileSetServer(t, "")

	rec := httptest.NewRecorder()
	s.handleFileSet(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("listing status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "[report.pdf]") || !strings.Contains(body, "[notes.txt]") {
		t.Fatalf("listing missing files: %q", body)
	}

	rec = httptest.NewRecorder()
	s.handleFileSet(rec, httptest.NewRequest(http.MethodGet, "/report.pdf", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "REPORT" {
		t.Fatalf("download: status=%d body=%q", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "report.pdf") {
		t.Fatalf("content-disposition = %q", cd)
	}
}

func TestFileSetRejectsOthers(t *testing.T) {
	s, _ := fileSetServer(t, "")

	rec := httptest.NewRecorder()
	s.handleFileSet(rec, httptest.NewRequest(http.MethodGet, "/secret.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown file status = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.handleFileSet(rec, httptest.NewRequest(http.MethodGet, "/../etc/passwd", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("traversal status = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.handleFileSet(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
}

func TestFileSetHonoursHiddenPath(t *testing.T) {
	s, _ := fileSetServer(t, "secret")

	rec := httptest.NewRecorder()
	s.handleFileSet(rec, httptest.NewRequest(http.MethodGet, "/secret/report.pdf", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "REPORT" {
		t.Fatalf("scoped download: status=%d body=%q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.handleFileSet(rec, httptest.NewRequest(http.MethodGet, "/report.pdf", nil))
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "report.pdf") {
		t.Fatalf("outside prefix: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestFileSetEntriesAreReadableAndSortable(t *testing.T) {
	dir := t.TempDir()
	readable := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(readable, []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	locked := filepath.Join(dir, "locked.txt")
	if err := os.WriteFile(locked, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })

	_, entries := buildFileSet([]config.FileEntry{
		{Name: "one.txt", Real: readable, Size: 6},
		{Name: "locked.txt", Real: locked, Size: 6},
	})
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	// Without this the listing marks every served file [no read] and, worse,
	// renders it as plain text instead of a download link.
	if !entries[0].Readable {
		t.Error("a readable file was marked unreadable")
	}
	if entries[0].Bytes != 6 {
		t.Errorf("Bytes = %d, want 6 so the client can sort by size", entries[0].Bytes)
	}
	if os.Geteuid() != 0 && entries[1].Readable {
		t.Error("an unreadable file should still be marked")
	}
}

func TestFileSetArchive(t *testing.T) {
	dir := t.TempDir()
	elsewhere := t.TempDir() // -f files need not share a directory
	paths := map[string]string{
		"a.txt": filepath.Join(dir, "a.txt"),
		"b.txt": filepath.Join(elsewhere, "b.txt"),
	}
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	secret := filepath.Join(elsewhere, "secret.txt")
	if err := os.WriteFile(secret, []byte("no"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := newTestServer(t, dir, "")
	srv.fileSetMode = true
	srv.fileSet, srv.fileEntries = buildFileSet([]config.FileEntry{
		{Name: "a.txt", Real: paths["a.txt"], Size: 4},
		{Name: "b.txt", Real: paths["b.txt"], Size: 4},
	})
	handler := srv.Routes(log.New(io.Discard, "", 0))

	names := func(body []byte) []string {
		t.Helper()
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatalf("archive is not a zip: %v", err)
		}
		var out []string
		for _, f := range zr.File {
			out = append(out, f.Name)
		}
		sort.Strings(out)
		return out
	}

	// Everything served, including the file outside the root directory.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/_trasmetto-archive/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("archive = %d, want 200", recorder.Code)
	}
	if got := strings.Join(names(recorder.Body.Bytes()), ","); got != "a.txt,b.txt" {
		t.Errorf("archive contains %q, want both served files", got)
	}

	// A selection.
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/_trasmetto-archive/", strings.NewReader("items=b.txt"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, request)
	if got := strings.Join(names(recorder.Body.Bytes()), ","); got != "b.txt" {
		t.Errorf("selection contains %q, want only b.txt", got)
	}

	// A path that was never served cannot be smuggled in.
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/_trasmetto-archive/", strings.NewReader("items="+secret))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("unserved path = %d, want 400", recorder.Code)
	}
}
