package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"trasmetto/internal/config"
)

func TestFormatListenErrorFriendlyMessages(t *testing.T) {
	cfg := config.Config{Host: "10.0.0.1", Port: 8000}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"unix addr in use", syscall.EADDRINUSE, "port 8000 - address already in use"},
		{"windows addr in use", wsaEADDRINUSE, "port 8000 - address already in use"},
		{"unix addr unavailable", syscall.EADDRNOTAVAIL, "10.0.0.1 - cannot assign requested address"},
		{"windows addr unavailable", wsaEADDRNOTAVAIL, "10.0.0.1 - cannot assign requested address"},
		{"unix permission", syscall.EACCES, "port 8000 - permission denied"},
		{"windows permission", wsaEACCES, "port 8000 - permission denied"},
	}
	for _, tc := range tests {

		wrapped := &net.OpError{Op: "listen", Net: "tcp", Err: &os.SyscallError{Syscall: "bind", Err: tc.err}}
		if got := formatListenError(wrapped, cfg); got != tc.want {
			t.Errorf("%s: formatListenError = %q, want %q", tc.name, got, tc.want)
		}
	}

	other := fmt.Errorf("something else")
	if got := formatListenError(other, cfg); got != "something else" {
		t.Errorf("unrelated error = %q, want it passed through", got)
	}
}

func TestPrintExitingHasNoEscapesOnWindows(t *testing.T) {

	var b strings.Builder
	printExiting(&b, false)
	if !strings.Contains(b.String(), "Exiting...") {
		t.Fatalf("printExiting = %q, want it to mention Exiting...", b.String())
	}
}
