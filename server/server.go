package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"golocalgal/types"
	"golocalgal/vars"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds configuration for starting the HTTP server.
type Config struct {
	Bind      string
	Dsn       string
	MediaRoot string
	DfLog     string
	DfLogRoot string
	SlowSqlMs int
}

// Controller controls a running server instance for the GUI
type Controller struct {
	srv    *http.Server
	ctx    context.Context
	cancel context.CancelCauseFunc
	ready  chan struct{}
}

func (c *Controller) Context() context.Context {
	if c == nil || c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}
func (c *Controller) Ready() <-chan struct{} {
	if c == nil || c.ready == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return c.ready
}
func (c *Controller) Done() <-chan struct{} {
	return c.Context().Done()
}
func (c *Controller) Err() error {
	return context.Cause(c.Context())
}

func GetServerConfig() Config {
	slowSqlMs := 100
	if v := vars.EnvSlowSqlMs.GetValue(); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n >= -1 {
			slowSqlMs = n
		}
	}

	dfLog := vars.EnvDflog.GetValueDefault("./ripme.downloaded.files.log")
	defaultDfLogRoot := getDefaultDfLogRoot(dfLog)
	dfLogRoot := vars.EnvDflogRoot.GetValueDefault(defaultDfLogRoot)

	serverConfig := Config{
		Bind:      vars.EnvBind.GetValueDefault("127.0.0.1:5037"),
		Dsn:       vars.EnvSqliteDsn.GetValueDefault("file:ripme.sqlite"),
		MediaRoot: vars.EnvMediaRoot.GetValueDefault("./rips"),
		DfLog:     dfLog,
		DfLogRoot: dfLogRoot,
		SlowSqlMs: slowSqlMs,
	}
	return serverConfig
}

func getDefaultDfLogRoot(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(filepath.Dir(path))
	}
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal("Unable to get cwd: %w", err)
	}
	return filepath.Clean(filepath.Dir(filepath.Join(wd, path)))
}

func StartServer(cfg Config) (*Controller, error) {
	log.Printf("Starting LocalGal")

	var err error

	vars.SlowSqlMs = cfg.SlowSqlMs
	vars.DfLogRoot = cfg.DfLogRoot

	cfg.Dsn = DsnWithReadOnly(cfg.Dsn)
	cfg.Dsn = DsnWithDefaultTimeout(cfg.Dsn)
	cfg.Dsn = DsnWithForeignKeys(cfg.Dsn)

	vars.Db, err = GetDb(cfg.Dsn)
	if err != nil {
		return nil, err
	}

	dbFilename := getFileFromDsn(cfg.Dsn)
	if err = initDB(vars.Db, dbFilename); err != nil {
		log.Printf("init db: %v", err)
		return nil, err
	}

	vars.CacheDb, err = GetCacheDb()
	if err != nil {
		return nil, err
	}

	vars.Tpl = template.Must(template.New("").Funcs(template.FuncMap{
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, errors.New("dict must have key-value pairs")
			}
			m := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				k, _ := values[i].(string)
				m[k] = values[i+1]
			}
			return m, nil
		},
		"add":       func(a, b int) int { return a + b },
		"sub":       func(a, b int) int { return a - b },
		"hasPrefix": func(s, pre string) bool { return strings.HasPrefix(strings.ToLower(s), strings.ToLower(pre)) },
		"hasSuffix": func(s, suf string) bool { return strings.HasSuffix(strings.ToLower(s), strings.ToLower(suf)) },
		"fmtDateMillis": func(ms int64) string {
			if ms <= 0 {
				return ""
			}
			return time.UnixMilli(ms).Format("2006-01-02")
		},
		"queryParam": func(k string, v string) string {
			if len(v) == 0 {
				return ""
			}
			return "&" + k + "=" + v
		},
		"queryString": func(params map[string]string) string {
			if len(params) == 0 {
				return ""
			}
			v := url.Values{}
			for key, value := range params {
				v.Set(key, value)
			}
			return "?" + v.Encode()
		},
		"calcPages": func(total, size int) int {
			if size <= 0 {
				return 1
			}
			pages := total / size
			if total%size != 0 {
				pages++
			}
			if pages == 0 {
				pages = 1
			}
			return pages
		},
		"bytesToHumanReadable": func(size int64) string {
			floatBytes := float64(size)
			magnitudes := []string{"", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "Zi"}
			magIdx := 0
			for ; floatBytes >= 1024; magIdx++ {
				floatBytes /= 1024
			}
			return fmt.Sprintf("%.2f %sB", floatBytes, magnitudes[magIdx])
		},
		"fmtMillis": func(d time.Duration) string { return d.Round(time.Millisecond).String() },
		"finalPageMillis": func(p types.Perf) string {
			if !p.Start.IsZero() {
				return time.Since(p.Start).Round(time.Millisecond).String()
			}
			return p.PageTime.Round(time.Millisecond).String()
		},
		// Version info helpers for templates
		"appVersion":   func() string { return vars.BuildInfo.Version },
		"appCommit":    func() string { return vars.BuildInfo.Commit },
		"appBuildDate": func() string { return vars.BuildInfo.BuildDate },
	}).ParseFS(vars.TemplatesFS, "templates/*.gohtml"))

	vars.MediaRoot = cfg.MediaRoot

	mux := newMux()
	srv := &http.Server{
		Addr:    cfg.Bind,
		Handler: mux,
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	ctrl := Controller{srv: srv, ctx: ctx, cancel: cancel, ready: make(chan struct{})}

	go func() {
		if err := loadKnownFiles(ctx, cfg.DfLog); err != nil {
			if ctx.Err() != nil {
				log.Println("startup canceled while loading known files")
				cancel(ctx.Err())
				return
			}
			log.Printf("loading known files error: %v", err)
		}
		if err := ctx.Err(); err != nil {
			// Canceled before starting server; do not attempt to listen
			cancel(err)
			return
		}
		ln, err := net.Listen("tcp", cfg.Bind)
		if err != nil {
			cancel(err)
			log.Printf("listen error: %v", err)
			return
		}
		close(ctrl.ready)
		log.Printf("LocalGal listening on %s", ln.Addr())
		err = srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		if err != nil {
			log.Printf("Error starting server: %v", err)
		}
		cancel(err)
	}()
	return &ctrl, nil
}

func (c *Controller) Stop(ctx context.Context) error {
	log.Println("LocalGal stopping")
	var firstErr error
	if c != nil {
		c.cancel(context.Canceled)
	}
	if c != nil && c.srv != nil {
		if err := c.srv.Shutdown(ctx); err != nil {
			firstErr = err
		}
	}
	if vars.Db != nil {
		if err := vars.Db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleBrowse)
	mux.HandleFunc("/gallery/{ripper_host}/{gid}", handleGallery)
	mux.HandleFunc("/gallery/{ripper_host}/{gid}/{file_id}", handleGalleryFile)
	mux.HandleFunc("/file/{ripper_host}/{file_id}", handleFileStandalone)
	mux.HandleFunc("/file/{ripper_host}/{file_id}/galleries", handleFileGalleryFragment)
	mux.HandleFunc("/tags", handleTags)
	mux.HandleFunc("/tag/{tag_name}", handleTagDetail)
	mux.HandleFunc("/search", handleSearch)
	mux.HandleFunc("/search/galleries", handleSearchGalleries)
	mux.HandleFunc("/search/files", handleSearchFiles)
	mux.HandleFunc("/search/tags", handleSearchTags)
	mux.HandleFunc("/random/gallery", handleRandomGallery)
	mux.HandleFunc("/random/file", handleRandomFile)
	mux.HandleFunc("/random/page", handleRandomPage)

	mux.HandleFunc("GET /api/", asApi(handle404))
	mux.HandleFunc("GET /api/galleries", asApi(handleBrowse))
	mux.HandleFunc("GET /api/gallery/{ripper_host}/{gid}", asApi(handleGallery))
	mux.HandleFunc("GET /api/gallery/{ripper_host}/{gid}/{file_id}", asApi(handleGalleryFile))
	mux.HandleFunc("GET /api/file/{ripper_host}/{file_id}", asApi(handleFileStandalone))
	mux.HandleFunc("GET /api/file/{ripper_host}/{file_id}/galleries", asApi(handleFileGalleryFragment))
	mux.HandleFunc("GET /api/tags", asApi(handleTags))
	mux.HandleFunc("GET /api/tag/{tag_name}", asApi(handleTagDetail))
	mux.HandleFunc("GET /api/search", asApi(handleSearch))
	mux.HandleFunc("GET /api/search/galleries", asApi(handleSearchGalleries))
	mux.HandleFunc("GET /api/search/tags", asApi(handleSearchTags))
	mux.HandleFunc("GET /api/search/files", asApi(handleSearchFiles))
	mux.HandleFunc("GET /api/random/gallery", asApi(handleRandomGallery))
	mux.HandleFunc("GET /api/random/file", asApi(handleRandomFile))

	mux.HandleFunc("/media/", handleMedia)

	// For development:
	//mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("/media/data/code/golocalgal_public/static"))))
	mux.HandleFunc("/static/", handleStatic)

	mux.HandleFunc("/about", handleAbout)
	//mux.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
	//	renderError(r.Context(), w, &types.Perf{}, http.StatusInternalServerError, fmt.Errorf("foobar"))
	//})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok")) })

	var wrapped http.Handler
	wrapped = logMiddleware(mux)
	wrapped = tinyOptimizeDb(mux)
	return wrapped
}

func asApi(handler func(w http.ResponseWriter, r *http.Request)) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		handler(w, withRenderMode(r, RenderJSON))
	}
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		dur := time.Since(start)
		log.Printf("%s %s %v", r.Method, r.URL.Path, dur.Round(time.Millisecond))
	})
}

// tinyOptimizeDb runs a row-limited optimize. It's probably faster to optimize queries on 400-10000 rows than it is to wait 2 minutes for a worse-case response.
func tinyOptimizeDb(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
			return withSQL(ctx, func() error {
				pragma1 := "PRAGMA analysis_limit=10000"
				pragma2 := "PRAGMA optimize"
				if _, err = vars.Db.ExecContext(ctx, pragma1); err != nil {
					log.Printf("pragma error %q: %v", pragma1, err)
					if _, err = vars.Db.ExecContext(ctx, pragma2); err != nil {
						log.Printf("pragma error %q: %v", pragma2, err)
					}
				}
				return err
			})
		})
		if err != nil {
			renderError(r.Context(), w, &types.Perf{}, http.StatusInternalServerError, err)
			return
		}
		_ = p
		next.ServeHTTP(w, r)
	})
}

type renderModeKey struct{}
type RenderMode int

const (
	RenderHTML RenderMode = iota
	RenderJSON
)

func withRenderMode(r *http.Request, mode RenderMode) *http.Request {
	ctx := context.WithValue(r.Context(), renderModeKey{}, mode)
	return r.WithContext(ctx)
}

func getRenderMode(ctx context.Context) RenderMode {
	if v, ok := ctx.Value(renderModeKey{}).(RenderMode); ok {
		return v
	}
	return RenderHTML
}

func render(ctx context.Context, w http.ResponseWriter, name string, data any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if basePager, ok := data.(types.BasePager); ok {
		p := basePager.GetBasePage().Perf
		pageTime := time.Since(p.Start)
		pageTimeStr := strconv.FormatInt(pageTime.Milliseconds(), 10)
		sqlTimeStr := strconv.FormatInt(p.SQLTime.Milliseconds(), 10)
		sqlCountStr := strconv.FormatInt(int64(p.SQLCount), 10)
		w.Header().Set("X-App-Page-Time-Ms", pageTimeStr)
		w.Header().Set("X-App-Sql-Time-Ms", sqlTimeStr)
		w.Header().Set("X-App-Sql-Count", sqlCountStr)
	}

	renderMode := getRenderMode(ctx)
	if renderMode == RenderJSON {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.Header().Set("X-App-Version", vars.BuildInfo.Version)
		w.Header().Set("X-App-Commit", vars.BuildInfo.Commit)
		w.Header().Set("X-App-Build-Date", vars.BuildInfo.BuildDate)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}
	jsCookie := &http.Cookie{
		Name:   "js",
		Value:  "",
		Path:   "/",
		MaxAge: -1, // Tell the client to instantly delete the cookie
	}
	http.SetCookie(w, jsCookie)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Set short-lived cache for HTML pages to allow quick back/forward without staleness
	w.Header().Set("Cache-Control", "private, max-age=60")
	return vars.Tpl.ExecuteTemplate(w, name, data)
}

// Same as render, but fragments shouldn't clear the JS cookie
func renderFragment(ctx context.Context, w http.ResponseWriter, name string, data any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if basePager, ok := data.(types.BasePager); ok {
		p := basePager.GetBasePage().Perf
		pageTime := time.Since(p.Start)
		pageTimeStr := strconv.FormatInt(pageTime.Milliseconds(), 10)
		sqlTimeStr := strconv.FormatInt(p.SQLTime.Milliseconds(), 10)
		sqlCountStr := strconv.FormatInt(int64(p.SQLCount), 10)
		w.Header().Set("X-App-Page-Time-Ms", pageTimeStr)
		w.Header().Set("X-App-Sql-Time-Ms", sqlTimeStr)
		w.Header().Set("X-App-Sql-Count", sqlCountStr)
	}

	renderMode := getRenderMode(ctx)
	if renderMode == RenderJSON {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.Header().Set("X-App-Version", vars.BuildInfo.Version)
		w.Header().Set("X-App-Commit", vars.BuildInfo.Commit)
		w.Header().Set("X-App-Build-Date", vars.BuildInfo.BuildDate)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Set short-lived cache for HTML pages to allow quick back/forward without staleness
	w.Header().Set("Cache-Control", "private, max-age=60")
	return vars.Tpl.ExecuteTemplate(w, name, data)
}

func renderError(ctx context.Context, w http.ResponseWriter, perf *types.Perf, status int, err error) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	pageTime := time.Since(perf.Start)
	pageTimeStr := strconv.FormatInt(pageTime.Milliseconds(), 10)
	sqlTimeStr := strconv.FormatInt(perf.SQLTime.Milliseconds(), 10)
	sqlCountStr := strconv.FormatInt(int64(perf.SQLCount), 10)
	w.Header().Set("X-App-Page-Time-Ms", pageTimeStr)
	w.Header().Set("X-App-Sql-Time-Ms", sqlTimeStr)
	w.Header().Set("X-App-Sql-Count", sqlCountStr)

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		status = http.StatusNotFound
		err = nil
	}
	statusText := fmt.Sprintf("%d %s", status, http.StatusText(status))
	model := types.ErrorPage{StatusText: statusText, BasePage: &types.BasePage{Perf: perf}}
	if err != nil {
		model.Message = err.Error()
	} else {
		model.Message = statusText
	}
	w.WriteHeader(status)
	renderMode := getRenderMode(ctx)
	if renderMode == RenderJSON {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-App-Version", vars.BuildInfo.Version)
		w.Header().Set("X-App-Commit", vars.BuildInfo.Commit)
		w.Header().Set("X-App-Build-Date", vars.BuildInfo.BuildDate)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(model)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = vars.Tpl.ExecuteTemplate(w, "error.gohtml", model)
}

func httpRedirect(ctx context.Context, w http.ResponseWriter, r *http.Request, perf *types.Perf, url string, code int) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	pageTime := time.Since(perf.Start)
	pageTimeStr := strconv.FormatInt(pageTime.Milliseconds(), 10)
	sqlTimeStr := strconv.FormatInt(perf.SQLTime.Milliseconds(), 10)
	sqlCountStr := strconv.FormatInt(int64(perf.SQLCount), 10)
	w.Header().Set("X-App-Page-Time-Ms", pageTimeStr)
	w.Header().Set("X-App-Sql-Time-Ms", sqlTimeStr)
	w.Header().Set("X-App-Sql-Count", sqlCountStr)
	http.Redirect(w, r, url, code)
}
