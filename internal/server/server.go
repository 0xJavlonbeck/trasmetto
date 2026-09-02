package server

import (
	"crypto/rand"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"trasmetto/internal/config"
)

const assetVersion = "20260902m"

type Server struct {
	root             string
	rootDisplay      string
	uploadOnly       bool
	downloadOnly     bool
	allowReplace     bool
	maxUploadBytes   int64
	noZip            bool
	maxZipBytes      int64
	maxDownloadBytes int64
	publicPath       string
	authReadEnabled  bool
	authReadUser     string
	authReadPass     string
	authWriteEnabled bool
	authWriteUser    string
	authWritePass    string
	authSecret       []byte
	fileSetMode      bool
	fileSet          map[string]string
	fileEntries      []entry
	colorLogs        bool
	logger           *log.Logger
	staticFiles      fs.FS
	pageTemplate     *template.Template
	loginTemplate    *template.Template
	assetSegment     string
	archiveSegment   string
	manageSegment    string
	fullAccess       bool
	events           *EventLogger
}

// SetEventLogger attaches the JSON file log, if one was requested.
func (s *Server) SetEventLogger(events *EventLogger) { s.events = events }

func New(cfg config.Config, logger *log.Logger, staticFiles fs.FS) (*Server, error) {
	displayRoot, realRoot, err := cfg.ValidatedRoot()
	if err != nil {
		return nil, err
	}

	publicPath := cfg.Path

	// A served entry with one of these names would otherwise be shadowed by the
	// route, so step the route aside instead of hiding the user's files.
	assetSegment := freeRouteSegment(realRoot, strings.Trim(assetRoutePrefix, "/"))
	archiveSegment := freeRouteSegment(realRoot, strings.Trim(archiveRoutePrefix, "/"))
	manageSegment := freeRouteSegment(realRoot, strings.Trim(manageRoutePrefix, "/"))

	fontURL := pathURL(publicPath, assetSegment+"/fonts/unifont-ui.woff2") + "?v=" + assetVersion
	backgroundURL := pathURL(publicPath, assetSegment+"/img/background.png") + "?v=" + assetVersion
	funcs := template.FuncMap{
		"pathurl": func(rel string) string {
			return pathURL(publicPath, rel)
		},
		"assetversion": func() string {
			return assetVersion
		},
		"asseturl": func(rel string) string {
			return pathURL(publicPath, assetSegment+"/"+rel)
		},
		"archiveurl": func(rel string) string {
			return archivePathURL(publicPath, archiveSegment, rel)
		},
		"manageurl": func(rel string) string {
			return managePathURL(publicPath, manageSegment, rel)
		},
		"fonturl": func() template.CSS {
			return cssURL(fontURL)
		},
		"backgroundurl": func() template.CSS {
			return cssURL(backgroundURL)
		},
	}
	tmpl, err := template.New("index.html").Funcs(funcs).ParseFS(staticFiles, "static/html/index.html")
	if err != nil {
		return nil, err
	}
	loginTmpl, err := template.New("login.html").Funcs(funcs).ParseFS(staticFiles, "static/html/login.html")
	if err != nil {
		return nil, err
	}

	var secret []byte
	if cfg.AuthEnabled() {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
	}

	var fileSet map[string]string
	var fileEntries []entry
	if cfg.FileSetMode {
		fileSet, fileEntries = buildFileSet(cfg.FileSet)
	}

	return &Server{
		root:             realRoot,
		rootDisplay:      displayRoot,
		uploadOnly:       cfg.UploadOnly,
		downloadOnly:     cfg.DownloadOnly || cfg.FileSetMode,
		allowReplace:     cfg.AllowReplace,
		maxUploadBytes:   cfg.MaxUploadBytes,
		noZip:            cfg.NoZip,
		maxZipBytes:      cfg.MaxZipBytes,
		maxDownloadBytes: cfg.MaxDownloadBytes,
		fileSetMode:      cfg.FileSetMode,
		fileSet:          fileSet,
		fileEntries:      fileEntries,
		publicPath:       publicPath,
		authReadEnabled:  cfg.AuthReadEnabled,
		authReadUser:     cfg.AuthReadUser,
		authReadPass:     cfg.AuthReadPass,
		authWriteEnabled: cfg.AuthWriteEnabled,
		authWriteUser:    cfg.AuthWriteUser,
		authWritePass:    cfg.AuthWritePass,
		authSecret:       secret,
		logger:           logger,
		staticFiles:      staticFiles,
		pageTemplate:     tmpl,
		loginTemplate:    loginTmpl,
		assetSegment:     assetSegment,
		archiveSegment:   archiveSegment,
		manageSegment:    manageSegment,
		fullAccess:       cfg.FullAccess,
	}, nil
}

// buildFileSet turns the -f selection into the lookup map and the listing rows.
// The rows must carry the same fields readEntries produces, or the listing
// marks every served file unreadable and stops linking to it.
func buildFileSet(files []config.FileEntry) (map[string]string, []entry) {
	lookup := make(map[string]string, len(files))
	entries := make([]entry, 0, len(files))
	for _, f := range files {
		lookup[f.Name] = f.Real
		entries = append(entries, entry{
			Name:     f.Name,
			RelPath:  f.Name,
			Readable: isEntryReadable(f.Real, false),
			Size:     formatSize(f.Size, false),
			Bytes:    f.Size,
		})
	}
	return lookup, entries
}

// freeRouteSegment returns name, or name-1, name-2 ... — the first variant that
// does not exist in root, so the route never hides a served entry.
func freeRouteSegment(root, name string) string {
	candidate := name
	for i := 1; ; i++ {
		if _, err := os.Lstat(filepath.Join(root, candidate)); err != nil {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", name, i)
	}
}

func (s *Server) RootDisplay() string {
	return s.rootDisplay
}

func (s *Server) Routes(logger *log.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.assetRoutePrefix(), s.handleStaticAsset)
	if s.fileSetMode {
		mux.HandleFunc(s.archiveRoutePrefix(), s.handleArchive)
		mux.HandleFunc("/", s.handleFileSet)
	} else {
		mux.HandleFunc(s.archiveRoutePrefix(), s.handleArchive)
		mux.HandleFunc(s.manageRoutePrefix(), s.handleManage)
		mux.HandleFunc("/", s.handlePath)
		// Registering the subtree patterns above makes ServeMux redirect the
		// slashless forms into them, which would hide a served file that
		// happens to carry one of these names. Serve those paths normally.
		mux.HandleFunc(strings.TrimSuffix(s.assetRoutePrefix(), "/"), s.handlePath)
		mux.HandleFunc(strings.TrimSuffix(s.archiveRoutePrefix(), "/"), s.handlePath)
		mux.HandleFunc(strings.TrimSuffix(s.manageRoutePrefix(), "/"), s.handlePath)
	}

	var handler http.Handler = mux
	if s.authEnabled() {
		handler = s.authMiddleware(handler)
	}
	return requestLogger(logger, handler, logRoutes{
		assetPrefix:   s.assetRoutePrefix(),
		archivePrefix: s.archiveRoutePrefix(),
		managePrefix:  s.manageRoutePrefix(),
		loginPath:     s.loginRoutePath(),
	}, s.colorLogs, s.events)
}

func (s *Server) EnableColor(on bool) { s.colorLogs = on }

func (s *Server) authEnabled() bool {
	return s.authReadEnabled || s.authWriteEnabled
}

func cssURL(raw string) template.CSS {
	return template.CSS(`url("` + raw + `")`)
}
