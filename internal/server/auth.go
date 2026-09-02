package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookieName = "trasmetto_session"
	sessionTTL        = 7 * 24 * time.Hour

	scopeRead  = 1 << 0
	scopeWrite = 1 << 1
)

func (s *Server) loginRoutePath() string {
	return pathURL(s.publicPath, "_trasmetto-login")
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, s.assetRoutePrefix()) {
			next.ServeHTTP(w, r)
			return
		}

		if !s.withinPublicPath(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == s.loginRoutePath() {
			s.handleLogin(w, r)
			return
		}

		write := s.isWriteRequest(r)
		if write && !s.authWriteEnabled || !write && !s.authReadEnabled {
			next.ServeHTTP(w, r)
			return
		}
		if s.requestAuthorized(r, write) {
			next.ServeHTTP(w, r)
			return
		}

		if wantsHTML(r) {
			s.renderLogin(w, r, http.StatusUnauthorized, r.URL.RequestURI(), "")
			return
		}
		http.Error(w, "authentication required", http.StatusUnauthorized)
	})
}

func (s *Server) isWriteRequest(r *http.Request) bool {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		return false
	}
	if strings.HasPrefix(r.URL.Path, s.archiveRoutePrefix()) {
		return false
	}
	return true
}

func (s *Server) requestAuthorized(r *http.Request, write bool) bool {
	need := scopeRead
	user, pass := s.authReadUser, s.authReadPass
	if write {
		need = scopeWrite
		user, pass = s.authWriteUser, s.authWritePass
	}
	if s.sessionScopes(r)&need != 0 {
		return true
	}
	if got, gotPass, ok := r.BasicAuth(); ok && credsEqual(got, gotPass, user, pass) {
		return true
	}
	return false
}

func (s *Server) writeAuthPending(r *http.Request) bool {
	if r == nil || !s.authWriteEnabled {
		return false
	}
	return s.sessionScopes(r)&scopeWrite == 0
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	next := s.safeNext(r.FormValue("next"))
	ajax := r.Header.Get("X-Trasmetto-Login") == "1"
	if r.Method != http.MethodPost {
		s.renderLogin(w, r, http.StatusOK, next, "")
		return
	}

	user := r.FormValue("username")
	pass := r.FormValue("password")
	granted := 0
	if s.authReadEnabled && credsEqual(user, pass, s.authReadUser, s.authReadPass) {
		granted |= scopeRead
	}
	if s.authWriteEnabled && credsEqual(user, pass, s.authWriteUser, s.authWritePass) {
		granted |= scopeWrite
	}
	if granted == 0 {
		if ajax {
			http.Error(w, "authentication failed", http.StatusUnauthorized)
			return
		}
		s.renderLogin(w, r, http.StatusUnauthorized, next, "Authentication failed. Check your username and password.")
		return
	}

	s.setSessionCookie(w, r, granted)
	if ajax {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

type loginData struct {
	Title  string
	Error  string
	Next   string
	Action string
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, status int, next, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	data := loginData{
		Title:  "Sign in — Trasmetto",
		Error:  errMsg,
		Next:   next,
		Action: s.loginRoutePath(),
	}
	if err := s.loginTemplate.Execute(w, data); err != nil && !isClientDisconnect(err) {
		s.logDetail(r, "render login failed: %v", err)
	}
}

func (s *Server) safeNext(next string) string {
	if next == "" || strings.ContainsAny(next, "\\\r\n") {
		return pathURL(s.publicPath, ".")
	}
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return pathURL(s.publicPath, ".")
	}
	return next
}

func (s *Server) withinPublicPath(r *http.Request) bool {
	if s.publicPath == "" {
		return true
	}
	path, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/"))
	if err != nil {
		return false
	}
	_, ok := s.stripPublicPath(path)
	return ok
}

func wantsHTML(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func credsEqual(gotUser, gotPass, wantUser, wantPass string) bool {

	gu := sha256.Sum256([]byte(gotUser))
	wu := sha256.Sum256([]byte(wantUser))
	gp := sha256.Sum256([]byte(gotPass))
	wp := sha256.Sum256([]byte(wantPass))
	return subtle.ConstantTimeCompare(gu[:], wu[:]) == 1 &&
		subtle.ConstantTimeCompare(gp[:], wp[:]) == 1
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, scopes int) {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := strconv.Itoa(scopes) + "." + strconv.FormatInt(exp, 10)
	value := payload + "." + s.signSession(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(exp, 0),
	})
}

func (s *Server) sessionScopes(r *http.Request) int {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return 0
	}
	scopesStr, rest, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return 0
	}
	expStr, sig, ok := strings.Cut(rest, ".")
	if !ok {
		return 0
	}
	payload := scopesStr + "." + expStr
	if subtle.ConstantTimeCompare([]byte(sig), []byte(s.signSession(payload))) != 1 {
		return 0
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() >= exp {
		return 0
	}
	scopes, err := strconv.Atoi(scopesStr)
	if err != nil {
		return 0
	}
	return scopes
}

func (s *Server) signSession(payload string) string {
	mac := hmac.New(sha256.New, s.authSecret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
