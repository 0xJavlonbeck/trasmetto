package server

import (
	"bytes"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newAuthServer(readEnabled, writeEnabled bool) *Server {
	return &Server{
		authReadEnabled:  readEnabled,
		authReadUser:     "alice",
		authReadPass:     "s3cret",
		authWriteEnabled: writeEnabled,
		authWriteUser:    "alice",
		authWritePass:    "s3cret",
		authSecret:       bytes.Repeat([]byte("k"), 32),
		logger:           log.New(io.Discard, "", 0),
		loginTemplate:    template.Must(template.New("login").Parse("LOGIN{{if .Error}} ERR:{{.Error}}{{end}}")),
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "CONTENT")
	})
}

func req(method, target string, html bool) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	if html {
		r.Header.Set("Accept", "text/html,application/xhtml+xml")
	}
	return r
}

func TestAuthAllChallengesAndAllows(t *testing.T) {
	h := newAuthServer(true, true).authMiddleware(okHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req(http.MethodGet, "/", false))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("api no-auth status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("must not send a Basic challenge (causes native popup on upload XHR)")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req(http.MethodGet, "/", true))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "LOGIN") {
		t.Fatalf("browser no-auth: status=%d body=%q, want 401 login page", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("browser request must not get a Basic challenge (would show native popup)")
	}

	r := req(http.MethodGet, "/", false)
	r.SetBasicAuth("alice", "s3cret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || rec.Body.String() != "CONTENT" {
		t.Fatalf("valid basic auth status=%d body=%q, want 200 CONTENT", rec.Code, rec.Body.String())
	}

	r = req(http.MethodGet, "/", false)
	r.SetBasicAuth("alice", "nope")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong pass status = %d, want 401", rec.Code)
	}
}

func TestAuthLoginFlowSetsUsableCookie(t *testing.T) {
	s := newAuthServer(true, true)
	h := s.authMiddleware(okHandler())

	form := url.Values{"username": {"alice"}, "password": {"wrong"}, "next": {"/"}}
	rec := postForm(t, h, s.loginRoutePath(), form)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "ERR:Authentication failed") {
		t.Fatalf("bad login: status=%d body=%q", rec.Code, rec.Body.String())
	}

	form = url.Values{"username": {"alice"}, "password": {"s3cret"}, "next": {"/some/dir"}}
	rec = postForm(t, h, s.loginRoutePath(), form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("good login status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/some/dir" {
		t.Fatalf("redirect Location = %q, want /some/dir", got)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a cookie")
	}

	r := req(http.MethodGet, "/", true)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie-auth status = %d, want 200", rec.Code)
	}

	bad := *cookies[0]
	bad.Value = bad.Value + "x"
	r = req(http.MethodGet, "/", true)
	r.AddCookie(&bad)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered cookie status = %d, want 401", rec.Code)
	}
}

func TestAuthUploadScopeKeepsReadsPublic(t *testing.T) {
	h := newAuthServer(false, true).authMiddleware(okHandler())

	if rec := doReq(t, h, req(http.MethodGet, "/file.txt", true)); rec.Code != http.StatusOK {
		t.Fatalf("public GET status = %d, want 200", rec.Code)
	}

	if rec := doReq(t, h, req(http.MethodPost, archiveRoutePrefix, false)); rec.Code != http.StatusOK {
		t.Fatalf("zip POST status = %d, want 200 (download)", rec.Code)
	}
	if rec := doReq(t, h, req(http.MethodPost, "/dir/", false)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("upload no-auth status = %d, want 401", rec.Code)
	}
}

func TestAuthLoginAjaxReturnsStatusNotRedirect(t *testing.T) {
	s := newAuthServer(false, true)
	h := s.authMiddleware(okHandler())

	r := httptest.NewRequest(http.MethodPost, s.loginRoutePath(),
		strings.NewReader(url.Values{"username": {"alice"}, "password": {"s3cret"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Trasmetto-Login", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ajax login status = %d, want 204", rec.Code)
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("ajax login set no cookie")
	}

	r = httptest.NewRequest(http.MethodPost, s.loginRoutePath(),
		strings.NewReader(url.Values{"username": {"alice"}, "password": {"no"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Trasmetto-Login", "1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "LOGIN") {
		t.Fatalf("wrong ajax login: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestWriteAuthPending(t *testing.T) {
	s := newAuthServer(false, true)
	if !s.writeAuthPending(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Fatal("writeAuthPending = false, want true with no session")
	}

	rec := httptest.NewRecorder()
	s.setSessionCookie(rec, httptest.NewRequest(http.MethodGet, "/", nil), scopeWrite)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		r.AddCookie(c)
	}
	if s.writeAuthPending(r) {
		t.Fatal("writeAuthPending = true after a write session, want false")
	}

	if newAuthServer(false, false).writeAuthPending(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Fatal("writeAuthPending = true when uploads are public")
	}
}

func TestAuthAssetsAlwaysPublic(t *testing.T) {
	s := newAuthServer(true, true)
	h := s.authMiddleware(okHandler())
	rec := doReq(t, h, req(http.MethodGet, s.assetRoutePrefix()+"css/x.css", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want 200 (assets public so login page can load)", rec.Code)
	}
}

func doReq(t *testing.T, h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func postForm(t *testing.T, h http.Handler, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestSafeNextRejectsOffSiteTargets(t *testing.T) {
	s := &Server{}
	root := s.safeNext("")

	for _, bad := range []string{
		"//evil.com",
		"/\\evil.com",
		"/\\/evil.com",
		"https://evil.com",
		"http://evil.com",
		"/ok\r\nSet-Cookie: x=1",
	} {
		if got := s.safeNext(bad); got != root {
			t.Errorf("safeNext(%q) = %q, want the local root %q", bad, got, root)
		}
	}

	for _, good := range []string{"/", "/dir", "/a/b%20c"} {
		if got := s.safeNext(good); got != good {
			t.Errorf("safeNext(%q) = %q, want it unchanged", good, got)
		}
	}
}

func TestAuthDoesNotRevealServerOutsideHiddenPath(t *testing.T) {
	s := newAuthServer(true, true)
	s.publicPath = "hidden"
	s.pageTemplate = template.Must(template.New("index.html").Parse("TRASMETTO PAGE"))
	h := s.authMiddleware(okHandler())

	for _, target := range []string{"/", "/admin", "/s.txt", "/_trasmetto-login"} {
		rec := doReq(t, h, req(http.MethodGet, target, true))
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s returned 401, revealing that something protected exists", target)
		}
		if strings.Contains(rec.Body.String(), "LOGIN") {
			t.Errorf("%s served the login page, revealing the server", target)
		}
	}

	rec := doReq(t, h, req(http.MethodGet, "/hidden", true))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "LOGIN") {
		t.Fatalf("/hidden: status=%d body=%q, want 401 + login page", rec.Code, rec.Body.String())
	}

	r := req(http.MethodGet, "/hidden/s.txt", false)
	r.SetBasicAuth("alice", "s3cret")
	if rec := doReq(t, h, r); rec.Code != http.StatusOK {
		t.Fatalf("authed request inside prefix = %d, want 200", rec.Code)
	}
}
