// Trasmetto - a cross-platform file transfer server.
// Copyright (C) 2026 javlonbeck
//
// This program is free software: you can redistribute it and/or modify it under
// the terms of the GNU General Public License as published by the Free Software
// Foundation, either version 3 of the License, or (at your option) any later
// version. It is distributed WITHOUT ANY WARRANTY; see the GNU General Public
// License for details: <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"trasmetto"
	"trasmetto/internal/client"
	"trasmetto/internal/config"
	"trasmetto/internal/netutil"
	"trasmetto/internal/server"
	"trasmetto/internal/termcolor"
)

const appVersion = "1.0.0"

func main() {
	cfg, err := config.Parse()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}
	if cfg.ShowVersion {
		fmt.Printf("trasmetto v%s\n", appVersion)
		return
	}
	if cfg.DownloadURL != "" {
		if err := runTransfer(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	logger := log.New(os.Stdout, "", 0)

	app, err := server.New(cfg, logger, trasmetto.StaticFiles)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	color := termcolor.Enabled(cfg.NoColor)
	app.EnableColor(color)

	// Opened before Routes() so the request logger picks it up.
	var events *server.EventLogger
	if cfg.LogFile != "" {
		file, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: open --log: %v\n", err)
			os.Exit(1)
		}
		events = server.NewEventLogger(file)
		defer events.Close()
		app.SetEventLogger(events)
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           app.Routes(logger),
		ErrorLog:          server.HTTPErrorLogger(logger),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
	if cfg.HTTPS {
		httpServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	if err := prepareTLS(httpServer, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", formatListenError(err, cfg))
		os.Exit(1)
	}
	defer listener.Close()

	// With -p 0 the kernel picks the port, so report the one actually bound.
	if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok && cfg.Port != tcpAddr.Port {
		cfg.Port = tcpAddr.Port
		addr = net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))
	}

	scheme := "http"
	if cfg.HTTPS {
		scheme = "https"
	}
	if !cfg.NoBanner {
		printBanner(os.Stdout)
	}
	urlPath := "/"
	if cfg.Path != "" {
		urlPath = "/" + cfg.Path
	}
	urlStr := fmt.Sprintf("%s://%s%s", scheme, addr, urlPath)
	servingLine := fmt.Sprintf("Serving Trasmetto on %s port %d (%s) ...", cfg.Host, cfg.Port, urlStr)
	logger.Printf("Serving Trasmetto on %s port %d (%s) ...", cfg.Host, cfg.Port, termcolor.Wrap(color, termcolor.Cyan, urlStr))

	if urls := netutil.ReachableURLs(cfg.Host, scheme, cfg.Port); len(urls) > 0 {
		logger.Print("Reachable at:")
		for _, u := range urls {

			if cfg.Path != "" {
				u += cfg.Path
			}
			logger.Printf("  %s", termcolor.Wrap(color, termcolor.Cyan, u))
		}
		logger.Print("")
	}
	value := func(text string) string { return termcolor.Wrap(color, termcolor.Cyan, text) }

	if cfg.FileSetMode {
		logger.Printf("Serving %s selected file(s):", value(fmt.Sprint(len(cfg.FileSet))))
		for _, f := range cfg.FileSet {
			logger.Printf("  %s", value(f.Name))
		}
	} else {
		logger.Printf("Serving: %s", value(app.RootDisplay()))
	}
	if cfg.MaxUploadSizeSet {
		if cfg.MaxUploadBytes == 0 {
			logger.Printf("Maximum upload size: %s", value("unlimited"))
		} else {
			logger.Printf("Maximum upload size: %s", value(config.FormatBytes(cfg.MaxUploadBytes)))
		}
	}
	if cfg.NoZip {
		logger.Printf("Folder zip download: %s", value("disabled"))
	} else if cfg.MaxZipSizeSet {
		if cfg.MaxZipBytes == 0 {
			logger.Printf("Maximum zip size: %s", value("unlimited"))
		} else {
			logger.Printf("Maximum zip size: %s", value(config.FormatBytes(cfg.MaxZipBytes)))
		}
	}
	if cfg.MaxDownloadBytes > 0 {
		logger.Printf("Download speed limit: %s per download", value(config.FormatBytes(cfg.MaxDownloadBytes)+"/s"))
	}
	if cfg.Path != "" {
		logger.Printf("Path: %s", value("/"+cfg.Path))
	}
	if cfg.AuthEnabled() {
		switch {
		case cfg.AuthReadEnabled && cfg.AuthWriteEnabled:
			logger.Printf("Authentication: %s", value("required for downloads and uploads"))
		case cfg.AuthWriteEnabled:
			logger.Printf("Authentication: %s", value("required for uploads"))
		case cfg.AuthReadEnabled:
			logger.Printf("Authentication: %s", value("required for downloads"))
		}
		if !cfg.HTTPS {
			logger.Print(termcolor.Wrap(color, termcolor.Yellow, "WARNING: authentication without --https sends credentials in cleartext; add --https."))
		}
	}
	if cfg.DownloadOnly {
		logger.Printf("Mode: %s", value("only-download"))
	}
	if cfg.UploadOnly {
		logger.Printf("Mode: %s", value("only-upload"))
	}
	if cfg.FullAccess {
		logger.Printf("Full access: %s", value("visitors can create folders and delete files"))
	}
	if cfg.AllowReplace {
		logger.Printf("Replace existing files: %s", value("enabled"))
	}
	if cfg.HTTPS && cfg.CertFile == "" {
		logger.Print("HTTPS uses a generated self-signed certificate; browsers will warn until you trust it or provide --cert and --key.")
	}
	if events != nil {
		logPath, _ := filepath.Abs(cfg.LogFile)
		logger.Printf("Log file: %s", value(logPath))
		servedRoot, rootErr := filepath.Abs(app.RootDisplay())
		// In file-set mode only the named files are reachable, so a log inside
		// the working directory is not exposed.
		if !cfg.FileSetMode && rootErr == nil && withinDir(logPath, servedRoot) {
			logger.Print(termcolor.Wrap(color, termcolor.Yellow,
				"WARNING: the log file is inside the served directory; visitors can download it."))
		}
		events.Start(appVersion, app.RootDisplay(), urlStr, authSummary(cfg), startConfig(cfg))
	}
	logger.Print(strings.Repeat("-", len(servingLine)))

	go func() {
		if err := serve(httpServer, listener, cfg); !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("Error: %v", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 2)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	printExiting(os.Stdout, termcolor.IsTerminal(os.Stdout))
	events.Stop()

	go func() {
		<-stop
		os.Exit(1)
	}()

	if err := httpServer.Shutdown(context.Background()); err != nil {
		logger.Printf("graceful shutdown failed: %v", err)
	}
}

func withinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func startConfig(cfg config.Config) server.StartConfig {
	out := server.StartConfig{
		NoZip:        cfg.NoZip,
		AllowReplace: cfg.AllowReplace,
		FullAccess:   cfg.FullAccess,
		Files:        len(cfg.FileSet),
	}
	switch {
	case cfg.DownloadOnly:
		out.Mode = "only-download"
	case cfg.UploadOnly:
		out.Mode = "only-upload"
	}
	if cfg.MaxUploadSizeSet && cfg.MaxUploadBytes > 0 {
		out.MaxUpload = config.FormatBytes(cfg.MaxUploadBytes)
	}
	if cfg.MaxZipSizeSet && cfg.MaxZipBytes > 0 {
		out.MaxZip = config.FormatBytes(cfg.MaxZipBytes)
	}
	if cfg.MaxDownloadBytes > 0 {
		out.MaxDownloadRate = config.FormatBytes(cfg.MaxDownloadBytes) + "/s"
	}
	return out
}

func authSummary(cfg config.Config) string {
	switch {
	case cfg.AuthReadEnabled && cfg.AuthWriteEnabled:
		return "all"
	case cfg.AuthWriteEnabled:
		return "uploads"
	case cfg.AuthReadEnabled:
		return "downloads"
	}
	return "none"
}

func printExiting(w io.Writer, tty bool) {
	if runtime.GOOS == "windows" || !tty {
		fmt.Fprint(w, "\nExiting...\n")
		return
	}
	fmt.Fprint(w, "\r\033[KExiting...\n")
}

func runTransfer(cfg config.Config) error {
	if cfg.UploadPath != "" {
		return client.Upload(client.UploadOptions{
			URL:         cfg.DownloadURL,
			FilePath:    cfg.UploadPath,
			InsecureTLS: cfg.InsecureTLS,
			Auth:        cfg.ClientAuthUpload(),
			Stdout:      os.Stdout,
		})
	}

	_, err := client.Download(client.DownloadOptions{
		URL:         cfg.DownloadURL,
		OutputPath:  cfg.OutputPath,
		InsecureTLS: cfg.InsecureTLS,
		Auth:        cfg.ClientAuthDownload(),
		Stdout:      os.Stdout,
	})
	return err
}

func prepareTLS(httpServer *http.Server, cfg config.Config) error {
	if !cfg.HTTPS {
		return nil
	}
	if cfg.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return err
		}
		httpServer.TLSConfig.Certificates = []tls.Certificate{cert}
		return nil
	}

	cert, err := netutil.SelfSignedCertificate(cfg.Host)
	if err != nil {
		return err
	}
	httpServer.TLSConfig.Certificates = []tls.Certificate{cert}
	return nil
}

func serve(httpServer *http.Server, listener net.Listener, cfg config.Config) error {
	if !cfg.HTTPS {
		return httpServer.Serve(listener)
	}
	return httpServer.ServeTLS(listener, "", "")
}

const (
	wsaEACCES        = syscall.Errno(10013)
	wsaEADDRINUSE    = syscall.Errno(10048)
	wsaEADDRNOTAVAIL = syscall.Errno(10049)
)

func formatListenError(err error, cfg config.Config) string {
	switch {
	case errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, wsaEADDRINUSE):
		return fmt.Sprintf("port %d - address already in use", cfg.Port)
	case errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, wsaEADDRNOTAVAIL):
		return fmt.Sprintf("%s - cannot assign requested address", cfg.Host)
	case errors.Is(err, syscall.EACCES) || errors.Is(err, wsaEACCES):
		return fmt.Sprintf("port %d - permission denied", cfg.Port)
	default:
		return err.Error()
	}
}

func printBanner(w io.Writer) {
	fmt.Fprint(w,
		" _                                _   _        \n"+
			"| |_ _ __ __ _ ___ _ __ ___   ___| |_| |_ ___  \n"+
			"| __| '__/ _` / __| '_ ` _ \\ / _ \\ __| __/ _ \\ \n"+
			"| |_| | | (_| \\__ \\ | | | | |  __/ |_| || (_) |\n"+
			" \\__|_|  \\__,_|___/_| |_| |_|\\___|\\__|\\__\\___/ \n"+
			"                                               \n")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "v"+appVersion)
	fmt.Fprintln(w, "author: javlonbeck")
	fmt.Fprintln(w)
}
