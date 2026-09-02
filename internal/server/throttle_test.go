package server

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestThrottledWriterPacesToRate(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	rec := httptest.NewRecorder()

	const rate = 100 << 10
	tw := &throttledWriter{ResponseWriter: rec, rate: rate, chunk: throttleChunk(rate)}

	start := time.Now()
	data := make([]byte, 50<<10)
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < 350*time.Millisecond {
		t.Errorf("50KB at 100KB/s took %v, want >= ~0.5s (not throttled)", elapsed)
	}
	if elapsed > 900*time.Millisecond {
		t.Errorf("50KB at 100KB/s took %v, want ~0.5s (too slow)", elapsed)
	}
	if rec.Body.Len() != len(data) {
		t.Errorf("wrote %d bytes, want %d", rec.Body.Len(), len(data))
	}
}

func TestDownloadWriterUnwrapsWhenNoLimit(t *testing.T) {
	rec := httptest.NewRecorder()
	if got := (&Server{maxDownloadBytes: 0}).downloadWriter(rec); got != rec {
		t.Fatal("with no limit, downloadWriter should return the writer unchanged")
	}
	if _, ok := (&Server{maxDownloadBytes: 1000}).downloadWriter(rec).(*throttledWriter); !ok {
		t.Fatal("with a limit, downloadWriter should wrap in a throttledWriter")
	}
}
