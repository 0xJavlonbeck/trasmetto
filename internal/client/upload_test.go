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

func TestUploadStreamsMultipartFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(filePath, []byte("upload content"), 0600); err != nil {
		t.Fatalf("write upload file: %v", err)
	}

	httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/destination" {
			t.Errorf("path = %s, want /destination", r.URL.Path)
		}
		file, header, err := r.FormFile("files")
		if err != nil {
			t.Fatalf("read multipart file: %v", err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("read uploaded content: %v", err)
		}
		if header.Filename != "report.txt" {
			t.Errorf("filename = %q, want report.txt", header.Filename)
		}
		if string(data) != "upload content" {
			t.Errorf("content = %q, want upload content", data)
		}
		return uploadTestResponse(r, http.StatusNoContent, ""), nil
	})}

	var stdout bytes.Buffer
	err := Upload(UploadOptions{
		URL:        "http://example.test/destination",
		FilePath:   filePath,
		Stdout:     &stdout,
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "uploaded "+filePath+" ->") {
		t.Fatalf("stdout = %q, want uploaded file path", stdout.String())
	}
}

func TestUploadReturnsServerError(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "too-large.bin")
	if err := os.WriteFile(filePath, []byte("content"), 0600); err != nil {
		t.Fatalf("write upload file: %v", err)
	}

	httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return uploadTestResponse(r, http.StatusRequestEntityTooLarge, "upload limit 1MB exceeded\n"), nil
	})}

	err := Upload(UploadOptions{
		URL:        "http://example.test/",
		FilePath:   filePath,
		HTTPClient: httpClient,
	})
	if err == nil {
		t.Fatal("Upload succeeded, want error")
	}
	if !strings.Contains(err.Error(), "HTTP 413: upload limit 1MB exceeded") {
		t.Fatalf("error = %q, want server response", err)
	}
}

func uploadTestResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}
