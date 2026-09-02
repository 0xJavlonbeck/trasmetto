package server

import (
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
)

func TestRenderHiddenNotFoundIsGeneric(t *testing.T) {
	recorder := httptest.NewRecorder()

	(&Server{}).renderHiddenNotFound(recorder)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{"Trasmetto", "_trasmetto-assets", "Browse", "Upload"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("hidden 404 body contains %q: %s", forbidden, body)
		}
	}

	if !strings.Contains(body, "<h1>Not Found</h1>") ||
		!strings.Contains(body, "The requested resource was not found on this server.") {
		t.Fatalf("hidden 404 body = %q, want the plain Not Found page", body)
	}
}

func TestIsClientDisconnect(t *testing.T) {
	tests := []error{
		syscall.EPIPE,
		syscall.ECONNRESET,
		errors.New("write tcp: wsasend: An existing connection was forcibly closed by the remote host"),
	}
	for _, err := range tests {
		if !isClientDisconnect(err) {
			t.Errorf("isClientDisconnect(%q) = false, want true", err)
		}
	}

	if isClientDisconnect(errors.New("template: invalid value")) {
		t.Error("isClientDisconnect() suppressed a genuine template error")
	}
}

func TestRenderPagePartialRendersContentFragmentOnly(t *testing.T) {
	tmpl := template.Must(template.New("index.html").Parse(
		`FULL<head>{{template "content" .}}{{define "content"}}FRAGMENT:{{.Current}}{{end}}`,
	))
	s := &Server{pageTemplate: tmpl, logger: log.New(io.Discard, "", 0)}

	full := httptest.NewRecorder()
	s.renderPage(full, nil, http.StatusOK, ".", nil, "", "", false)
	if body := full.Body.String(); !strings.Contains(body, "FULL") || !strings.Contains(body, "<head>") {
		t.Fatalf("full render = %q, want document chrome", body)
	}

	partial := httptest.NewRecorder()
	s.renderPage(partial, nil, http.StatusOK, ".", nil, "", "", true)
	body := partial.Body.String()
	if strings.Contains(body, "FULL") || strings.Contains(body, "<head>") {
		t.Fatalf("partial render leaked document chrome: %q", body)
	}
	if !strings.HasPrefix(body, "FRAGMENT:") {
		t.Fatalf("partial render = %q, want content fragment only", body)
	}
}
