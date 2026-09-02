package server

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"trasmetto/internal/termcolor"
)

func HTTPErrorLogger(logger *log.Logger) *log.Logger {
	return log.New(httpErrorWriter{logger: logger}, "", 0)
}

type httpErrorWriter struct {
	logger *log.Logger
}

func (w httpErrorWriter) Write(data []byte) (int, error) {
	for _, prefix := range [][]byte{
		[]byte("http: TLS handshake error"),
		[]byte("http2: server"),
	} {
		if bytes.Contains(data, prefix) {
			return len(data), nil
		}
	}

	message := strings.TrimRight(string(data), "\r\n")
	if message != "" {
		w.logger.Print(message)
	}
	return len(data), nil
}

// logRoutes tells the logger which of the server's own endpoints a request is
// hitting, so it can label them correctly instead of calling every POST an
// upload.
type logRoutes struct {
	assetPrefix   string
	archivePrefix string
	managePrefix  string
	loginPath     string
}

func requestLogger(logger *log.Logger, next http.Handler, routes logRoutes, colorLogs bool, events *EventLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")

		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		isAsset := isAssetRequest(r.URL.Path, routes.assetPrefix)

		isArchive := strings.HasPrefix(r.URL.Path, routes.archivePrefix)
		isManage := routes.managePrefix != "" && strings.HasPrefix(r.URL.Path, routes.managePrefix)
		isLogin := routes.loginPath != "" && r.URL.Path == routes.loginPath
		remoteIP := clientIP(r.RemoteAddr)
		uri := requestURI(r)

		// Only a real upload gets the "started" line; logins, folder
		// operations and archive selections are all POSTs too.
		if !isAsset && !isArchive && !isManage && !isLogin && shouldLogRequestStart(r) {
			logger.Printf("[%s] %-15s | %s | %-4s %s",
				formatLogTime(start),
				remoteIP,
				termcolor.Wrap(colorLogs, termcolor.Dim, "UPL"),
				r.Method,
				uri,
			)
		}

		logStatus := func(status int) {
			if isAsset {
				return
			}
			logger.Printf("[%s] %-15s | %s | %-4s %s",
				currentLogTime(),
				remoteIP,
				termcolor.Status(status, colorLogs),
				r.Method,
				uri,
			)
		}
		ww.onHeader = logStatus

		r, details := withLogDetails(r)
		next.ServeHTTP(ww, r)
		if !ww.wroteHeader {
			// Nothing was ever written; report it now so the request is not lost.
			logStatus(ww.status)
		}

		// UI stylesheets and scripts would bury the actual transfers, so the
		// file log skips them the same way the terminal does.
		if events != nil && !isAsset {
			bytesIn := r.ContentLength
			if bytesIn < 0 {
				bytesIn = 0
			}
			duration := float64(time.Since(start).Microseconds()) / 1000
			now := eventTime(time.Now())

			switch {
			case details.createdDir != "":
				events.addManage("mkdir", manageEvent{
					Time: now, IP: remoteIP, Folder: details.createdDir,
					DurationMS: duration, Status: ww.status, UserAgent: r.UserAgent(),
				})

			case len(details.deleted) > 0:
				events.addManage("delete", manageEvent{
					Time: now, IP: remoteIP, Items: details.deleted,
					DurationMS: duration, Status: ww.status, UserAgent: r.UserAgent(),
				})

			case len(details.uploaded) > 0:
				entries := make([]uploadEntry, 0, len(details.uploaded))
				for _, saved := range details.uploaded {
					entries = append(entries, uploadEntry{
						Time: now, IP: remoteIP,
						From: saved.from, To: saved.to,
						Bytes: saved.bytes, Replaced: saved.replaced, DurationMS: duration,
						Status: ww.status, UserAgent: r.UserAgent(),
					})
				}
				events.addUploads(entries)

			case details.archiveName != "":
				events.addDownload(downloadEntry{
					Time: now, IP: remoteIP,
					File: details.archiveName, Bytes: ww.bytes,
					DurationMS: duration, Status: ww.status, UserAgent: r.UserAgent(),
					Zip: details.archiveItems,
				})

			case details.isDownload:
				events.addDownload(downloadEntry{
					Time: now, IP: remoteIP,
					File: details.downloadFile, Bytes: ww.bytes,
					DurationMS: duration, Status: ww.status, UserAgent: r.UserAgent(),
				})

			default:
				events.addRequest(requestEvent{
					Time: now, IP: remoteIP,
					Method: r.Method, Path: uri, Status: ww.status,
					BytesIn: bytesIn, BytesOut: ww.bytes,
					DurationMS: duration, UserAgent: r.UserAgent(),
				})
			}
		}

	})
}

// logDetail prints a per-request note in the same columns as the status line,
// so with several clients connected you can tell who did what.
func (s *Server) logDetail(r *http.Request, format string, args ...any) {
	ip := "-"
	if r != nil {
		ip = clientIP(r.RemoteAddr)
	}
	s.logger.Printf("[%s] %-15s | %s",
		currentLogTime(),
		ip,
		fmt.Sprintf(format, args...),
	)
}

func shouldLogRequestStart(r *http.Request) bool {
	return r.Method == http.MethodPost
}

func isAssetRequest(path string, assetPrefix string) bool {
	return strings.HasPrefix(path, assetPrefix)
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	bytes       int64
	// onHeader fires once, when the status is committed. For a download that
	// is before the body streams, so the line appears immediately.
	onHeader func(status int)
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	if w.onHeader != nil {
		w.onHeader(status)
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func requestURI(r *http.Request) string {
	if r.URL == nil {
		return sanitizeLogValue(r.RequestURI)
	}
	uri := r.URL.Path
	if uri == "" {
		uri = r.URL.EscapedPath()
	}
	if r.URL.RawQuery != "" {
		query := r.URL.RawQuery
		if decoded, err := url.QueryUnescape(query); err == nil {
			query = decoded
		}
		uri += "?" + query
	}
	if uri == "" {
		return sanitizeLogValue(r.RequestURI)
	}
	return sanitizeLogValue(uri)
}

func sanitizeLogValue(s string) string {
	const hex = "0123456789abcdef"

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			b.WriteString(`\x`)
			b.WriteByte(hex[byte(r)>>4])
			b.WriteByte(hex[byte(r)&0x0f])
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func formatLogTime(t time.Time) string {
	return t.Format("02/01/2006 15:04:05")
}

func currentLogTime() string {
	return formatLogTime(time.Now())
}

func logQuoted(value string) string {
	const hex = "0123456789abcdef"

	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				b.WriteString(`\x`)
				b.WriteByte(hex[byte(r)>>4])
				b.WriteByte(hex[byte(r)&0x0f])
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
