package server

import (
	"context"
	"net/http"
)

// logDetails lets a handler tell the request logger what it actually did:
// which entries went into a zip, and which uploads landed under which names.
// It travels in the request context because a selection can name more files
// than a response header could carry.
type logDetails struct {
	archiveName  string
	archiveItems []string
	uploaded     []uploadRecord
	downloadFile string
	isDownload   bool
	createdDir   string
	deleted      []string
}

// uploadRecord pairs the name a client sent with the name it was saved under.
type uploadRecord struct {
	from     string
	to       string
	bytes    int64
	replaced bool
}

type logDetailsKey struct{}

func withLogDetails(r *http.Request) (*http.Request, *logDetails) {
	details := &logDetails{}
	return r.WithContext(context.WithValue(r.Context(), logDetailsKey{}, details)), details
}

func logDetailsFrom(r *http.Request) *logDetails {
	details, _ := r.Context().Value(logDetailsKey{}).(*logDetails)
	return details
}

func (d *logDetails) recordArchive(name string, items []string) {
	if d == nil {
		return
	}
	d.archiveName = name
	d.archiveItems = items
}

func (d *logDetails) recordDownload(file string) {
	if d == nil {
		return
	}
	d.isDownload = true
	d.downloadFile = file
}

func (d *logDetails) recordMkdir(name string) {
	if d == nil {
		return
	}
	d.createdDir = name
}

func (d *logDetails) recordDelete(names []string) {
	if d == nil {
		return
	}
	d.deleted = append(d.deleted, names...)
}

func (d *logDetails) recordUpload(from, to string, bytes int64, replaced bool) {
	if d == nil {
		return
	}
	d.uploaded = append(d.uploaded, uploadRecord{from: from, to: to, bytes: bytes, replaced: replaced})
}
