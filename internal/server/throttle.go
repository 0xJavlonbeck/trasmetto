package server

import (
	"net/http"
	"time"
)

func (s *Server) downloadWriter(w http.ResponseWriter) http.ResponseWriter {
	if s.maxDownloadBytes <= 0 {
		return w
	}
	return &throttledWriter{ResponseWriter: w, rate: s.maxDownloadBytes, chunk: throttleChunk(s.maxDownloadBytes)}
}

func throttleChunk(rate int64) int {
	chunk := rate / 16
	if chunk < 4<<10 {
		chunk = 4 << 10
	}
	if chunk > 64<<10 {
		chunk = 64 << 10
	}
	return int(chunk)
}

type throttledWriter struct {
	http.ResponseWriter
	rate  int64
	chunk int
	start time.Time
	sent  int64
}

func (t *throttledWriter) Write(p []byte) (int, error) {
	if t.start.IsZero() {
		t.start = time.Now()
	}
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > t.chunk {
			n = t.chunk
		}
		written, err := t.ResponseWriter.Write(p[:n])
		total += written
		t.sent += int64(written)
		t.pace()
		if err != nil {
			return total, err
		}
		p = p[n:]
	}
	return total, nil
}

func (t *throttledWriter) pace() {
	expected := time.Duration(float64(t.sent) / float64(t.rate) * float64(time.Second))
	if actual := time.Since(t.start); expected > actual {
		time.Sleep(expected - actual)
	}
}

func (t *throttledWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }
