package client

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	downloadBarWidth       = 28
	progressUpdateInterval = 150 * time.Millisecond
)

type downloadProgress struct {
	output      io.Writer
	url         string
	statusCode  int
	total       int64
	downloaded  int64
	startedAt   time.Time
	lastUpdate  time.Time
	lastLineLen int
	interactive bool
	started     bool
}

func newDownloadProgress(output io.Writer, url string, statusCode int, total int64) *downloadProgress {
	if output == nil {
		return nil
	}
	return &downloadProgress{
		output:      output,
		url:         url,
		statusCode:  statusCode,
		total:       total,
		interactive: isTerminal(output),
	}
}

func (p *downloadProgress) start(destination string) {
	if p == nil {
		return
	}
	p.started = true
	p.startedAt = time.Now()
	p.lastUpdate = p.startedAt

	fmt.Fprintf(p.output, "Downloading: %s\n", p.url)
	fmt.Fprintf(p.output, "Response: HTTP %d %s\n", p.statusCode, http.StatusText(p.statusCode))
	if p.total >= 0 {
		fmt.Fprintf(p.output, "Saving to: %s (%s)\n", destination, formatTransferBytes(p.total))
	} else {
		fmt.Fprintf(p.output, "Saving to: %s (size unknown)\n", destination)
	}
	if p.interactive {
		p.render(time.Now())
	}
}

func (p *downloadProgress) Write(data []byte) (int, error) {
	p.downloaded += int64(len(data))
	now := time.Now()
	if p.interactive && now.Sub(p.lastUpdate) >= progressUpdateInterval {
		p.render(now)
		p.lastUpdate = now
	}
	return len(data), nil
}

func (p *downloadProgress) finish(destination string) {
	if p == nil {
		return
	}
	now := time.Now()
	p.render(now)
	if p.interactive {
		fmt.Fprintln(p.output)
	}
	fmt.Fprintf(
		p.output,
		"Saved: %s (%s in %s)\n",
		destination,
		formatTransferBytes(p.downloaded),
		formatTransferDuration(now.Sub(p.startedAt)),
	)
}

func (p *downloadProgress) abort() {
	if p == nil || !p.started || !p.interactive {
		return
	}
	fmt.Fprintln(p.output)
}

func (p *downloadProgress) render(now time.Time) {
	line := p.progressLine(now)
	if p.interactive {
		padding := p.lastLineLen - len(line)
		if padding < 0 {
			padding = 0
		}
		fmt.Fprintf(p.output, "\r%s%s", line, strings.Repeat(" ", padding))
		p.lastLineLen = len(line)
		return
	}
	fmt.Fprintln(p.output, line)
}

func (p *downloadProgress) progressLine(now time.Time) string {
	elapsed := now.Sub(p.startedAt)
	rate := formatTransferRate(p.downloaded, elapsed)
	duration := formatTransferDuration(elapsed)
	if p.total < 0 {
		return fmt.Sprintf("[receiving] %s  %s  %s", formatTransferBytes(p.downloaded), rate, duration)
	}

	ratio := float64(1)
	if p.total > 0 {
		ratio = float64(p.downloaded) / float64(p.total)
	}
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	percent := int(ratio * 100)
	return fmt.Sprintf(
		"[%s] %3d%%  %s/%s  %s  %s",
		downloadBar(ratio),
		percent,
		formatTransferBytes(p.downloaded),
		formatTransferBytes(p.total),
		rate,
		duration,
	)
}

func downloadBar(ratio float64) string {
	filled := int(ratio * downloadBarWidth)
	if filled >= downloadBarWidth {
		return strings.Repeat("=", downloadBarWidth)
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("=", filled) + ">" + strings.Repeat(".", downloadBarWidth-filled-1)
}

func formatTransferBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}

	value := float64(bytes)
	unit := "B"
	for _, candidate := range []string{"KB", "MB", "GB", "TB"} {
		value /= 1024
		unit = candidate
		if value < 1024 || candidate == "TB" {
			break
		}
	}

	precision := 2
	if value >= 100 {
		precision = 0
	} else if value >= 10 {
		precision = 1
	}
	return fmt.Sprintf("%.*f %s", precision, value, unit)
}

func formatTransferRate(bytes int64, elapsed time.Duration) string {
	if bytes <= 0 || elapsed <= 0 {
		return "-- B/s"
	}
	perSecond := int64(float64(bytes) / elapsed.Seconds())
	if perSecond < 0 {
		perSecond = 0
	}
	return formatTransferBytes(perSecond) + "/s"
}

func formatTransferDuration(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	totalSeconds := int64(elapsed / time.Second)
	hours := totalSeconds / 3600
	minutes := totalSeconds % 3600 / 60
	seconds := totalSeconds % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func isTerminal(output io.Writer) bool {
	file, ok := output.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
