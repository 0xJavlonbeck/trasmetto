package client

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadUsesURLFilename(t *testing.T) {
	dir := t.TempDir()
	saved, err := Download(DownloadOptions{
		URL:        "http://example.test/files/report.txt",
		OutputPath: dir,
		HTTPClient: responseClient(http.StatusOK, nil, "content"),
	})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}

	want := filepath.Join(dir, "report.txt")
	if saved != want {
		t.Fatalf("saved path = %q, want %q", saved, want)
	}
	assertFileContent(t, saved, "content")
}

func TestDownloadUsesContentDispositionFilename(t *testing.T) {
	dir := t.TempDir()
	saved, err := Download(DownloadOptions{
		URL:        "http://example.test/download",
		OutputPath: dir,
		HTTPClient: responseClient(http.StatusOK, http.Header{
			"Content-Disposition": []string{`attachment; filename="server-name.txt"`},
		}, "body"),
	})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}

	want := filepath.Join(dir, "server-name.txt")
	if saved != want {
		t.Fatalf("saved path = %q, want %q", saved, want)
	}
	assertFileContent(t, saved, "body")
}

func TestDownloadRenamesExistingFile(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(existing, []byte("old"), 0644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	saved, err := Download(DownloadOptions{
		URL:        "http://example.test/file.txt",
		OutputPath: dir,
		HTTPClient: responseClient(http.StatusOK, nil, "new"),
	})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}

	want := filepath.Join(dir, "file(1).txt")
	if saved != want {
		t.Fatalf("saved path = %q, want %q", saved, want)
	}
	assertFileContent(t, existing, "old")
	assertFileContent(t, saved, "new")
}

func TestDownloadReportsProgress(t *testing.T) {
	dir := t.TempDir()
	var stdout bytes.Buffer

	_, err := Download(DownloadOptions{
		URL:        "http://example.test/passwd",
		OutputPath: dir,
		Stdout:     &stdout,
		HTTPClient: responseClient(http.StatusOK, nil, "downloaded"),
	})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"Downloading: http://example.test/passwd",
		"Response: HTTP 200 OK",
		"Saving to:",
		"[============================] 100%",
		"10 B/10 B",
		"Saved:",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("progress output missing %q:\n%s", want, output)
		}
	}
}

func responseClient(status int, header http.Header, body string) *http.Client {
	return &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if header == nil {
				header = make(http.Header)
			}
			return &http.Response{
				StatusCode:    status,
				Status:        http.StatusText(status),
				Header:        header,
				Body:          io.NopCloser(strings.NewReader(body)),
				ContentLength: int64(len(body)),
				Request:       r,
			}, nil
		}),
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s content = %q, want %q", path, string(data), want)
	}
}

func TestSafeFilenameStripsPathsAndUnescapes(t *testing.T) {
	cases := map[string]string{
		"report.pdf":            "report.pdf",
		"/etc/passwd":           "passwd",
		`C:\Windows\system.ini`: "system.ini",
		"../../secret":          "secret",
		"a%20b.txt":             "a b.txt",
		"  spaced.txt  ":        "spaced.txt",
	}
	for in, want := range cases {
		if got := safeFilename(in); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsWindowsReservedName(t *testing.T) {

	for _, reserved := range []string{"CON", "con", "NUL", "aux.txt", "LPT1", "COM9.dat", "PRN"} {
		if !isWindowsReservedName(reserved) {
			t.Errorf("isWindowsReservedName(%q) = false, want true", reserved)
		}
	}
	for _, ok := range []string{"console.txt", "report.pdf", "com", "lpt", "nullable.go", ""} {
		if isWindowsReservedName(ok) {
			t.Errorf("isWindowsReservedName(%q) = true, want false", ok)
		}
	}
}

func TestDownloadTrailingSeparatorCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested") + string(os.PathSeparator)

	saved, err := Download(DownloadOptions{
		URL:        "http://example.test/files/report.txt",
		OutputPath: target,
		HTTPClient: responseClient(http.StatusOK, nil, "content"),
	})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}

	want := filepath.Join(dir, "nested", "report.txt")
	if saved != want {
		t.Fatalf("saved path = %q, want %q", saved, want)
	}
	info, err := os.Stat(filepath.Join(dir, "nested"))
	if err != nil || !info.IsDir() {
		t.Fatalf("nested should be a directory, got err=%v", err)
	}
	assertFileContent(t, saved, "content")
}

func TestDownloadTrailingSeparatorRejectsExistingFile(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "afile")
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Download(DownloadOptions{
		URL:        "http://example.test/files/report.txt",
		OutputPath: existing + string(os.PathSeparator),
		HTTPClient: responseClient(http.StatusOK, nil, "content"),
	})
	if err == nil {
		t.Fatal("expected an error when the output path is a file, got nil")
	}
}
