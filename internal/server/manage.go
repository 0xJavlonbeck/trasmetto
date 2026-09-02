package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const manageRoutePrefix = "/_trasmetto-manage/"

// maxManageFormBytes bounds the form that names folders and deletion targets.
const maxManageFormBytes = 1 << 20

func (s *Server) manageRoutePrefix() string {
	segment := s.manageSegment
	if segment == "" {
		segment = strings.Trim(manageRoutePrefix, "/")
	}
	if s.publicPath == "" {
		return "/" + segment + "/"
	}
	return "/" + encodedURLPath(s.publicPath) + "/" + segment + "/"
}

func managePathURL(publicPath, segment, rel string) string {
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

// handleManage creates folders and deletes entries, both only under
// --full-access. Everything it touches is resolved inside the served root.
func (s *Server) handleManage(w http.ResponseWriter, r *http.Request) {
	if !s.fullAccess {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	targetDir, _, err := s.resolveManagePath(r)
	if err != nil {
		if errors.Is(err, errOutsidePublicPath) {
			s.renderHiddenNotFound(w)
			return
		}
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxManageFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	switch r.FormValue("op") {
	case "mkdir":
		s.handleMakeDir(w, r, targetDir)
	case "delete":
		s.handleDelete(w, r)
	default:
		http.Error(w, "unknown operation", http.StatusBadRequest)
	}
}

func (s *Server) handleMakeDir(w http.ResponseWriter, r *http.Request, targetDir string) {
	raw := strings.TrimSpace(r.FormValue("name"))
	if strings.ContainsAny(raw, `/\`) {
		http.Error(w, "folder name cannot contain slashes", http.StatusBadRequest)
		return
	}
	name := safeFilename(raw)
	if name == "" {
		http.Error(w, "folder name is required", http.StatusBadRequest)
		return
	}
	// The user typed this name, so refuse anything we would have to rewrite
	// rather than silently creating a folder under a different name.
	if name != raw {
		http.Error(w, "folder name cannot start or end with a dot or space", http.StatusBadRequest)
		return
	}

	destination := filepath.Join(targetDir, name)
	if !isWithinRoot(destination, s.root) {
		http.Error(w, "invalid folder name", http.StatusBadRequest)
		return
	}
	if _, err := os.Lstat(destination); err == nil {
		http.Error(w, fmt.Sprintf("%q already exists", name), http.StatusConflict)
		return
	}
	if err := os.Mkdir(destination, 0o755); err != nil {
		s.logDetail(r, "mkdir failed %s: %v", logQuoted(name), err)
		http.Error(w, manageErrorMessage(err), statusForFileError(err))
		return
	}

	created := relFromAbs(destination, s.root)
	s.logDetail(r, "created folder %s", logQuoted(created))
	logDetailsFrom(r).recordMkdir(filepath.ToSlash(created))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	items := r.Form["items"]
	if len(items) == 0 {
		http.Error(w, "nothing selected", http.StatusBadRequest)
		return
	}

	var removed []string
	for _, item := range items {
		target, err := s.resolveDeletionTarget(item)
		if err != nil {
			http.Error(w, "invalid selection", http.StatusBadRequest)
			return
		}
		info, err := os.Lstat(target)
		if err != nil {
			http.Error(w, manageErrorMessage(err), statusForFileError(err))
			return
		}
		if err := remove(target, info); err != nil {
			s.logDetail(r, "delete failed %s: %v", logQuoted(item), err)
			http.Error(w, manageErrorMessage(err), statusForFileError(err))
			return
		}
		name := filepath.ToSlash(relFromAbs(target, s.root))
		removed = append(removed, name)
		s.logDetail(r, "deleted %s", logQuoted(name))
	}

	logDetailsFrom(r).recordDelete(removed)
	w.WriteHeader(http.StatusNoContent)
}

func remove(target string, info os.FileInfo) error {
	if info.IsDir() {
		return os.RemoveAll(target)
	}
	return os.Remove(target)
}

// resolveDeletionTarget resolves a selection the same way the archive route
// does: relative to the served root, since that is what the listing checkboxes
// carry. It deliberately does not follow symlinks, so deleting a link removes
// the link and never its target.
func (s *Server) resolveDeletionTarget(item string) (string, error) {
	name := strings.TrimSpace(item)
	if name == "" || name == "." {
		return "", fmt.Errorf("invalid selection")
	}
	candidate, _, err := s.resolvePath(name)
	if err != nil {
		return "", err
	}
	if candidate == filepath.Clean(s.root) {
		return "", fmt.Errorf("refusing to delete the served directory")
	}
	return candidate, nil
}

// resolveManagePath maps the request path to the directory being managed.
func (s *Server) resolveManagePath(r *http.Request) (string, string, error) {
	if !s.withinPublicPath(r) {
		return "", "", errOutsidePublicPath
	}
	trimmed := strings.TrimPrefix(r.URL.EscapedPath(), s.manageRoutePrefix())
	rel, err := url.PathUnescape(trimmed)
	if err != nil {
		return "", "", err
	}
	dir, cleanRel, err := s.resolvePath(rel)
	if err != nil {
		return "", "", err
	}
	real, err := s.realPath(dir)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("not a directory")
	}
	return real, cleanRel, nil
}

// manageErrorMessage keeps server paths out of responses.
func manageErrorMessage(err error) string {
	switch {
	case os.IsPermission(err):
		return "no permission for this operation"
	case os.IsNotExist(err):
		return "it no longer exists"
	case os.IsExist(err):
		return "it already exists"
	}
	return "the operation failed"
}
