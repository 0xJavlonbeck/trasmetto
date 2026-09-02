package server

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func manageServer(t *testing.T, root string, fullAccess bool) http.Handler {
	t.Helper()
	srv := newTestServer(t, root, "")
	srv.fullAccess = fullAccess
	return srv.Routes(log.New(io.Discard, "", 0))
}

func post(t *testing.T, handler http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestManageRequiresFullAccess(t *testing.T) {
	root := t.TempDir()
	handler := manageServer(t, root, false)

	got := post(t, handler, "/_trasmetto-manage/", url.Values{"op": {"mkdir"}, "name": {"x"}})
	if got.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when --full-access is off", got.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "x")); err == nil {
		t.Error("a folder was created without --full-access")
	}
}

func TestMakeDir(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	handler := manageServer(t, root, true)

	if got := post(t, handler, "/_trasmetto-manage/", url.Values{"op": {"mkdir"}, "name": {"photos"}}); got.Code != http.StatusNoContent {
		t.Fatalf("mkdir = %d, want 204", got.Code)
	}
	if info, err := os.Stat(filepath.Join(root, "photos")); err != nil || !info.IsDir() {
		t.Fatalf("folder not created: %v", err)
	}
	// Creating inside a subdirectory follows the request path.
	if got := post(t, handler, "/_trasmetto-manage/sub", url.Values{"op": {"mkdir"}, "name": {"deep"}}); got.Code != http.StatusNoContent {
		t.Fatalf("nested mkdir = %d, want 204", got.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "deep")); err != nil {
		t.Fatalf("nested folder not created: %v", err)
	}
	// A second attempt is a conflict, not a silent success.
	if got := post(t, handler, "/_trasmetto-manage/", url.Values{"op": {"mkdir"}, "name": {"photos"}}); got.Code != http.StatusConflict {
		t.Errorf("duplicate mkdir = %d, want 409", got.Code)
	}
}

func TestMakeDirRejectsBadNames(t *testing.T) {
	root := t.TempDir()
	handler := manageServer(t, root, true)

	// Names we would have to rewrite are refused, not silently changed.
	for _, name := range []string{"", "   ", "../escape", "a/b", `a\b`, ".", "..", ".hidden", "trailing."} {
		got := post(t, handler, "/_trasmetto-manage/", url.Values{"op": {"mkdir"}, "name": {name}})
		if got.Code != http.StatusBadRequest {
			t.Errorf("mkdir %q = %d, want 400", name, got.Code)
		}
	}
	// Nothing escaped the served root.
	parent := filepath.Dir(root)
	if _, err := os.Stat(filepath.Join(parent, "escape")); err == nil {
		t.Error("mkdir escaped the served root")
	}
}

func TestDeleteFilesAndFolders(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	nested := filepath.Join(root, "dir", "inner")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	handler := manageServer(t, root, true)

	got := post(t, handler, "/_trasmetto-manage/", url.Values{"op": {"delete"}, "items": {"a.txt", "dir"}})
	if got.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", got.Code)
	}
	for _, gone := range []string{"a.txt", "dir"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", gone)
		}
	}
}

func TestDeleteFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(filepath.Join(sub, "deep"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, name := range []string{"sub/b.txt", "sub/keep.txt", "sub/deep/d.txt"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	handler := manageServer(t, root, true)

	// The listing's checkboxes carry root-relative paths, so a deletion issued
	// from inside a folder must resolve from the root, not from that folder.
	got := post(t, handler, "/_trasmetto-manage/sub", url.Values{"op": {"delete"}, "items": {"sub/b.txt", "sub/deep"}})
	if got.Code != http.StatusNoContent {
		t.Fatalf("delete from subdirectory = %d (%s), want 204", got.Code, strings.TrimSpace(got.Body.String()))
	}
	for _, gone := range []string{"sub/b.txt", "sub/deep"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(gone))); !os.IsNotExist(err) {
			t.Errorf("%s still exists", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(sub, "keep.txt")); err != nil {
		t.Errorf("an unselected file was removed: %v", err)
	}
}

func TestDeleteStaysInsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "served")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	outside := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	handler := manageServer(t, root, true)

	for _, item := range []string{"../outside.txt", "../../etc/passwd", "", "  "} {
		if got := post(t, handler, "/_trasmetto-manage/", url.Values{"op": {"delete"}, "items": {item}}); got.Code != http.StatusBadRequest {
			t.Errorf("delete %q = %d, want 400", item, got.Code)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("a file outside the root was deleted: %v", err)
	}

	// Deleting a symlink removes the link, never what it points at.
	if got := post(t, handler, "/_trasmetto-manage/", url.Values{"op": {"delete"}, "items": {"link.txt"}}); got.Code != http.StatusNoContent {
		t.Fatalf("delete symlink = %d, want 204", got.Code)
	}
	if _, err := os.Lstat(filepath.Join(root, "link.txt")); !os.IsNotExist(err) {
		t.Error("symlink still present")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
		t.Error("deleting the symlink damaged its target")
	}
}

func TestMakeDirKeepsTheNameAsTyped(t *testing.T) {
	root := t.TempDir()
	handler := manageServer(t, root, true)

	if got := post(t, handler, "/_trasmetto-manage/", url.Values{"op": {"mkdir"}, "name": {"Photos 2026"}}); got.Code != http.StatusNoContent {
		t.Fatalf("mkdir = %d, want 204", got.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "Photos 2026")); err != nil {
		t.Errorf("folder was not created under the name given: %v", err)
	}
	// A dotted name must fail rather than appear without its dot.
	if got := post(t, handler, "/_trasmetto-manage/", url.Values{"op": {"mkdir"}, "name": {".hidden"}}); got.Code != http.StatusBadRequest {
		t.Errorf("mkdir .hidden = %d, want 400", got.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "hidden")); err == nil {
		t.Error(`".hidden" was silently created as "hidden"`)
	}
}

func TestManageRejectsGet(t *testing.T) {
	handler := manageServer(t, t.TempDir(), true)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/_trasmetto-manage/", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", recorder.Code)
	}
}
