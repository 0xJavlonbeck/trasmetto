package server

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// EventLogger writes newline-delimited JSON: one self-contained object per
// line, tagged with its type. That keeps the file tailable, greppable and
// ingestible by log shippers, and an abrupt kill costs at most the last line.
// The sections are recovered by filtering on type, for example
// jq 'select(.type=="download")'.
type EventLogger struct {
	mu      sync.Mutex
	encoder *json.Encoder
	closer  io.Closer
	started bool
	stopped bool
}

func NewEventLogger(w io.Writer) *EventLogger {
	logger := &EventLogger{encoder: json.NewEncoder(w)}
	if closer, ok := w.(io.Closer); ok {
		logger.closer = closer
	}
	return logger
}

type startEvent struct {
	Type    string `json:"type"`
	Time    string `json:"time"`
	Version string `json:"version,omitempty"`
	Root    string `json:"root"`
	URL     string `json:"url"`
	Auth    string `json:"auth"`
	StartConfig
}

// StartConfig carries the options that change what the requests below can do,
// so an old log explains its own 403s, 413s and slow transfers. Everything is
// omitted at its default, keeping an ordinary run's start line short.
type StartConfig struct {
	Mode            string `json:"mode,omitempty"`
	Files           int    `json:"files,omitempty"`
	MaxUpload       string `json:"max_upload,omitempty"`
	MaxZip          string `json:"max_zip,omitempty"`
	MaxDownloadRate string `json:"max_download_rate,omitempty"`
	NoZip           bool   `json:"no_zip,omitempty"`
	AllowReplace    bool   `json:"allow_replace,omitempty"`
	FullAccess      bool   `json:"full_access,omitempty"`
}

type stopEvent struct {
	Type string `json:"type"`
	Time string `json:"time"`
}

// downloadEntry records what was taken, and for a zip, what went into it.
type downloadEntry struct {
	Type       string   `json:"type"`
	Time       string   `json:"time"`
	IP         string   `json:"ip"`
	File       string   `json:"file"`
	Bytes      int64    `json:"bytes"`
	DurationMS float64  `json:"duration_ms"`
	Status     int      `json:"status"`
	UserAgent  string   `json:"user_agent,omitempty"`
	Zip        []string `json:"zip,omitempty"`
}

// uploadEntry records one saved file: its original name and where it landed.
type uploadEntry struct {
	Type       string  `json:"type"`
	Time       string  `json:"time"`
	IP         string  `json:"ip"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Bytes      int64   `json:"bytes"`
	Replaced   bool    `json:"replaced,omitempty"`
	DurationMS float64 `json:"duration_ms"`
	Status     int     `json:"status"`
	UserAgent  string  `json:"user_agent,omitempty"`
}

// manageEvent records a folder creation or a deletion.
type manageEvent struct {
	Type       string   `json:"type"`
	Time       string   `json:"time"`
	IP         string   `json:"ip"`
	Folder     string   `json:"folder,omitempty"`
	Items      []string `json:"items,omitempty"`
	DurationMS float64  `json:"duration_ms"`
	Status     int      `json:"status"`
	UserAgent  string   `json:"user_agent,omitempty"`
}

// requestEvent covers everything that is neither a download nor an upload:
// directory listings, 404s, rejected uploads, auth failures.
type requestEvent struct {
	Type       string  `json:"type"`
	Time       string  `json:"time"`
	IP         string  `json:"ip"`
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	Status     int     `json:"status"`
	BytesIn    int64   `json:"bytes_in,omitempty"`
	BytesOut   int64   `json:"bytes_out,omitempty"`
	DurationMS float64 `json:"duration_ms"`
	UserAgent  string  `json:"user_agent,omitempty"`
}

func (l *EventLogger) writeLine(event any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.encoder.Encode(event)
}

// Start records the configuration this run serves with.
func (l *EventLogger) Start(version, root, url, auth string, cfg StartConfig) {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.started {
		l.mu.Unlock()
		return
	}
	l.started = true
	l.mu.Unlock()

	l.writeLine(startEvent{
		Type:        "START",
		Time:        eventTime(time.Now()),
		Version:     version,
		Root:        root,
		URL:         url,
		Auth:        auth,
		StartConfig: cfg,
	})
}

func (l *EventLogger) addDownload(entry downloadEntry) {
	entry.Type = "download"
	l.writeLine(entry)
}

func (l *EventLogger) addUploads(entries []uploadEntry) {
	for _, entry := range entries {
		entry.Type = "upload"
		l.writeLine(entry)
	}
}

func (l *EventLogger) addManage(kind string, entry manageEvent) {
	entry.Type = kind
	l.writeLine(entry)
}

func (l *EventLogger) addRequest(entry requestEvent) {
	entry.Type = "request"
	l.writeLine(entry)
}

// Stop marks the end of the run.
func (l *EventLogger) Stop() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.stopped = true
	l.mu.Unlock()

	l.writeLine(stopEvent{Type: "STOP", Time: eventTime(time.Now())})
}

// Close writes the stop line if it is missing, then closes the file.
func (l *EventLogger) Close() error {
	if l == nil {
		return nil
	}
	l.Stop()
	l.mu.Lock()
	closer := l.closer
	l.mu.Unlock()
	if closer == nil {
		return nil
	}
	return closer.Close()
}

func eventTime(t time.Time) string {
	_, offset := t.Zone()
	return t.Format("2006-01-02 15:04:05 ") + utcOffsetLabel(offset)
}

// utcOffsetLabel spells the host's zone as UTC+5 rather than +05:00, which
// reads plainly without needing the reader to know zone abbreviations.
func utcOffsetLabel(offsetSeconds int) string {
	if offsetSeconds == 0 {
		return "UTC"
	}
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	if minutes == 0 {
		return fmt.Sprintf("UTC%s%d", sign, hours)
	}
	return fmt.Sprintf("UTC%s%d:%02d", sign, hours, minutes)
}
