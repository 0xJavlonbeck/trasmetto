package config

import (
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultUploadLimit = 0

const (
	generatedPathLength   = 6
	generatedPathAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
)

type Config struct {
	Host              string
	Port              int
	Root              string
	Path              string
	NoBanner          bool
	NoColor           bool
	HTTPS             bool
	CertFile          string
	KeyFile           string
	DownloadURL       string
	OutputPath        string
	LogFile           string
	UploadPath        string
	InsecureTLS       bool
	UploadOnly        bool
	DownloadOnly      bool
	AllowReplace      bool
	FullAccess        bool
	MaxUploadBytes    int64
	MaxUploadSizeSet  bool
	MaxDownloadBytes  int64
	NoZip             bool
	MaxZipBytes       int64
	MaxZipSizeSet     bool
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	ShowVersion       bool
	Auth              string
	AuthUpload        string

	AuthReadEnabled  bool
	AuthReadUser     string
	AuthReadPass     string
	AuthWriteEnabled bool
	AuthWriteUser    string
	AuthWritePass    string
	Files            []string
	FileSet          []FileEntry
	FileSetMode      bool
	RootSet          bool
}

type FileEntry struct {
	Name string
	Real string
	Size int64
}

type stringListValue []string

func (v *stringListValue) String() string { return strings.Join(*v, ", ") }
func (v *stringListValue) Set(s string) error {
	*v = append(*v, s)
	return nil
}

const (
	defaultMaxUploadSize   = "10GB"
	defaultMaxZipSize      = "5GB"
	defaultMaxDownloadRate = "0"
)

func (cfg Config) AuthEnabled() bool {
	return cfg.AuthReadEnabled || cfg.AuthWriteEnabled
}

func registerFlags(fs *flag.FlagSet, cfg *Config, maxUploadSize, maxZipSize, maxDownloadRate *string, idleTimeoutMinutes, readHeaderTimeoutSeconds *int) {
	fs.BoolVar(&cfg.ShowVersion, "v", false, "print version and exit")
	fs.BoolVar(&cfg.ShowVersion, "version", false, "print version and exit")
	fs.StringVar(&cfg.Host, "i", "0.0.0.0", "IP address to host on")
	fs.StringVar(&cfg.Host, "ip", "0.0.0.0", "IP address to host on")
	fs.IntVar(&cfg.Port, "p", 8000, "TCP port to host on")
	fs.IntVar(&cfg.Port, "port", 8000, "TCP port to host on")
	fs.StringVar(&cfg.Root, "d", ".", "directory to serve")
	fs.StringVar(&cfg.Root, "dir", ".", "directory to serve")
	fs.StringVar(&cfg.Path, "path", "", "hidden URL path prefix required to access the uploader; use --path with no value to generate a random one")
	fs.BoolVar(&cfg.NoBanner, "no-banner", false, "do not print the startup banner")
	fs.BoolVar(&cfg.NoColor, "no-color", false, "disable coloured terminal output")
	fs.StringVar(&cfg.LogFile, "log", "", "save logs to file (defaults to ./trasmetto-<date>-<time>.log)")
	fs.BoolVar(&cfg.HTTPS, "https", false, "serve over HTTPS")
	fs.StringVar(&cfg.CertFile, "cert", "", "TLS certificate file for HTTPS; if omitted with -https, a self-signed certificate is generated")
	fs.StringVar(&cfg.KeyFile, "key", "", "TLS private key file for HTTPS; if omitted with -https, a self-signed certificate is generated")
	fs.StringVar(&cfg.DownloadURL, "u", "", "remote URL to download from or upload to, then exit")
	fs.StringVar(&cfg.DownloadURL, "url", "", "remote URL to download from or upload to, then exit")
	fs.StringVar(&cfg.OutputPath, "o", "", "output file path or directory for -u downloads")
	fs.StringVar(&cfg.OutputPath, "outfile", "", "output file path or directory for -u downloads")
	fs.StringVar(&cfg.UploadPath, "upload", "", "local file to upload to the -u URL")
	fs.BoolVar(&cfg.InsecureTLS, "insecure", false, "allow insecure HTTPS certificates for -u transfers")
	fs.BoolVar(&cfg.UploadOnly, "only-upload", false, "allow browsing and uploads, but disable downloads")
	fs.BoolVar(&cfg.DownloadOnly, "only-download", false, "allow browsing and downloads, but disable uploads")
	fs.BoolVar(&cfg.AllowReplace, "allow-replace", false, "replace existing files instead of saving as file(1).txt")
	fs.BoolVar(&cfg.FullAccess, "full-access", false, "let visitors create folders and delete files from the browser")
	fs.StringVar(maxUploadSize, "max-upload-size", *maxUploadSize, "maximum total upload size per request, for example 500MB, 2GB, or 0 for unlimited")
	fs.BoolVar(&cfg.NoZip, "no-zip", false, "disable downloading a folder as a zip archive")
	fs.StringVar(maxZipSize, "max-zip-size", *maxZipSize, "refuse to zip a folder larger than this, for example 2GB, 10GB, or 0 for unlimited")
	fs.StringVar(maxDownloadRate, "max-download-rate", *maxDownloadRate, "throttle each download to this speed per second, for example 1MB or 500KB, or 0 for unlimited")
	fs.IntVar(idleTimeoutMinutes, "idle-timeout", *idleTimeoutMinutes, "maximum time in minutes to keep idle connections open")
	fs.IntVar(readHeaderTimeoutSeconds, "read-header-timeout", *readHeaderTimeoutSeconds, "maximum time in seconds to read request headers")
	fs.StringVar(&cfg.Auth, "auth", "", "require login for everything as user:pass (also sent to the -u server)")
	fs.StringVar(&cfg.AuthUpload, "auth-upload", "", "require login (user:pass) for uploads only; in client mode, use the same flag to log in")
	fs.Var((*stringListValue)(&cfg.Files), "f", "serve only this file (repeatable); disables directory browsing")
	fs.Var((*stringListValue)(&cfg.Files), "file", "serve only this file (repeatable); disables directory browsing")
}

func (cfg *Config) applyFiles() error {
	if len(cfg.Files) == 0 {
		return nil
	}
	if cfg.RootSet {
		return fmt.Errorf("--file cannot be combined with -d/--dir; serve a directory or specific files, not both")
	}
	if cfg.UploadOnly {
		return fmt.Errorf("--file cannot be combined with --only-upload")
	}

	if cfg.AuthUpload != "" {
		return fmt.Errorf("--auth-upload has no effect with --file (there are no uploads); use --auth to require a login")
	}

	used := make(map[string]bool)
	for _, raw := range cfg.Files {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return fmt.Errorf("--file %q: %w", raw, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("--file %q: %w", raw, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("--file %q is not a regular file", raw)
		}
		cfg.FileSet = append(cfg.FileSet, FileEntry{
			Name: dedupeFileName(filepath.Base(abs), used),
			Real: abs,
			Size: info.Size(),
		})
	}
	cfg.FileSetMode = true
	return nil
}

func dedupeFileName(base string, used map[string]bool) string {
	if !used[base] {
		used[base] = true
		return base
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s(%d)%s", stem, i, ext)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func (cfg *Config) applyAuth() error {
	if cfg.Auth != "" && cfg.AuthUpload != "" {
		return fmt.Errorf("--auth cannot be combined with --auth-upload")
	}

	parse := func(flagName, raw string) (user, pass string, enabled bool, err error) {
		if raw == "" {
			return "", "", false, nil
		}
		user, pass, ok := strings.Cut(raw, ":")
		if !ok || user == "" || pass == "" {
			return "", "", false, fmt.Errorf("%s must be in the form user:pass", flagName)
		}
		return user, pass, true, nil
	}

	if cfg.Auth != "" {
		user, pass, enabled, err := parse("--auth", cfg.Auth)
		if err != nil {
			return err
		}
		cfg.AuthReadUser, cfg.AuthReadPass, cfg.AuthReadEnabled = user, pass, enabled
		cfg.AuthWriteUser, cfg.AuthWritePass, cfg.AuthWriteEnabled = user, pass, enabled
		return nil
	}

	uUser, uPass, uEnabled, err := parse("--auth-upload", cfg.AuthUpload)
	if err != nil {
		return err
	}
	cfg.AuthWriteUser, cfg.AuthWritePass, cfg.AuthWriteEnabled = uUser, uPass, uEnabled
	return nil
}

func (cfg Config) ClientAuthUpload() string {
	if cfg.Auth != "" {
		return cfg.Auth
	}
	return cfg.AuthUpload
}

func (cfg Config) ClientAuthDownload() string {
	return cfg.Auth
}

func Parse() (Config, error) {
	cfg := Config{}
	maxUploadSize := defaultMaxUploadSize
	maxZipSize := defaultMaxZipSize
	maxDownloadRate := defaultMaxDownloadRate
	idleTimeoutMinutes := 2
	readHeaderTimeoutSeconds := 10

	flag.Usage = func() {
		PrintUsage(flag.CommandLine.Output())
	}

	registerFlags(flag.CommandLine, &cfg, &maxUploadSize, &maxZipSize, &maxDownloadRate, &idleTimeoutMinutes, &readHeaderTimeoutSeconds)

	parseArgs, err := expandBareValueFlags(os.Args[1:])
	if err != nil {
		return Config{}, err
	}
	if err := flag.CommandLine.Parse(parseArgs); err != nil {
		return Config{}, err
	}
	flag.Visit(func(parsedFlag *flag.Flag) {
		switch parsedFlag.Name {
		case "max-upload-size":
			cfg.MaxUploadSizeSet = true
		case "max-zip-size":
			cfg.MaxZipSizeSet = true
		case "d", "dir":
			cfg.RootSet = true
		}
	})

	limit, err := ParseSize(maxUploadSize)
	if err != nil {
		return Config{}, fmt.Errorf("parse -max-upload-size: %w", err)
	}
	cfg.MaxUploadBytes = limit

	downloadRate, err := ParseSize(maxDownloadRate)
	if err != nil {
		return Config{}, fmt.Errorf("parse -max-download-rate: %w", err)
	}
	cfg.MaxDownloadBytes = downloadRate

	zipLimit, err := ParseSize(maxZipSize)
	if err != nil {
		return Config{}, fmt.Errorf("parse -max-zip-size: %w", err)
	}
	cfg.MaxZipBytes = zipLimit
	cfg.IdleTimeout = time.Duration(idleTimeoutMinutes) * time.Minute
	cfg.ReadHeaderTimeout = time.Duration(readHeaderTimeoutSeconds) * time.Second

	accessPath, err := NormalizeAccessPath(cfg.Path)
	if err != nil {
		return Config{}, fmt.Errorf("parse -path: %w", err)
	}
	cfg.Path = accessPath

	if cfg.UploadOnly && cfg.DownloadOnly {
		return Config{}, fmt.Errorf("-only-upload and -only-download cannot be used together")
	}
	if cfg.UploadOnly && cfg.MaxDownloadBytes > 0 {
		return Config{}, fmt.Errorf("--max-download-rate has no effect with --only-upload (there are no downloads)")
	}
	if cfg.FullAccess && cfg.DownloadOnly {
		return Config{}, fmt.Errorf("--full-access cannot be combined with --only-download, which makes the share read-only")
	}
	if cfg.FullAccess && len(cfg.Files) > 0 {
		return Config{}, fmt.Errorf("--full-access has no effect with --file (there is no directory to manage)")
	}
	if err := cfg.applyAuth(); err != nil {
		return Config{}, err
	}
	if err := cfg.applyFiles(); err != nil {
		return Config{}, err
	}
	if err := cfg.ValidateTransfer(); err != nil {
		return Config{}, err
	}
	if err := cfg.ValidateTLS(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// defaultLogFileName gives each run its own file, so sessions never interleave.
func defaultLogFileName() string {
	return "trasmetto-" + time.Now().Format("20060102-150405") + ".log"
}

func expandBareValueFlags(args []string) ([]string, error) {
	expanded := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			expanded = append(expanded, args[i:]...)
			break
		}
		if arg == "-log" || arg == "--log" {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				expanded = append(expanded, arg, args[i+1])
				i++
				continue
			}
			expanded = append(expanded, arg+"="+defaultLogFileName())
			continue
		}
		if arg == "-path" || arg == "--path" {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				expanded = append(expanded, arg, args[i+1])
				i++
				continue
			}
			generated, err := randomAccessPath(generatedPathLength)
			if err != nil {
				return nil, fmt.Errorf("generate random path: %w", err)
			}
			expanded = append(expanded, arg+"="+generated)
			continue
		}
		expanded = append(expanded, arg)
	}
	return expanded, nil
}

func randomAccessPath(length int) (string, error) {
	buf := make([]byte, length)
	limit := big.NewInt(int64(len(generatedPathAlphabet)))
	for i := range buf {
		index, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		buf[i] = generatedPathAlphabet[index.Int64()]
	}
	return string(buf), nil
}

func FormatBytes(bytes int64) string {
	const (
		kb = int64(1024)
		mb = kb * 1024
		gb = mb * 1024
		tb = gb * 1024
	)

	switch {
	case bytes >= tb:
		return trimDecimal(float64(bytes)/float64(tb)) + "TB"
	case bytes >= gb:
		return trimDecimal(float64(bytes)/float64(gb)) + "GB"
	case bytes >= mb:
		return trimDecimal(float64(bytes)/float64(mb)) + "MB"
	case bytes >= kb:
		return trimDecimal(float64(bytes)/float64(kb)) + "KB"
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

func trimDecimal(value float64) string {
	return strings.TrimSuffix(fmt.Sprintf("%.1f", value), ".0")
}

func PrintUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage of trasmetto:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "General")
	fmt.Fprintln(w, "  -h, --help               show this help and exit")
	fmt.Fprintln(w, "  -v, --version            print version and exit")
	fmt.Fprintln(w, "  -i, --ip string          IP address to host on (default \"0.0.0.0\")")
	fmt.Fprintln(w, "  -p, --port int           TCP port to host on (default 8000)")
	fmt.Fprintln(w, "  -d, --dir string         directory to serve (default \".\")")
	fmt.Fprintln(w, "      --path [string]      hidden URL path prefix required to access the uploader;")
	fmt.Fprintln(w, "                           use --path with no value to generate a random one")
	fmt.Fprintln(w, "      --no-banner          do not print the startup banner")
	fmt.Fprintln(w, "      --no-color           disable coloured terminal output")
	fmt.Fprintln(w, "      --log [path]         save logs to file (defaults to ./trasmetto-<date>-<time>.log)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Client Mode")
	fmt.Fprintln(w, "  -u, --url string         remote URL to download from, or upload to with --upload")
	fmt.Fprintln(w, "  -o, --outfile string     output file path or directory for downloads (with -u)")
	fmt.Fprintln(w, "      --upload string      local file to upload; requires -u/--url (the target server)")
	fmt.Fprintln(w, "      --insecure           allow insecure HTTPS certificates for -u transfers")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "File Settings")
	fmt.Fprintln(w, "  -f, --file path                serve only this file (repeatable); disables directory browsing")
	fmt.Fprintln(w, "      --max-upload-size string   maximum total upload size per request; use 0 for unlimited (default \"10GB\")")
	fmt.Fprintln(w, "      --max-zip-size string      refuse to zip a folder larger than this; use 0 for unlimited (default \"5GB\")")
	fmt.Fprintln(w, "      --no-zip                   disable downloading a folder as a zip archive")
	fmt.Fprintln(w, "      --allow-replace            replace existing files instead of saving as file(1).txt")
	fmt.Fprintln(w, "      --full-access              let visitors create folders and delete files from the browser")
	fmt.Fprintln(w, "      --only-download            allow browsing and downloads, but disable uploads")
	fmt.Fprintln(w, "      --only-upload              allow browsing and uploads, but disable downloads")
	fmt.Fprintln(w, "                                 (--only-download and --only-upload are mutually exclusive)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Authentication")
	fmt.Fprintln(w, "      --auth user:pass         require login for everything")
	fmt.Fprintln(w, "                               (in client mode, use the same parameter to login)")
	fmt.Fprintln(w, "      --auth-upload user:pass  require login for uploads only")
	fmt.Fprintln(w, "                               (in client mode, use the same parameter to login)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "HTTPS")
	fmt.Fprintln(w, "      --https             serve over HTTPS")
	fmt.Fprintln(w, "      --cert string       TLS certificate file for HTTPS")
	fmt.Fprintln(w, "      --key string        TLS private key file for HTTPS")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Connection Settings")
	fmt.Fprintln(w, "      --idle-timeout int         maximum time (minutes) to keep idle connections open (default 2)")
	fmt.Fprintln(w, "      --read-header-timeout int  maximum time (seconds) to read request headers (default 10)")
	fmt.Fprintln(w, "      --max-download-rate string throttle each download to this speed/sec (e.g. 1MB);")
	fmt.Fprintln(w, "                                 use 0 for unlimited (default \"0\")")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Examples")
	fmt.Fprintln(w, "  trasmetto -p 80 -d ../..")
	fmt.Fprintln(w, "  trasmetto --path veryhiddenpath --only-download")
	fmt.Fprintln(w, "  trasmetto -u http://192.168.1.10:8000/ --upload report.pdf")
	fmt.Fprintln(w, "  trasmetto -u http://192.168.1.10:8000/file.txt -o out.txt")
}

func (cfg Config) ValidateTransfer() error {
	if cfg.UploadPath != "" && cfg.DownloadURL == "" {
		return fmt.Errorf("--upload needs a target server; pass -u/--url too, for example: trasmetto -u http://host:8000/ --upload %s", cfg.UploadPath)
	}
	if cfg.UploadPath != "" && cfg.OutputPath != "" {
		return fmt.Errorf("--outfile cannot be used with --upload")
	}
	if cfg.OutputPath != "" && cfg.DownloadURL == "" {
		return fmt.Errorf("-o/--outfile needs a download to save; pass -u/--url too, for example: trasmetto -u http://host:8000/file.txt -o %s", cfg.OutputPath)
	}
	return nil
}

func (cfg Config) ValidateTLS() error {
	if !cfg.HTTPS && (cfg.CertFile != "" || cfg.KeyFile != "") {
		return fmt.Errorf("--cert and --key require --https")
	}
	if cfg.CertFile != "" && cfg.KeyFile == "" {
		return fmt.Errorf("--cert requires --key")
	}
	if cfg.KeyFile != "" && cfg.CertFile == "" {
		return fmt.Errorf("--key requires --cert")
	}
	return nil
}

func NormalizeAccessPath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || value == "/" {
		return "", nil
	}

	parts := strings.Split(strings.Trim(value, "/"), "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid path %q", raw)
		}
	}

	cleaned := strings.TrimPrefix(path.Clean("/"+strings.Join(parts, "/")), "/")
	if cleaned == "." {
		return "", nil
	}
	return cleaned, nil
}

func (cfg Config) ValidatedRoot() (displayRoot string, realRoot string, err error) {
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return "", "", fmt.Errorf("resolve directory: %w", err)
	}
	root = filepath.Clean(root)

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("%s - no such file or directory", root)
		}
		if os.IsPermission(err) {
			return "", "", fmt.Errorf("%s - permission denied", root)
		}
		return "", "", fmt.Errorf("%s - %v", root, err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("%s is not a directory", root)
	}

	realRoot, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve directory symlinks: %w", err)
	}

	realRoot = filepath.Clean(realRoot)
	return realRoot, realRoot, nil
}

func ParseSize(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return defaultUploadLimit, nil
	}
	if value == "0" {
		return 0, nil
	}

	numberPart := value
	unitPart := ""
	for i, r := range value {
		if (r < '0' || r > '9') && r != '.' {
			numberPart = strings.TrimSpace(value[:i])
			unitPart = strings.ToLower(strings.TrimSpace(value[i:]))
			break
		}
	}

	number, err := strconv.ParseFloat(numberPart, 64)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("invalid size %q", raw)
	}

	multiplier, ok := sizeMultipliers[unitPart]
	if !ok {
		return 0, fmt.Errorf("unsupported size unit %q", unitPart)
	}

	return int64(number * float64(multiplier)), nil
}

var sizeMultipliers = map[string]int64{
	"":   1,
	"b":  1,
	"k":  1024,
	"kb": 1024,
	"m":  1024 * 1024,
	"mb": 1024 * 1024,
	"g":  1024 * 1024 * 1024,
	"gb": 1024 * 1024 * 1024,
	"t":  1024 * 1024 * 1024 * 1024,
	"tb": 1024 * 1024 * 1024 * 1024,
}
