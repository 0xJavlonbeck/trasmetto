package server

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"trasmetto/internal/config"
)

const archiveRoutePrefix = "/_trasmetto-archive/"

const maxSelectionFormBytes = 4 << 20

func (s *Server) archiveRoutePrefix() string {
	segment := s.archiveSegment
	if segment == "" {
		segment = strings.Trim(archiveRoutePrefix, "/")
	}
	if s.publicPath == "" {
		return "/" + segment + "/"
	}
	return "/" + encodedURLPath(s.publicPath) + "/" + segment + "/"
}

func archivePathURL(publicPath, segment, rel string) string {
	u := "/"
	if p := encodedURLPath(publicPath); p != "" {
		u += p + "/"
	}
	u += segment + "/"
	if r := encodedURLPath(rel); r != "" {
		u += r
	}
	return u
}

type zipTarget struct {
	real string
	name string
}

func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {

	if s.noZip {
		http.NotFound(w, r)
		return
	}
	if s.uploadOnly {
		http.Error(w, "downloads are disabled", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost:
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	targets, archiveName, ok := s.archiveTargets(w, r)
	if !ok {
		return
	}

	total, exceeded := s.targetsSize(r.Context(), targets)
	if s.maxZipBytes > 0 && exceeded {
		subject := "This folder is"
		if r.Method == http.MethodPost {
			subject = "The selected items are"
		}
		msg := fmt.Sprintf("%s larger than the %s zip download limit", subject, config.FormatBytes(s.maxZipBytes))
		w.Header().Set("X-Trasmetto-Zip-Error", msg)
		http.Error(w, msg, http.StatusRequestEntityTooLarge)
		return
	}

	if isArchivePrecheck(r) {
		w.Header().Set("X-Trasmetto-Zip-Size", strconv.FormatInt(total, 10))
		w.WriteHeader(http.StatusOK)
		return
	}

	clearWriteDeadline(w)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDispositionAttachment(archiveName))
	s.logArchive(r, archiveName, targets)
	if r.Method != http.MethodHead {
		logDetailsFrom(r).recordArchive(archiveName, s.archiveItemNames(targets))
	}
	s.streamZip(s.downloadWriter(w), r, targets)
}

// archiveItemNames lists what went into the archive, relative to the root.
func (s *Server) archiveItemNames(targets []zipTarget) []string {
	items := make([]string, 0, len(targets))
	for _, t := range targets {
		item := filepath.ToSlash(relFromAbs(t.real, s.root))
		if item == "." || item == "" {
			item = "/" // the whole served directory
		}
		items = append(items, item)
	}
	return items
}

func (s *Server) logArchive(r *http.Request, archiveName string, targets []zipTarget) {
	items := make([]string, 0, len(targets))
	for _, t := range targets {
		items = append(items, logQuoted(relFromAbs(t.real, s.root)))
	}
	s.logDetail(r, "zipped %s <- %s", logQuoted(archiveName), strings.Join(items, ", "))
}

func (s *Server) archiveTargets(w http.ResponseWriter, r *http.Request) ([]zipTarget, string, bool) {
	if s.fileSetMode {
		return s.fileSetArchiveTargets(w, r)
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, maxSelectionFormBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid selection", http.StatusBadRequest)
			return nil, "", false
		}
		var targets []zipTarget
		for _, item := range r.Form["items"] {
			if t, ok := s.resolveSelectionItem(item); ok {
				targets = append(targets, t)
			}
		}
		if len(targets) == 0 {
			http.Error(w, "no valid items selected", http.StatusBadRequest)
			return nil, "", false
		}
		return targets, randomArchiveName(), true
	}

	rel, err := s.archiveTargetRel(r)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return nil, "", false
	}
	dir, _, err := s.resolvePath(rel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return nil, "", false
	}
	realDir, err := s.realPath(dir)
	if err != nil {
		http.Error(w, "not found", statusForFileError(err))
		return nil, "", false
	}
	info, err := os.Stat(realDir)
	if err != nil {
		http.Error(w, "not found", statusForFileError(err))
		return nil, "", false
	}
	if !info.IsDir() {
		http.Error(w, "not a directory", http.StatusBadRequest)
		return nil, "", false
	}
	return []zipTarget{{real: realDir, name: ""}}, archiveFilename(realDir, s.root), true
}

// fileSetArchiveTargets resolves selections through the -f map, because those
// files live at arbitrary paths rather than under the served root.
func (s *Server) fileSetArchiveTargets(w http.ResponseWriter, r *http.Request) ([]zipTarget, string, bool) {
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, maxSelectionFormBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid selection", http.StatusBadRequest)
			return nil, "", false
		}
		var targets []zipTarget
		for _, item := range r.Form["items"] {
			name := strings.TrimSpace(item)
			if real, ok := s.fileSet[name]; ok {
				targets = append(targets, zipTarget{real: real, name: name})
			}
		}
		if len(targets) == 0 {
			http.Error(w, "no valid items selected", http.StatusBadRequest)
			return nil, "", false
		}
		return targets, randomArchiveName(), true
	}

	targets := make([]zipTarget, 0, len(s.fileEntries))
	for _, item := range s.fileEntries {
		if real, ok := s.fileSet[item.Name]; ok {
			targets = append(targets, zipTarget{real: real, name: item.Name})
		}
	}
	if len(targets) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return nil, "", false
	}
	// There is no folder to name the archive after, only a set of files.
	return targets, "files.zip", true
}

func (s *Server) resolveSelectionItem(item string) (zipTarget, bool) {
	item = strings.TrimSpace(item)
	if item == "" || item == "." {
		return zipTarget{}, false
	}
	full, _, err := s.resolvePath(item)
	if err != nil {
		return zipTarget{}, false
	}
	real, err := s.realPath(full)
	if err != nil || !isWithinRoot(real, s.root) {
		return zipTarget{}, false
	}
	name := strings.Trim(filepath.Base(real), ". ")
	if name == "" {
		return zipTarget{}, false
	}
	return zipTarget{real: real, name: name}, true
}

func isArchivePrecheck(r *http.Request) bool {
	return r.Method == http.MethodHead || r.Header.Get("X-Trasmetto-Precheck") == "1"
}

func (s *Server) archiveTargetRel(r *http.Request) (string, error) {
	escaped := strings.TrimPrefix(r.URL.EscapedPath(), s.archiveRoutePrefix())
	return url.PathUnescape(escaped)
}

func archiveFilename(dir, root string) string {
	base := strings.Trim(filepath.Base(dir), ". ")
	if base == "" || base == string(filepath.Separator) {
		base = "archive"
	}
	return base + ".zip"
}

func randomArchiveName() string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "archive.zip"
	}
	for i := range buf {
		buf[i] = letters[int(buf[i])%len(letters)]
	}
	return string(buf) + ".zip"
}

func (s *Server) targetsSize(ctx context.Context, targets []zipTarget) (int64, bool) {
	var total int64
	exceeded := false
	count := func(_ string, _ string, info fs.FileInfo) error {
		total += info.Size()
		if s.maxZipBytes > 0 && total > s.maxZipBytes {
			exceeded = true
			return errZipLimitExceeded
		}
		return nil
	}
	for _, t := range targets {
		if err := walkTarget(ctx, t, count); err != nil {
			break
		}
	}
	return total, exceeded
}

func (s *Server) streamZip(w http.ResponseWriter, r *http.Request, targets []zipTarget) {
	limited := &limitedWriter{w: w, limit: s.maxZipBytes}
	zw := zip.NewWriter(limited)
	ctx := r.Context()

	add := func(path, entry string, info fs.FileInfo) error {
		if err := addFileToZip(zw, path, entry, info); err != nil {
			if errors.Is(err, errZipLimitExceeded) || isClientDisconnect(err) {
				return err
			}
			s.logDetail(r, "archive skip %s: %v", logQuoted(path), err)
		}
		return nil
	}

	for _, t := range targets {
		if err := walkTarget(ctx, t, add); err != nil {
			break
		}
	}

	limited.allowOverflow = true
	if closeErr := zw.Close(); closeErr != nil && !isClientDisconnect(closeErr) {
		s.logDetail(r, "archive finalize failed: %v", closeErr)
		return
	}
	if limited.exceeded {
		s.logDetail(r, "archive truncated: exceeded max-zip-size")
	}
}

func walkTarget(ctx context.Context, t zipTarget, fn func(path, entry string, info fs.FileInfo) error) error {
	info, err := os.Stat(t.real)
	if err != nil {
		return nil
	}
	if info.Mode().IsRegular() {
		name := t.name
		if name == "" {
			name = filepath.Base(t.real)
		}
		return fn(t.real, name, info)
	}
	if !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(t.real, func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return nil
		}
		if path == t.real {
			return nil
		}
		if isInternalTemp(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if !d.Type().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(t.real, path)
		if relErr != nil {
			return nil
		}
		entry := filepath.ToSlash(rel)
		if t.name != "" {
			entry = t.name + "/" + entry
		}
		di, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		return fn(path, entry, di)
	})
}

var errZipLimitExceeded = errors.New("zip size limit exceeded")

type limitedWriter struct {
	w             io.Writer
	limit         int64
	written       int64
	exceeded      bool
	allowOverflow bool
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.limit <= 0 || l.allowOverflow {
		n, err := l.w.Write(p)
		l.written += int64(n)
		return n, err
	}
	if l.written >= l.limit {
		l.exceeded = true
		return 0, errZipLimitExceeded
	}
	if remaining := l.limit - l.written; int64(len(p)) > remaining {
		n, err := l.w.Write(p[:remaining])
		l.written += int64(n)
		l.exceeded = true
		if err != nil {
			return n, err
		}
		return n, errZipLimitExceeded
	}
	n, err := l.w.Write(p)
	l.written += int64(n)
	return n, err
}

func addFileToZip(zw *zip.Writer, path, name string, info fs.FileInfo) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = name
	header.Method = zip.Deflate

	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(writer, file)
	return err
}
