package server

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"trasmetto/internal/config"
)

const (
	multipartOverhead = 10 << 20
)

var errUploadTooLarge = errors.New("upload exceeds configured size limit")

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	clearWriteDeadline(w)

	if s.downloadOnly {
		http.Error(w, "uploads are disabled", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	targetDir, _, err := s.resolveURLPath(r)
	if err != nil {
		if errors.Is(err, errOutsidePublicPath) {
			s.renderHiddenNotFound(w)
			return
		}
		http.Error(w, fmt.Sprintf("invalid upload path: %v", err), http.StatusBadRequest)
		return
	}

	realTargetDir, err := s.realPath(targetDir)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid upload path: %v", err), statusForFileError(err))
		return
	}

	info, err := os.Stat(realTargetDir)
	if err != nil || !info.IsDir() {
		if err != nil {
			http.Error(w, fmt.Sprintf("upload target is not accessible: %v", err), statusForFileError(err))
			return
		}
		http.Error(w, "upload target is not a directory", http.StatusBadRequest)
		return
	}

	if s.maxUploadBytes > 0 && r.ContentLength > s.maxUploadBytes+multipartOverhead {
		message := uploadLimitMessage(s.maxUploadBytes)
		s.logDetail(r, "upload failed: %s", message)
		http.Error(w, message, http.StatusRequestEntityTooLarge)
		return
	}
	if s.maxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.maxUploadBytes+multipartOverhead)
	}
	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, fmt.Sprintf("upload parse failed: %v", err), http.StatusBadRequest)
		return
	}

	budget := uploadBudget{
		enabled:   s.maxUploadBytes > 0,
		remaining: s.maxUploadBytes,
	}
	filesSaved := 0
	var savedNames []string

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			status, message := uploadReadError(err, s.maxUploadBytes)
			http.Error(w, message, status)
			return
		}
		if part.FormName() != "files" || part.FileName() == "" {
			_ = part.Close()
			continue
		}

		filename := part.FileName()
		savedPath, replaced, err := s.saveUploadPart(realTargetDir, filename, part, &budget)
		_ = part.Close()
		if err != nil {
			status := statusForFileError(err)
			message := saveErrorMessage(err)
			var maxBytesErr *http.MaxBytesError
			if errors.Is(err, errUploadTooLarge) || errors.As(err, &maxBytesErr) {
				status = http.StatusRequestEntityTooLarge
				message = uploadLimitMessage(s.maxUploadBytes)
			}
			// The server log keeps the underlying error; the client gets a
			// message that does not disclose server paths.
			s.logDetail(r, "upload failed %q: %v", filename, err)
			http.Error(w, fmt.Sprintf("upload failed for %q: %s", filename, message), status)
			return
		}
		filesSaved++
		savedNames = append(savedNames, filepath.Base(savedPath))
		var savedSize int64
		if info, err := os.Stat(savedPath); err == nil {
			savedSize = info.Size()
		}
		logDetailsFrom(r).recordUpload(filename, relFromAbs(savedPath, s.root), savedSize, replaced)
		suffix := ""
		if replaced {
			suffix = " (replaced)"
		}
		s.logDetail(r, "uploaded %s -> %s%s", logQuoted(filename), logQuoted(relFromAbs(savedPath, s.root)), suffix)
	}

	if filesSaved == 0 {
		http.Error(w, "no files selected", http.StatusBadRequest)
		return
	}

	encoded := make([]string, len(savedNames))
	for i, name := range savedNames {
		encoded[i] = url.QueryEscape(name)
	}
	w.Header().Set("X-Trasmetto-Saved", strings.Join(encoded, ","))
	w.WriteHeader(http.StatusNoContent)
}

func uploadReadError(err error, limit int64) (int, string) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return http.StatusRequestEntityTooLarge, uploadLimitMessage(limit)
	}
	if isTimeoutError(err) {
		return http.StatusRequestTimeout, "upload timed out while reading request body"
	}
	return http.StatusBadRequest, fmt.Sprintf("upload parse failed: %v", err)
}

// saveErrorMessage describes a failed save without echoing the server's paths.
func saveErrorMessage(err error) string {
	switch {
	case os.IsPermission(err):
		return "no write permission in this directory"
	case os.IsExist(err):
		return "a file with that name already exists"
	case errors.Is(err, os.ErrNotExist):
		return "the upload directory no longer exists"
	case isTimeoutError(err):
		return "the upload timed out"
	}
	return "the file could not be saved"
}

func uploadLimitMessage(limit int64) string {
	if limit <= 0 {
		return "upload exceeds configured size limit"
	}
	return fmt.Sprintf("upload limit %s exceeded", config.FormatBytes(limit))
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// saveUploadPart writes one part and reports whether it overwrote a file that
// was already there, which only happens under --allow-replace.
func (s *Server) saveUploadPart(targetDir string, filename string, src io.Reader, budget *uploadBudget) (string, bool, error) {
	name := safeFilename(filename)
	if name == "" {
		return "", false, errors.New("empty filename")
	}

	destination, _, err := s.resolvePath(filepath.Join(relFromAbs(targetDir, s.root), name))
	if err != nil {
		return "", false, err
	}

	if s.allowReplace {
		_, statErr := os.Lstat(destination)
		replaced := statErr == nil
		saved, err := s.saveStreamReplacingExisting(destination, src, budget)
		return saved, replaced && err == nil, err
	}
	saved, err := s.saveStreamWithoutOverwrite(nextAvailablePath, destination, src, budget)
	return saved, false, err
}

type destinationSelector func(string) (string, error)

func (s *Server) saveStreamWithoutOverwrite(selectPath destinationSelector, destination string, src io.Reader, budget *uploadBudget) (string, error) {

	var dst *os.File
	var finalPath string
	for attempts := 0; ; attempts++ {
		candidate, err := selectPath(destination)
		if err != nil {
			return "", err
		}
		if !isWithinRoot(candidate, s.root) {
			return "", fmt.Errorf("path escapes root")
		}

		file, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			dst = file
			finalPath = candidate
			break
		}
		if errors.Is(err, os.ErrExist) {
			if attempts < 10000 {
				continue
			}
			return "", fmt.Errorf("file already exists")
		}
		return "", err
	}

	removeOnError := true
	defer func() {
		if removeOnError {
			_ = os.Remove(finalPath)
		}
	}()

	if err := copyUpload(dst, src, budget); err != nil {
		_ = dst.Close()
		return "", err
	}
	if err := dst.Close(); err != nil {
		return "", err
	}

	removeOnError = false
	return finalPath, nil
}

func (s *Server) saveStreamReplacingExisting(destination string, src io.Reader, budget *uploadBudget) (string, error) {
	if !isWithinRoot(destination, s.root) {
		return "", fmt.Errorf("path escapes root")
	}

	if realDestination, err := filepath.EvalSymlinks(destination); err == nil {
		if !isWithinRoot(realDestination, s.root) {
			return "", fmt.Errorf("path escapes root through symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	dir := filepath.Dir(destination)
	tmp, err := os.CreateTemp(dir, uploadTempPrefix+"*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()

	if err := copyUpload(tmp, src, budget); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, destination); err != nil {
		return "", err
	}

	removeTemp = false
	return destination, nil
}

type uploadBudget struct {
	enabled   bool
	remaining int64
}

func copyUpload(dst io.Writer, src io.Reader, budget *uploadBudget) error {
	if budget == nil || !budget.enabled {
		_, err := io.Copy(dst, src)
		return err
	}
	if budget.remaining < 0 {
		return errUploadTooLarge
	}

	limited := &io.LimitedReader{R: src, N: budget.remaining + 1}
	written, err := io.Copy(dst, limited)
	if written > budget.remaining {
		budget.remaining = 0
		return errUploadTooLarge
	}
	budget.remaining -= written
	return err
}

func nextAvailablePath(path string) (string, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path, nil
	} else if err != nil {
		return "", err
	}

	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i < 10000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s(%d)%s", stem, i, ext))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("could not find available filename")
}
