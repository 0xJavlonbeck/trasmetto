package config

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestApplyFilesBuildsSet(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(a, []byte("A"), 0600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	a2 := filepath.Join(sub, "a.txt")
	if err := os.WriteFile(a2, []byte("BB"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Files: []string{a, a2}}
	if err := cfg.applyFiles(); err != nil {
		t.Fatalf("applyFiles: %v", err)
	}
	if !cfg.FileSetMode || len(cfg.FileSet) != 2 {
		t.Fatalf("FileSetMode=%v entries=%d", cfg.FileSetMode, len(cfg.FileSet))
	}
	if cfg.FileSet[0].Name != "a.txt" || cfg.FileSet[1].Name != "a(1).txt" {
		t.Fatalf("names = %q, %q", cfg.FileSet[0].Name, cfg.FileSet[1].Name)
	}
	if cfg.FileSet[0].Size != 1 || cfg.FileSet[1].Size != 2 {
		t.Fatalf("sizes = %d, %d", cfg.FileSet[0].Size, cfg.FileSet[1].Size)
	}
}

func TestApplyFilesRejects(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(f, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []Config{
		{Files: []string{filepath.Join(dir, "missing")}},
		{Files: []string{dir}},
		{Files: []string{f}, UploadOnly: true},
		{Files: []string{f}, RootSet: true},
		{Files: []string{f}, AuthUpload: "u:p"},
	}
	for i, cfg := range cases {
		if err := cfg.applyFiles(); err == nil {
			t.Errorf("case %d: applyFiles = nil, want error", i)
		}
	}
}

func TestPrintUsageDocumentsEveryFlag(t *testing.T) {
	fs := flag.NewFlagSet("trasmetto", flag.ContinueOnError)
	var cfg Config
	maxUpload, maxZip, maxDown := "0", defaultMaxZipSize, "0"
	idle, readHeader := 2, 10
	registerFlags(fs, &cfg, &maxUpload, &maxZip, &maxDown, &idle, &readHeader)

	var buf bytes.Buffer
	PrintUsage(&buf)
	usage := buf.String()

	fs.VisitAll(func(f *flag.Flag) {
		if strings.Contains(usage, "--"+f.Name) {
			return
		}
		if len(f.Name) == 1 && strings.Contains(usage, "-"+f.Name) {
			return
		}
		t.Errorf("flag %q is registered but not documented in PrintUsage", f.Name)
	})
}

func TestParseSizeBinaryUnitsWithCommonLabels(t *testing.T) {
	tests := map[string]int64{
		"":      0,
		"0":     0,
		"100MB": 100 * 1024 * 1024,
		"2GB":   2 * 1024 * 1024 * 1024,
		"1.5GB": 1536 * 1024 * 1024,
	}

	for input, want := range tests {
		got, err := ParseSize(input)
		if err != nil {
			t.Fatalf("ParseSize(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseSize(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestParseSizeRejectsBinaryUnits(t *testing.T) {
	for _, input := range []string{"100MiB", "2GiB", "1TiB"} {
		if _, err := ParseSize(input); err == nil {
			t.Fatalf("ParseSize(%q) succeeded, want error", input)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := map[int64]string{
		11:                 "11B",
		1024:               "1KB",
		1536:               "1.5KB",
		1024 * 1024:        "1MB",
		1024 * 1024 * 1024: "1GB",
	}

	for input, want := range tests {
		if got := FormatBytes(input); got != want {
			t.Fatalf("FormatBytes(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeAccessPath(t *testing.T) {
	tests := map[string]string{
		"":                 "",
		"/":                "",
		"veryhiddenpath":   "veryhiddenpath",
		"/veryhiddenpath/": "veryhiddenpath",
		"secret/files":     "secret/files",
	}

	for input, want := range tests {
		got, err := NormalizeAccessPath(input)
		if err != nil {
			t.Fatalf("NormalizeAccessPath(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeAccessPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeAccessPathRejectsTraversal(t *testing.T) {
	for _, input := range []string{"..", "../secret", "secret/..", "secret/./files"} {
		if _, err := NormalizeAccessPath(input); err == nil {
			t.Fatalf("NormalizeAccessPath(%q) succeeded, want error", input)
		}
	}
}

func TestExpandBarePathFlagGeneratesRandomWhenValueMissing(t *testing.T) {
	cases := [][]string{
		{"-d", "/tmp", "--path"},
		{"--path", "-p", "8000"},
		{"-path"},
	}
	for _, args := range cases {
		got, err := expandBareValueFlags(args)
		if err != nil {
			t.Fatalf("expandBareValueFlags(%v) returned error: %v", args, err)
		}
		var pathToken string
		for _, token := range got {
			if strings.HasPrefix(token, "--path=") || strings.HasPrefix(token, "-path=") {
				pathToken = token
			}
			if token == "--path" || token == "-path" {
				t.Fatalf("expandBareValueFlags(%v) left a bare path flag: %v", args, got)
			}
		}
		value := pathToken[strings.IndexByte(pathToken, '=')+1:]
		if len(value) != generatedPathLength {
			t.Fatalf("generated path %q length = %d, want %d", value, len(value), generatedPathLength)
		}
		for _, r := range value {
			if !strings.ContainsRune(generatedPathAlphabet, r) {
				t.Fatalf("generated path %q contains invalid character %q", value, r)
			}
		}
	}
}

func TestExpandBarePathFlagKeepsExplicitValue(t *testing.T) {
	cases := map[string][]string{
		"space":  {"--path", "secret", "-p", "8000"},
		"equals": {"--path=secret"},
		"short":  {"-path", "secret"},
	}
	for name, args := range cases {
		got, err := expandBareValueFlags(args)
		if err != nil {
			t.Fatalf("%s: expandBareValueFlags returned error: %v", name, err)
		}
		joined := strings.Join(got, " ")
		if !strings.Contains(joined, "secret") {
			t.Fatalf("%s: explicit value dropped: %v", name, got)
		}
		if strings.Contains(joined, "secret=") || strings.Contains(joined, "=secret=") {
			t.Fatalf("%s: explicit value mangled: %v", name, got)
		}
	}
}

func TestExpandBarePathFlagStopsAtTerminator(t *testing.T) {

	got, err := expandBareValueFlags([]string{"-d", "/tmp", "--", "--path"})
	if err != nil {
		t.Fatalf("expandBareValueFlags returned error: %v", err)
	}
	if strings.Join(got, " ") != "-d /tmp -- --path" {
		t.Fatalf("terminator not respected: %v", got)
	}
}

func TestValidateTLS(t *testing.T) {
	valid := []Config{
		{},
		{HTTPS: true},
		{HTTPS: true, CertFile: "cert.pem", KeyFile: "key.pem"},
	}
	for _, cfg := range valid {
		if err := cfg.ValidateTLS(); err != nil {
			t.Fatalf("ValidateTLS(%+v) returned error: %v", cfg, err)
		}
	}

	invalid := []Config{
		{CertFile: "cert.pem", KeyFile: "key.pem"},
		{HTTPS: true, CertFile: "cert.pem"},
		{HTTPS: true, KeyFile: "key.pem"},
	}
	for _, cfg := range invalid {
		if err := cfg.ValidateTLS(); err == nil {
			t.Fatalf("ValidateTLS(%+v) succeeded, want error", cfg)
		}
	}
}

func TestValidateTransfer(t *testing.T) {
	valid := []Config{
		{},
		{DownloadURL: "http://example.test/file"},
		{DownloadURL: "http://example.test/", UploadPath: "file.txt"},
	}
	for _, cfg := range valid {
		if err := cfg.ValidateTransfer(); err != nil {
			t.Fatalf("ValidateTransfer(%+v) returned error: %v", cfg, err)
		}
	}

	invalid := []Config{
		{UploadPath: "file.txt"},
		{DownloadURL: "http://example.test/", UploadPath: "file.txt", OutputPath: "out.txt"},
	}
	for _, cfg := range invalid {
		if err := cfg.ValidateTransfer(); err == nil {
			t.Fatalf("ValidateTransfer(%+v) succeeded, want error", cfg)
		}
	}
}

func TestApplyAuthAllProtectsBothSides(t *testing.T) {
	cfg := Config{Auth: "admin:p@ss:word"}
	if err := cfg.applyAuth(); err != nil {
		t.Fatalf("applyAuth: %v", err)
	}
	if !cfg.AuthEnabled() {
		t.Fatal("AuthEnabled() = false, want true")
	}
	if !cfg.AuthReadEnabled || !cfg.AuthWriteEnabled {
		t.Fatal("--auth should protect both read and write")
	}

	if cfg.AuthReadUser != "admin" || cfg.AuthReadPass != "p@ss:word" {
		t.Errorf("read creds = %q/%q, want admin/p@ss:word", cfg.AuthReadUser, cfg.AuthReadPass)
	}
	if cfg.AuthWriteUser != "admin" || cfg.AuthWritePass != "p@ss:word" {
		t.Errorf("write creds = %q/%q, want admin/p@ss:word", cfg.AuthWriteUser, cfg.AuthWritePass)
	}
}

func TestApplyAuthUploadOnly(t *testing.T) {
	cfg := Config{AuthUpload: "writer:w"}
	if err := cfg.applyAuth(); err != nil {
		t.Fatalf("applyAuth: %v", err)
	}
	if !cfg.AuthWriteEnabled || cfg.AuthWriteUser != "writer" || cfg.AuthWritePass != "w" {
		t.Errorf("write side = %v %q/%q", cfg.AuthWriteEnabled, cfg.AuthWriteUser, cfg.AuthWritePass)
	}

	if cfg.AuthReadEnabled {
		t.Error("--auth-upload alone must not protect downloads")
	}
}

func TestApplyAuthRejectsBadInput(t *testing.T) {
	for _, tc := range []Config{
		{Auth: "nocolon"},
		{Auth: ":onlypass"},
		{Auth: "onlyuser:"},
		{AuthUpload: "bad"},
		{Auth: "u:p", AuthUpload: "u:p"},
	} {
		if err := tc.applyAuth(); err == nil {
			t.Errorf("applyAuth(%+v) = nil, want error", tc)
		}
	}
}

func TestPrintUsageUsesGroupedFlags(t *testing.T) {
	var out bytes.Buffer
	PrintUsage(&out)

	text := out.String()
	for _, want := range []string{"General", "-i, --ip", "-p, --port", "-d, --dir", "--no-banner", "Client Mode", "-u, --url", "-o, --outfile", "--upload", "File Settings", "--allow-replace", "HTTPS", "Connection Settings"} {
		if !strings.Contains(text, want) {
			t.Fatalf("usage missing %q:\n%s", want, text)
		}
	}
	for _, want := range []string{"--idle-timeout int", "(minutes)", "(default 2)", "--read-header-timeout int", "(seconds)", "(default 10)", "-v, --version", "-h, --help"} {
		if !strings.Contains(text, want) {
			t.Fatalf("usage missing %q:\n%s", want, text)
		}
	}
	for _, old := range []string{"-host", "-upload-limit", "--read-timeout", "--write-timeout", "\n  -k", "\n      -k", "2m0s", "10s", "30s"} {
		if strings.Contains(text, old) {
			t.Fatalf("usage contains old flag %q:\n%s", old, text)
		}
	}
}

func TestValidateTransferRequiresURLForOutfile(t *testing.T) {
	err := Config{OutputPath: "out.txt"}.ValidateTransfer()
	if err == nil {
		t.Fatal("expected -o without -u to be rejected")
	}
	if !strings.Contains(err.Error(), "-u/--url") {
		t.Fatalf("error should point at -u/--url, got %q", err)
	}
	if err := (Config{OutputPath: "out.txt", DownloadURL: "http://host/f"}).ValidateTransfer(); err != nil {
		t.Fatalf("-o with -u should be valid, got %v", err)
	}
}

func TestBareLogFileFlagDefaultsToCurrentDirectory(t *testing.T) {
	got, err := expandBareValueFlags([]string{"--log", "-p", "8000"})
	if err != nil {
		t.Fatalf("expandBareValueFlags returned error: %v", err)
	}
	name, ok := strings.CutPrefix(got[0], "--log=")
	if !ok {
		t.Fatalf("bare --log = %q, want a generated file name", got[0])
	}
	if !regexp.MustCompile(`^trasmetto-\d{8}-\d{6}\.log$`).MatchString(name) {
		t.Fatalf("generated name = %q, want trasmetto-<date>-<time>.log", name)
	}

	// An explicit path must survive untouched.
	got, err = expandBareValueFlags([]string{"--log", "/var/log/t.log"})
	if err != nil {
		t.Fatalf("expandBareValueFlags returned error: %v", err)
	}
	if len(got) != 2 || got[1] != "/var/log/t.log" {
		t.Fatalf("explicit path was rewritten: %v", got)
	}
}

// parseArgsForTest runs the real Parse against a throwaway flag set, so the
// validation rules are exercised exactly as the binary applies them.
func parseArgsForTest(args []string) (Config, error) {
	oldArgs, oldFlags := os.Args, flag.CommandLine
	defer func() { os.Args, flag.CommandLine = oldArgs, oldFlags }()

	flag.CommandLine = flag.NewFlagSet("trasmetto", flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	os.Args = append([]string{"trasmetto"}, args...)
	return Parse()
}

func TestConflictingFlagsAreRejected(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"rate without downloads", []string{"--only-upload", "--max-download-rate", "1MB"}, "no effect with --only-upload"},
		{"both modes", []string{"--only-upload", "--only-download"}, "cannot be used together"},
		{"full access read-only", []string{"--full-access", "--only-download"}, "cannot be combined with --only-download"},
		{"both auth flags", []string{"--auth", "a:b", "--auth-upload", "c:d"}, "cannot be combined"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseArgsForTest(tc.args)
			if err == nil {
				t.Fatalf("%v was accepted, want an error", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestValidCombinationsStillParse(t *testing.T) {
	for _, args := range [][]string{
		{"--only-upload"},
		{"--max-download-rate", "1MB"},
		{"--only-download", "--max-download-rate", "1MB"},
		{"--only-upload", "--max-download-rate", "0"},
	} {
		if _, err := parseArgsForTest(args); err != nil {
			t.Errorf("%v was rejected: %v", args, err)
		}
	}
}
