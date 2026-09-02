package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// decodeLines parses newline-delimited JSON, failing on the first bad line.
func decodeLines(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d is not valid JSON: %v\n%s", i+1, err, line)
		}
		entries = append(entries, entry)
	}
	return entries
}

func ofType(entries []map[string]any, kind string) []map[string]any {
	var out []map[string]any
	for _, entry := range entries {
		if entry["type"] == kind {
			out = append(out, entry)
		}
	}
	return out
}

func TestEventLoggerTagsEachLine(t *testing.T) {
	var buf bytes.Buffer
	events := NewEventLogger(&buf)
	events.Start("1.0.0", "/srv", "http://0.0.0.0:8000/", "all", StartConfig{})
	events.addDownload(downloadEntry{Time: "t1", IP: "10.0.0.1", File: "top.txt", Bytes: 9, Status: 200})
	events.addDownload(downloadEntry{Time: "t2", IP: "10.0.0.2", File: "docs.zip", Bytes: 154, Status: 200, Zip: []string{"docs"}})
	events.addUploads([]uploadEntry{
		{Time: "t3", IP: "10.0.0.3", From: "report.pdf", To: "docs/report(1).pdf", Bytes: 12, Status: 204},
		{Time: "t4", IP: "10.0.0.3", From: "notes.txt", To: "docs/notes.txt", Bytes: 3, Status: 204},
	})
	events.addRequest(requestEvent{Time: "t5", IP: "10.0.0.4", Method: "GET", Path: "/nope", Status: 404})
	events.Stop()

	entries := decodeLines(t, buf.Bytes())
	if len(entries) != 7 {
		t.Fatalf("got %d lines, want 7", len(entries))
	}
	if got := len(ofType(entries, "download")); got != 2 {
		t.Errorf("download lines = %d, want 2", got)
	}
	if got := len(ofType(entries, "upload")); got != 2 {
		t.Errorf("upload lines = %d, want 2 (one per saved file)", got)
	}
	if got := len(ofType(entries, "request")); got != 1 {
		t.Errorf("request lines = %d, want 1", got)
	}
	if entries[0]["type"] != "START" || entries[len(entries)-1]["type"] != "STOP" {
		t.Errorf("start/stop should bracket the run: %v ... %v", entries[0]["type"], entries[len(entries)-1]["type"])
	}

	zips := ofType(entries, "download")
	if zip, ok := zips[1]["zip"].([]any); !ok || len(zip) != 1 || zip[0] != "docs" {
		t.Errorf("zip contents missing: %v", zips[1])
	}
	uploads := ofType(entries, "upload")
	if uploads[0]["from"] != "report.pdf" || uploads[0]["to"] != "docs/report(1).pdf" {
		t.Errorf("upload lost from/to: %v", uploads[0])
	}
}

func TestStartLineOmitsDefaultConfig(t *testing.T) {
	var plain, configured bytes.Buffer
	NewEventLogger(&plain).Start("1.0.0", "/srv", "http://h:8000/", "none", StartConfig{})
	NewEventLogger(&configured).Start("1.0.0", "/srv", "http://h:8000/", "none", StartConfig{
		Mode: "only-download", MaxUpload: "10MB", NoZip: true,
	})

	start := decodeLines(t, plain.Bytes())[0]
	for _, key := range []string{"mode", "files", "max_upload", "max_zip", "max_download_rate", "no_zip", "allow_replace"} {
		if _, ok := start[key]; ok {
			t.Errorf("default run should omit %q", key)
		}
	}

	start = decodeLines(t, configured.Bytes())[0]
	if start["mode"] != "only-download" || start["max_upload"] != "10MB" || start["no_zip"] != true {
		t.Errorf("configured run lost its settings: %v", start)
	}
}

func TestUTCOffsetLabel(t *testing.T) {
	cases := map[int]string{
		0:              "UTC",
		5 * 3600:       "UTC+5",
		-4 * 3600:      "UTC-4",
		5*3600 + 1800:  "UTC+5:30",
		-3*3600 - 1800: "UTC-3:30",
		13 * 3600:      "UTC+13",
	}
	for offset, want := range cases {
		if got := utcOffsetLabel(offset); got != want {
			t.Errorf("utcOffsetLabel(%d) = %q, want %q", offset, got, want)
		}
	}
}
