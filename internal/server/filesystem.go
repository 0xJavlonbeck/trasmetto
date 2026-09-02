package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

var errOutsidePublicPath = errors.New("request outside public path")

type entry struct {
	Name     string
	RelPath  string
	IsDir    bool
	IsLink   bool
	Readable bool
	Size     string
	Bytes    int64
}

// SortGroup keeps directories, links and files in their own bands when the
// client re-sorts the listing.
func (e entry) SortGroup() int {
	return entrySortGroup(e)
}

func (e entry) DisplayName() string {
	if e.IsLink {
		return e.Name + "@"
	}
	if e.IsDir {
		return e.Name + "/"
	}
	return e.Name
}

func (s *Server) resolvePath(input string) (string, string, error) {
	rel := filepath.Clean(filepath.FromSlash(input))
	if rel == "" {
		rel = "."
	}
	if filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("absolute paths are not allowed")
	}

	full := filepath.Clean(filepath.Join(s.root, rel))
	if !isWithinRoot(full, s.root) {
		return "", "", fmt.Errorf("path escapes root")
	}

	return full, relFromAbs(full, s.root), nil
}

func (s *Server) resolveURLPath(r *http.Request) (string, string, error) {
	escapedPath := strings.TrimPrefix(r.URL.EscapedPath(), "/")
	unescapedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", "", err
	}
	unescapedPath, ok := s.stripPublicPath(unescapedPath)
	if !ok {
		return "", "", errOutsidePublicPath
	}
	return s.resolvePath(unescapedPath)
}

func (s *Server) stripPublicPath(requestPath string) (string, bool) {
	requestPath = strings.Trim(requestPath, "/")
	if s.publicPath == "" {
		return requestPath, true
	}
	if requestPath == s.publicPath {
		return ".", true
	}

	prefix := s.publicPath + "/"
	if strings.HasPrefix(requestPath, prefix) {
		return strings.TrimPrefix(requestPath, prefix), true
	}
	return "", false
}

func (s *Server) realPath(path string) (string, error) {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}

	realPath = filepath.Clean(realPath)
	if !isWithinRoot(realPath, s.root) {
		return "", fmt.Errorf("path escapes root through symlink")
	}

	return realPath, nil
}

// isEntryReadable reports whether this process could actually open the entry,
// so the listing can mark what it cannot serve instead of failing on click.
//
// Only regular files and directories are probed. Opening a FIFO blocks until a
// writer appears, which would hang the listing, and sockets and devices are not
// servable content either, so they are reported unreadable without touching
// them.
func isEntryReadable(path string, isDir bool) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// Follow the link, but judge what it points at by the same rule.
		if info, err = os.Stat(path); err != nil {
			return false
		}
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return false
	}

	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	if isDir {
		if _, err := file.ReadDir(1); err != nil && !errors.Is(err, io.EOF) {
			return false
		}
	}
	return true
}

func readEntries(dir string, root string) ([]entry, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	entries := make([]entry, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {

		if isInternalTemp(dirEntry.Name()) {
			continue
		}

		info, err := dirEntry.Info()
		if err != nil {
			continue
		}

		fullPath := filepath.Join(dir, dirEntry.Name())
		isLink := dirEntry.Type()&os.ModeSymlink != 0 || info.Mode()&os.ModeSymlink != 0
		isDir := dirEntry.IsDir()
		if isLink {
			isDir = symlinkTargetIsDir(fullPath, root)
		}

		rel := relFromAbs(fullPath, root)
		entries = append(entries, entry{
			Name:    dirEntry.Name(),
			RelPath: filepath.ToSlash(rel),
			IsDir:   isDir,
			IsLink:  isLink,
			// A link leaving the root can never be served, so it must not be
			// offered as one.
			Readable: isEntryReadable(fullPath, isDir) && (!isLink || symlinkWithinRoot(fullPath, root)),
			// isDir is the resolved value, so a symlink to a directory shows
			// "-" like a real one instead of the link's own byte size.
			Size:  formatSize(info.Size(), isDir),
			Bytes: entrySortBytes(info, isDir),
		})
	}

	sortEntries(entries)

	return entries, nil
}

func sortEntries(entries []entry) {
	sort.Slice(entries, func(i, j int) bool {
		leftGroup := entrySortGroup(entries[i])
		rightGroup := entrySortGroup(entries[j])
		if leftGroup != rightGroup {
			return leftGroup < rightGroup
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}

// entrySortBytes gives directories a size of -1 so they never mix in with
// files when the client sorts by size.
func entrySortBytes(info os.FileInfo, isDir bool) int64 {
	if isDir {
		return -1
	}
	return info.Size()
}

func entrySortGroup(item entry) int {
	if item.IsLink {
		return 1
	}
	if item.IsDir {
		return 0
	}
	return 2
}

// symlinkWithinRoot reports whether a link resolves to something still inside
// the served directory.
func symlinkWithinRoot(path string, root string) bool {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return isWithinRoot(filepath.Clean(realPath), root)
}

func symlinkTargetIsDir(path string, root string) bool {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	realPath = filepath.Clean(realPath)
	if !isWithinRoot(realPath, root) {
		return false
	}
	info, err := os.Stat(realPath)
	return err == nil && info.IsDir()
}

func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(filepath.Clean(filepath.FromSlash(name)))
	name = strings.Trim(name, ". ")
	if runtime.GOOS == "windows" && isWindowsReservedName(name) {
		name = "_" + name
	}
	return name
}

func isWindowsReservedName(name string) bool {
	stem := name
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

const (
	writeTestPrefix  = ".trasmetto-write-test-"
	uploadTempPrefix = ".trasmetto-upload-"
)

func isInternalTemp(name string) bool {
	return strings.HasPrefix(name, writeTestPrefix) || strings.HasPrefix(name, uploadTempPrefix)
}

func checkWritableDir(dir string) error {
	tmp, err := os.CreateTemp(dir, writeTestPrefix+"*")
	if err != nil {
		return err
	}

	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}

	return os.Remove(name)
}

func isWithinRoot(path string, root string) bool {
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
		root = strings.ToLower(root)
	}
	if path == root {
		return true
	}

	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func relFromAbs(path string, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "" {
		return "."
	}
	return filepath.Clean(rel)
}

func parentPath(rel string) string {
	if rel == "." {
		return "."
	}

	parent := filepath.Dir(filepath.FromSlash(rel))
	if parent == "" {
		return "."
	}
	return filepath.ToSlash(parent)
}

func displayPath(rel string) string {
	if rel == "." || rel == "" {
		return "/"
	}
	return "/" + filepath.ToSlash(rel)
}

func pathURL(publicPath string, rel string) string {
	rel = filepath.ToSlash(filepath.Clean(rel))
	prefix := encodedURLPath(publicPath)
	if rel == "." || rel == "" {
		if prefix == "" {
			return "/"
		}
		return "/" + prefix
	}

	encodedRel := encodedURLPath(rel)
	if prefix == "" {
		return "/" + encodedRel
	}
	return "/" + prefix + "/" + encodedRel
}

func encodedURLPath(raw string) string {
	raw = strings.Trim(raw, "/")
	if raw == "" || raw == "." {
		return ""
	}

	parts := strings.Split(filepath.ToSlash(raw), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func formatSize(size int64, isDir bool) string {
	if isDir {
		return "-"
	}

	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}
