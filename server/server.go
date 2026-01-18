package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"golocalgal/types"
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

// App holds the dependencies for the server handlers
type App struct {
	Db              *sql.DB
	CacheDb         *sql.DB
	Tpl             *template.Template
	StaticFSHandler http.Handler
	BuildInfo       types.BuildInfo
	MediaRoot       string
	DfLogRoot       string
	SlowSqlMs       int
	KnownFilePaths  map[string][]string
}

// Controller controls a running server instance for the GUI
type Controller struct {
	app    *App
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

func StartServer(cfg Config) (*Controller, error) {
	log.Printf("Starting LocalGal")

	var err error

	app := &App{
		SlowSqlMs:       cfg.SlowSqlMs,
		DfLogRoot:       cfg.DfLogRoot,
		MediaRoot:       cfg.MediaRoot,
		BuildInfo:       cfg.BuildInfo,
		StaticFSHandler: cfg.StaticFSHandler,
	}

	cfg.Dsn = DsnWithReadOnly(cfg.Dsn)
	cfg.Dsn = DsnWithDefaultTimeout(cfg.Dsn)
	cfg.Dsn = DsnWithForeignKeys(cfg.Dsn)

	app.Db, err = GetDb(cfg.Dsn)
	if err != nil {
		return nil, err
	}

	dbFilename := getFileFromDsn(cfg.Dsn)
	if err = initDB(context.Background(), app.Db, dbFilename); err != nil {
		log.Printf("init db: %v", err)
		return nil, err
	}

	if err := checkMinimumDbSchemaVersion(context.Background(), app.Db); err != nil {
		return nil, err
	}

	app.CacheDb, err = GetCacheDb(context.Background())
	if err != nil {
		return nil, err
	}

	app.Tpl = template.Must(template.New("").Funcs(template.FuncMap{
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
		"appVersion":   func() string { return app.BuildInfo.Version },
		"appCommit":    func() string { return app.BuildInfo.Commit },
		"appBuildDate": func() string { return app.BuildInfo.BuildDate },
	}).ParseFS(cfg.TemplatesFS, "templates/*.gohtml", "templates/fragments/*.gohtml"))

	mux := app.newMux()
	srv := &http.Server{
		Addr:    cfg.Bind,
		Handler: mux,
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	ctrl := Controller{app: app, srv: srv, ctx: ctx, cancel: cancel, ready: make(chan struct{})}

	go func() {
		if err := app.loadKnownFiles(ctx, cfg.DfLog); err != nil {
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
	if c != nil && c.app != nil && c.app.Db != nil {
		if err := c.app.Db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c != nil && c.app != nil && c.app.CacheDb != nil {
		if err := c.app.CacheDb.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (app *App) newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleBrowse)
	mux.HandleFunc("/gallery/{ripper_host}/{gid}", app.handleGallery)
	mux.HandleFunc("/gallery/{ripper_host}/{gid}/{file_id}", app.handleGalleryFile)
	mux.HandleFunc("/gallery-file-tags/{ripper_host}/{gid}", app.handleGalleryFileTagsFragment)
	mux.HandleFunc("/file/{ripper_host}/{file_id}", app.handleFileStandalone)
	mux.HandleFunc("/file/{ripper_host}/{file_id}/galleries", app.handleFileGalleryFragment)
	mux.HandleFunc("/tags", app.handleTags)
	mux.HandleFunc("/tag/{tag_name}", app.handleTagDetail)
	mux.HandleFunc("/search", app.handleSearch)
	mux.HandleFunc("/search/galleries", app.handleSearchGalleries)
	mux.HandleFunc("/search/files", app.handleSearchFiles)
	mux.HandleFunc("/search/tags", app.handleSearchTags)
	mux.HandleFunc("/user/{ripper_host}/{user_name}", app.handleUser)
	mux.HandleFunc("/user/{ripper_host}/{user_name}/galleries", app.handleUserGalleries)
	mux.HandleFunc("/user/{ripper_host}/{user_name}/files", app.handleUserFiles)
	mux.HandleFunc("/random/gallery", app.handleRandomGallery)
	mux.HandleFunc("/random/file", app.handleRandomFile)
	mux.HandleFunc("/random/page", app.handleRandomPage)
	mux.HandleFunc("/stats", app.handleStats)

	mux.HandleFunc("GET /api/", app.asApi(app.handle404))
	mux.HandleFunc("GET /api/galleries", app.asApi(app.handleBrowse))
	mux.HandleFunc("GET /api/gallery/{ripper_host}/{gid}", app.asApi(app.handleGallery))
	mux.HandleFunc("GET /api/gallery/{ripper_host}/{gid}/{file_id}", app.asApi(app.handleGalleryFile))
	mux.HandleFunc("GET /api/gallery-file-tags/{ripper_host}/{gid}", app.asApi(app.handleGalleryFileTagsFragment))
	mux.HandleFunc("GET /api/file/{ripper_host}/{file_id}", app.asApi(app.handleFileStandalone))
	mux.HandleFunc("GET /api/file/{ripper_host}/{file_id}/galleries", app.asApi(app.handleFileGalleryFragment))
	mux.HandleFunc("GET /api/tags", app.asApi(app.handleTags))
	mux.HandleFunc("GET /api/tag/{tag_name}", app.asApi(app.handleTagDetail))
	mux.HandleFunc("GET /api/search", app.asApi(app.handleSearch))
	mux.HandleFunc("GET /api/search/galleries", app.asApi(app.handleSearchGalleries))
	mux.HandleFunc("GET /api/search/tags", app.asApi(app.handleSearchTags))
	mux.HandleFunc("GET /api/search/files", app.asApi(app.handleSearchFiles))
	mux.HandleFunc("GET /api/user/{ripper_host}/{user_name}", app.asApi(app.handleUser))
	mux.HandleFunc("GET /api/user/{ripper_host}/{user_name}/galleries", app.asApi(app.handleUserGalleries))
	mux.HandleFunc("GET /api/user/{ripper_host}/{user_name}/files", app.asApi(app.handleUserFiles))
	mux.HandleFunc("GET /api/random/gallery", app.asApi(app.handleRandomGallery))
	mux.HandleFunc("GET /api/random/file", app.asApi(app.handleRandomFile))
	mux.HandleFunc("GET /api/stats", app.asApi(app.handleStats))

	mux.HandleFunc("/media/", app.handleMedia)

	// For development:
	//mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.HandleFunc("/static/", app.handleStatic)

	mux.HandleFunc("/about", app.handleAbout)
	//mux.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
	//	app.renderError(r.Context(), w, &types.Perf{}, http.StatusInternalServerError, fmt.Errorf("foobar"))
	//})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok")) })

	var wrapped http.Handler
	wrapped = app.logMiddleware(mux)
	wrapped = app.tinyOptimizeDb(mux)
	return wrapped
}

func (app *App) asApi(handler func(w http.ResponseWriter, r *http.Request)) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		handler(w, withRenderMode(r, RenderJSON))
	}
}

func (app *App) logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		dur := time.Since(start)
		log.Printf("%s %s %v", r.Method, r.URL.Path, dur.Round(time.Millisecond))
	})
}

// tinyOptimizeDb runs a row-limited optimize. It's probably faster to optimize queries on 400-10000 rows than it is to wait 2 minutes for a worse-case response.
func (app *App) tinyOptimizeDb(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
			return app.withSQL(ctx, func(ctx context.Context) error {
				pragma1 := "PRAGMA analysis_limit=10000"
				pragma2 := "PRAGMA optimize"
				if _, err = app.Db.ExecContext(ctx, pragma1); err != nil {
					log.Printf("pragma error %q: %v", pragma1, err)
					if _, err = app.Db.ExecContext(ctx, pragma2); err != nil {
						log.Printf("pragma error %q: %v", pragma2, err)
					}
				}
				return err
			})
		})
		if err != nil {
			app.renderError(r.Context(), w, &types.Perf{}, http.StatusInternalServerError, err)
			return
		}
		_ = p
		next.ServeHTTP(w, r)
	})
}

type renderErrorTemplateKey struct{}

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

func (app *App) render(ctx context.Context, w http.ResponseWriter, name string, data any) {
	select {
	case <-ctx.Done():
		return
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
		w.Header().Set("X-App-Version", app.BuildInfo.Version)
		w.Header().Set("X-App-Commit", app.BuildInfo.Commit)
		w.Header().Set("X-App-Build-Date", app.BuildInfo.BuildDate)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(data)
		return
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
	err := app.Tpl.ExecuteTemplate(w, name, data)
	if err != nil {
		w.Write([]byte(err.Error()))
	}
}

// Same as render, but fragments shouldn't clear the JS cookie
func (app *App) renderFragment(ctx context.Context, w http.ResponseWriter, name string, data any) {
	select {
	case <-ctx.Done():
		return
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
		w.Header().Set("X-App-Version", app.BuildInfo.Version)
		w.Header().Set("X-App-Commit", app.BuildInfo.Commit)
		w.Header().Set("X-App-Build-Date", app.BuildInfo.BuildDate)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(data)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Set short-lived cache for HTML pages to allow quick back/forward without staleness
	w.Header().Set("Cache-Control", "private, max-age=60")
	err := app.Tpl.ExecuteTemplate(w, name, data)
	if err != nil {
		w.Write([]byte(err.Error()))
	}
}

func (app *App) renderError(ctx context.Context, w http.ResponseWriter, perf *types.Perf, status int, err error) {
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
		w.Header().Set("X-App-Version", app.BuildInfo.Version)
		w.Header().Set("X-App-Commit", app.BuildInfo.Commit)
		w.Header().Set("X-App-Build-Date", app.BuildInfo.BuildDate)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(model)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	tpl := "error.gohtml"
	if v, ok := ctx.Value(renderErrorTemplateKey{}).(string); ok {
		tpl = v
	}
	_ = app.Tpl.ExecuteTemplate(w, tpl, model)
}

func (app *App) renderErrorFragment(ctx context.Context, w http.ResponseWriter, perf *types.Perf, status int, err error) {
	errorFragmentCtx := context.WithValue(ctx, renderErrorTemplateKey{}, "error_fragment.gohtml")
	app.renderError(errorFragmentCtx, w, perf, status, err)
}

func (app *App) httpRedirect(ctx context.Context, w http.ResponseWriter, r *http.Request, perf *types.Perf, url string, code int) {
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

// sendFile sends a static file to the client. true = sent, false = not sent
func sendFile(file string, w http.ResponseWriter, r *http.Request) bool {
	st, err := os.Stat(file)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	// Compute ETag from size and modtime
	etag := fmt.Sprintf("\"%x-%x\"", st.ModTime().Unix(), st.Size())
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", st.ModTime().UTC().Format(http.TimeFormat))
	// Set sensible cache headers for media files
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		if t, err := time.Parse(http.TimeFormat, ims); err == nil {
			if !st.ModTime().After(t) {
				w.WriteHeader(http.StatusNotModified)
				return true
			}
		}
	}
	// Use ServeContent to respect range requests
	f, err := os.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	http.ServeContent(w, r, filepath.Base(file), st.ModTime(), f)
	return true
}
