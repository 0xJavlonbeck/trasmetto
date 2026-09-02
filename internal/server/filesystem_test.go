package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestPathURLUsesPublicPath(t *testing.T) {
	tests := []struct {
		publicPath string
		rel        string
		want       string
	}{
		{"", ".", "/"},
		{"", "tmp/files", "/tmp/files"},
		{"", "_trasmetto-assets/js/app.js", "/_trasmetto-assets/js/app.js"},
		{"veryhiddenpath", ".", "/veryhiddenpath"},
		{"veryhiddenpath", "tmp/files", "/veryhiddenpath/tmp/files"},
		{"veryhiddenpath", "_trasmetto-assets/js/app.js", "/veryhiddenpath/_trasmetto-assets/js/app.js"},
	}

	for _, tc := range tests {
		if got := pathURL(tc.publicPath, tc.rel); got != tc.want {
			t.Fatalf("pathURL(%q, %q) = %q, want %q", tc.publicPath, tc.rel, got, tc.want)
		}
	}
}

func TestResolveURLPathRequiresPublicPath(t *testing.T) {
	s := &Server{
		root:       t.TempDir(),
		publicPath: "veryhiddenpath",
	}

	if _, _, err := s.resolveURLPath(httptest.NewRequest("GET", "/wrong", nil)); err == nil {
		t.Fatalf("resolveURLPath accepted request outside public path")
	}

	_, rel, err := s.resolveURLPath(httptest.NewRequest("GET", "/veryhiddenpath", nil))
	if err != nil {
		t.Fatalf("resolveURLPath public root returned error: %v", err)
	}
	if rel != "." {
		t.Fatalf("resolveURLPath public root rel = %q, want .", rel)
	}
}

func TestReadEntriesMarksSymbolicLinks(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "target.txt")
	dirPath := filepath.Join(root, "target-dir")
	if err := os.WriteFile(filePath, []byte("content"), 0600); err != nil {
		t.Fatalf("write target file: %v", err)
	}
	if err := os.Mkdir(dirPath, 0700); err != nil {
		t.Fatalf("create target directory: %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(root, "file-link")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		t.Fatalf("create file symlink: %v", err)
	}
	if err := os.Symlink("target-dir", filepath.Join(root, "dir-link")); err != nil {
		t.Fatalf("create directory symlink: %v", err)
	}

	entries, err := readEntries(root, root)
	if err != nil {
		t.Fatalf("readEntries returned error: %v", err)
	}
	byName := make(map[string]entry, len(entries))
	for _, item := range entries {
		byName[item.Name] = item
	}

	fileLink := byName["file-link"]
	if !fileLink.IsLink || fileLink.IsDir || fileLink.DisplayName() != "file-link@" {
		t.Errorf("file link = %+v, display %q", fileLink, fileLink.DisplayName())
	}
	dirLink := byName["dir-link"]
	if !dirLink.IsLink || !dirLink.IsDir || dirLink.DisplayName() != "dir-link@" {
		t.Errorf("directory link = %+v, display %q", dirLink, dirLink.DisplayName())
	}
	regularDir := byName["target-dir"]
	if regularDir.IsLink || !regularDir.IsDir || regularDir.DisplayName() != "target-dir/" {
		t.Errorf("regular directory = %+v, display %q", regularDir, regularDir.DisplayName())
	}
}

func TestReadEntriesDoesNotTreatEscapingSymlinkAsDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	linkPath := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, linkPath); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		t.Fatalf("create escaping symlink: %v", err)
	}

	entries, err := readEntries(root, root)
	if err != nil {
		t.Fatalf("readEntries returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}
	if !entries[0].IsLink || entries[0].IsDir || entries[0].DisplayName() != "outside-link@" {
		t.Fatalf("escaping link = %+v, display %q", entries[0], entries[0].DisplayName())
	}
}

func TestIsWindowsReservedName(t *testing.T) {
	reserved := []string{"CON", "con", "NUL", "COM1", "LPT9", "con.txt", "AUX.log"}
	for _, name := range reserved {
		if !isWindowsReservedName(name) {
			t.Errorf("isWindowsReservedName(%q) = false, want true", name)
		}
	}
	ok := []string{"console", "com", "report.txt", "COM0", "COM10", "lpt", "trasmetto"}
	for _, name := range ok {
		if isWindowsReservedName(name) {
			t.Errorf("isWindowsReservedName(%q) = true, want false", name)
		}
	}
}

func TestSortEntriesPlacesLinksAfterDirectories(t *testing.T) {
	entries := []entry{
		{Name: "z-file"},
		{Name: "z-link", IsLink: true, IsDir: true},
		{Name: "z-dir", IsDir: true},
		{Name: "a-link", IsLink: true},
		{Name: "a-file"},
		{Name: "a-dir", IsDir: true},
	}

	sortEntries(entries)
	want := []string{"a-dir", "z-dir", "a-link", "z-link", "a-file", "z-file"}
	for i, name := range want {
		if entries[i].Name != name {
			t.Fatalf("entry %d = %q, want %q; entries = %+v", i, entries[i].Name, name, entries)
		}
	}
}

func TestListingDoesNotBlockOnAFifo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs on windows")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}

	// Opening a FIFO with no writer blocks forever, so this must finish
	// without ever opening it.
	done := make(chan []entry, 1)
	go func() {
		entries, err := readEntries(root, root)
		if err != nil {
			t.Errorf("readEntries: %v", err)
		}
		done <- entries
	}()

	select {
	case entries := <-done:
		var pipe *entry
		for i := range entries {
			if entries[i].Name == "pipe" {
				pipe = &entries[i]
			}
		}
		if pipe == nil {
			t.Fatal("the FIFO is missing from the listing")
		}
		if pipe.Readable {
			t.Error("a FIFO must not be reported readable; it cannot be served")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readEntries blocked on the FIFO")
	}
}

func TestListingOnlyLinksWhatItCanServe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is restricted on windows")
	}
	outer := t.TempDir()
	root := filepath.Join(outer, "served")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outer, "outside.txt"), []byte("no"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	links := map[string]string{
		"inside.txt":  filepath.Join(root, "real.txt"),
		"outside.txt": filepath.Join(outer, "outside.txt"),
		"broken.txt":  filepath.Join(root, "missing.txt"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	entries, err := readEntries(root, root)
	if err != nil {
		t.Fatalf("readEntries: %v", err)
	}
	readable := map[string]bool{}
	for _, e := range entries {
		readable[e.Name] = e.Readable
	}

	// Anything shown as a link must actually be servable.
	if !readable["real.txt"] || !readable["inside.txt"] {
		t.Errorf("a servable file was marked unreadable: %v", readable)
	}
	if readable["outside.txt"] {
		t.Error("a link leaving the root is not servable and must not be offered")
	}
	if readable["broken.txt"] {
		t.Error("a dangling link must not be offered")
	}
}
