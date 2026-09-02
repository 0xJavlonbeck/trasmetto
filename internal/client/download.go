package client

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type DownloadOptions struct {
	URL         string
	OutputPath  string
	InsecureTLS bool
	Auth        string
	Stdout      io.Writer
	HTTPClient  *http.Client
}

func Download(opts DownloadOptions) (string, error) {
	parsed, err := parseRemoteURL(opts.URL)
	if err != nil {
		return "", err
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = defaultHTTPClient(opts.InsecureTLS)
	}

	request, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "trasmetto")
	applyAuth(request, parsed, opts.Auth)

	response, err := httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("download failed: HTTP %d", response.StatusCode)
	}

	destination, err := outputPath(opts.OutputPath, response, parsed)
	if err != nil {
		return "", err
	}

	progress := newDownloadProgress(opts.Stdout, parsed.String(), response.StatusCode, response.ContentLength)
	saved, err := saveResponseBody(destination, response.Body, progress)
	if err != nil {
		progress.abort()
		return "", err
	}
	progress.finish(saved)
	return saved, nil
}

func endsWithSeparator(path string) bool {
	if path == "" {
		return false
	}
	last := path[len(path)-1]
	return last == '/' || last == os.PathSeparator
}

func outputPath(rawOutput string, response *http.Response, parsed *url.URL) (string, error) {
	if rawOutput != "" {
		wantsDir := endsWithSeparator(rawOutput)
		info, err := os.Stat(rawOutput)
		if err == nil && info.IsDir() {
			filename, err := responseFilename(response, parsed)
			if err != nil {
				return "", err
			}
			return filepath.Join(rawOutput, filename), nil
		}
		if err == nil && wantsDir {
			return "", fmt.Errorf("output path %q is not a directory", rawOutput)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		// A trailing separator says the user meant a directory, so create it
		// instead of writing a file under that name.
		if wantsDir {
			if err := os.MkdirAll(rawOutput, 0o755); err != nil {
				return "", err
			}
			filename, err := responseFilename(response, parsed)
			if err != nil {
				return "", err
			}
			return filepath.Join(rawOutput, filename), nil
		}
		return rawOutput, nil
	}

	filename, err := responseFilename(response, parsed)
	if err != nil {
		return "", err
	}
	return filename, nil
}

func responseFilename(response *http.Response, parsed *url.URL) (string, error) {
	if disposition := response.Header.Get("Content-Disposition"); disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil {
			if filename := safeFilename(params["filename"]); filename != "" {
				return filename, nil
			}
		}
	}

	filename := safeFilename(path.Base(parsed.EscapedPath()))
	if filename == "" || filename == "." || filename == "/" {
		return "", fmt.Errorf("could not determine filename; use -o")
	}
	return filename, nil
}

func safeFilename(name string) string {
	name, _ = url.PathUnescape(name)
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(filepath.Clean(filepath.FromSlash(name)))
	name = strings.Trim(name, ". ")

	if runtime.GOOS == "windows" && isWindowsReservedName(name) {
		name = "_" + name
	}
	return name
}

func isWindowsReservedName(name string) bool {
	stem := name
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

func saveResponseBody(destination string, body io.Reader, progress *downloadProgress) (string, error) {
	destination = filepath.Clean(destination)
	if destination == "." || destination == string(filepath.Separator) {
		return "", fmt.Errorf("invalid output path %q", destination)
	}

	finalPath, err := nextAvailablePath(destination)
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(finalPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(dir, ".trasmetto-download-*")
	if err != nil {
		return "", err
	}
	progress.start(finalPath)
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()

	reader := body
	if progress != nil {
		reader = io.TeeReader(body, progress)
	}
	if _, err := io.Copy(tmp, reader); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	// Best effort: the file is already complete, so a filesystem that refuses
	// to set timestamps must not fail the download.
	now := time.Now()
	_ = os.Chtimes(tmpName, now, now)
	if err := os.Rename(tmpName, finalPath); err != nil {
		return "", err
	}

	removeTemp = false
	return finalPath, nil
}

func nextAvailablePath(path string) (string, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path, nil
	} else if err != nil {
		return "", err
	}

	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i < 10000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s(%d)%s", stem, i, ext))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("could not find available filename")
}
