package client

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func parseRemoteURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("URL host is required")
	}
	return parsed, nil
}

func applyAuth(req *http.Request, parsed *url.URL, auth string) {
	if parsed.User != nil {
		user := parsed.User.Username()
		pass, _ := parsed.User.Password()
		parsed.User = nil
		req.URL.User = nil
		req.SetBasicAuth(user, pass)
		return
	}
	if auth != "" {
		user, pass, _ := strings.Cut(auth, ":")
		req.SetBasicAuth(user, pass)
	}
}

func defaultHTTPClient(insecureTLS bool) *http.Client {
	return &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: insecureTLS, //nolint:gosec // Explicit --insecure mode.
				MinVersion:         tls.VersionTLS12,
			},
		},
	}
}
