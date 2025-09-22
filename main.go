package main

import (
	"bufio"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golocalgal/types"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
	_ "modernc.org/sqlite"

	_ "golocalgal/types"
)

//go:embed templates/*
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS
var staticFSHandler = http.FileServerFS(staticFS)

var (
	db     *sql.DB
	tpl    *template.Template
	srcMap map[int64]string // ripper_id -> name, optional cache
)

// Version metadata populated via -ldflags at build time
var (
	Version   = "dev"
	Commit    = ""
	BuildDate = ""
)

// perfTracker helps measure SQL and request timings
// It attaches a Perf tracker to the provided parent context to preserve request cancellation/deadlines.
func perfTracker(parent context.Context, next func(ctx context.Context, p *types.Perf) error) (types.Perf, error) {
	start := time.Now()
	p := types.Perf{Start: start}
	ctx := context.WithValue(parent, perfKey{}, &p)
	select {
	case <-ctx.Done():
		p.PageTime = time.Since(start)
		return p, ctx.Err()
	default:
	}
	err := next(ctx, &p)
	p.PageTime = time.Since(start)
	return p, err
}

type perfKey struct{}

type countTime struct {
	count int
	dur   time.Duration
}

func withSQL(ctx context.Context, fn func() error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	perf, _ := ctx.Value(perfKey{}).(*types.Perf)
	start := time.Now()
	err := fn()
	elapsed := time.Since(start)
	if perf != nil {
		perf.SQLCount++
		perf.SQLTime += elapsed
	}
	// Slow query logging: threshold via SLOW_SQL_MS env (default 100ms)
	if slowSqlMs >= 0 && elapsed >= time.Duration(slowSqlMs)*time.Millisecond {
		if _, file, line, ok := runtime.Caller(1); ok {
			log.Printf("SLOW SQL at %s:%d: %v (>= %dms)", file, line, elapsed.Round(time.Millisecond), slowSqlMs)
		} else {
			log.Printf("SLOW SQL: %v (>= %dms)", elapsed.Round(time.Millisecond), slowSqlMs)
		}
	}
	return err
}

var knownFilePaths map[string][]string
var mediaRoot string
var dfLogRoot string

var slowSqlMs int

func main() {
	var help bool
	flag.BoolVar(&help, "h", false, "show help")
	flag.BoolVar(&help, "help", false, "show help")
	flag.Parse()
	if help {
		flag.CommandLine.SetOutput(os.Stdout)
		fmt.Println("Usage: localgal [options]")
		fmt.Println("Options:")
		flag.PrintDefaults()
		fmt.Println("Environment Variables:")
		fmt.Println("  BIND:\tlisten address, default `127.0.0.1:5037` (to listen on all addresses, specify `:5037`)")
		fmt.Println("  SQLITE_DSN:\tsqlite data source name (connection string), default `file:ripme.sqlite`")
		fmt.Println("  MEDIA_ROOT:\trip base directory, default: `./rips`")
		fmt.Println("  SLOW_SQL_MS:\tduration threshold to log slow sql queries, milliseconds, default `100`")
		fmt.Println("  DFLOG:\tdownloaded file log, default `./ripme.downloaded.files.log`")
		fmt.Println("  DFLOG_ROOT:\tbase directory to resolve relative paths in DFLOG from, default directory that DFLOG is in")
		os.Exit(0)
	}

	log.Printf("Starting golocalml")
	bind := getEnv("BIND", "127.0.0.1:5037")
	dsn := getEnv("SQLITE_DSN", "file:ripme.sqlite?mode=ro&_query_only=1&_busy_timeout=10000&_foreign_keys=ON")
	dsn = forceReadOnlyDsn(dsn)

	slowSqlMs = 100
	if v := os.Getenv("SLOW_SQL_MS"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n >= -1 {
			slowSqlMs = n
		}
	}

	var err error
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1) // sqlite preferred in many cases

	dbFilename := getFileFromDsn(dsn)
	if err = initDB(db, dbFilename); err != nil {
		log.Fatalf("init db: %v", err)
	}

	tpl = template.Must(template.New("").Funcs(template.FuncMap{
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
		"fmtMillis": func(d time.Duration) string { return d.Round(time.Millisecond).String() },
		"finalPageMillis": func(p types.Perf) string {
			if !p.Start.IsZero() {
				return time.Since(p.Start).Round(time.Millisecond).String()
			}
			return p.PageTime.Round(time.Millisecond).String()
		},
		// Version info helpers for templates
		"appVersion":   func() string { return Version },
		"appCommit":    func() string { return Commit },
		"appBuildDate": func() string { return BuildDate },
	}).ParseFS(templatesFS, "templates/*.gohtml"))

	http.HandleFunc("/", handleBrowse)
	http.HandleFunc("/gallery/{ripper_host}/{gid}", handleGallery)
	http.HandleFunc("/gallery/{ripper_host}/{gid}/{file_id}", handleGalleryFile)
	http.HandleFunc("/file/{ripper_host}/{file_id}", handleFileStandalone)
	http.HandleFunc("/file/{ripper_host}/{file_id}/galleries", handleFileGalleryFragment)
	http.HandleFunc("/tags", handleTags)
	http.HandleFunc("/tag/{tag_name}", handleTagDetail)
	http.HandleFunc("/random/gallery", handleRandomGallery)
	http.HandleFunc("/random/file", handleRandomFile)

	asApi := func(handler func(w http.ResponseWriter, r *http.Request)) func(w http.ResponseWriter, r *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			handler(w, withRenderMode(r, RenderJSON))
		}
	}
	http.HandleFunc("GET /api/", asApi(handle404))
	http.HandleFunc("GET /api/galleries", asApi(handleBrowse))
	http.HandleFunc("GET /api/gallery/{ripper_host}/{gid}", asApi(handleGallery))
	http.HandleFunc("GET /api/gallery/{ripper_host}/{gid}/{file_id}", asApi(handleGalleryFile))
	http.HandleFunc("GET /api/file/{ripper_host}/{file_id}", asApi(handleFileStandalone))
	http.HandleFunc("GET /api/file/{ripper_host}/{file_id}/galleries", asApi(handleFileGalleryFragment))
	http.HandleFunc("GET /api/tags", asApi(handleTags))
	http.HandleFunc("GET /api/tag/{tag_name}", asApi(handleTagDetail))
	http.HandleFunc("GET /api/random/gallery", asApi(handleRandomGallery))
	http.HandleFunc("GET /api/random/file", asApi(handleRandomFile))

	// Media server: dynamic resolution using MEDIA_ROOT and known_files.log
	mediaRoot = getEnv("MEDIA_ROOT", "./rips")
	dfLog := getEnv("DFLOG", "./ripme.downloaded.files.log")
	defaultDfLogRoot := getDefaultDfLogRoot(dfLog)
	dfLogRoot = getEnv("DFLOG_ROOT", defaultDfLogRoot)
	loadKnownFiles(dfLog)
	http.HandleFunc("/media/", handleMedia)

	http.HandleFunc("/static/", handleStatic)
	http.HandleFunc("/about", handleAbout)
	//http.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
	//	renderError(r.Context(), w, &types.Perf{}, http.StatusInternalServerError, fmt.Errorf("foobar"))
	//})
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); _, _ = w.Write([]byte("ok")) })

	log.Printf("LocalGal listening on %s", bind)
	log.Fatal(http.ListenAndServe(bind, logMiddleware(http.DefaultServeMux)))
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

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getFileFromDsn(dsn string) string {
	_, afterFile, _ := strings.Cut(dsn, "file:")
	filename, _, _ := strings.Cut(afterFile, "?")
	return filename
}

func initDB(db *sql.DB, filename string) error {
	var pragmas []string

	// Not sure why anyone would try :memory:, but handle it anyway
	if ":memory:" != filename {
		stat, err := os.Stat(filename)
		if err != nil {
			return err
		}
		vm, _ := mem.VirtualMemory()
		statKib := max(0, stat.Size()/1024)
		availableKib := vm.Available / 1024
		maxCacheKib := min(uint64(statKib), availableKib, 2097152)
		pragmas = append(pragmas, fmt.Sprintf("PRAGMA cache_size=%d", maxCacheKib))
		//pragmas = append(pragmas, "PRAGMA cache_size=2097152;")  // kibibytes; 2GiB
	}

	// Set pragmas for large DBs
	//pragmas = append(pragmas, "PRAGMA journal_mode=WAL;")
	//pragmas = append(pragmas, "PRAGMA synchronous=NORMAL;")
	pragmas = append(pragmas, "PRAGMA temp_store=MEMORY;")
	//pragmas = append(pragmas, "PRAGMA mmap_size=268435456;") // 256MB

	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			log.Printf("pragma error %q: %v", p, err)
		}
	}
	return nil
}

func forceReadOnlyDsn(dsn string) string {
	base, query, _ := strings.Cut(dsn, "?")
	params := strings.Split(query, "&")
	if !slices.Contains(params, "mode=ro") {
		params = append(params, "mode=ro")
	}
	if !slices.Contains(params, "_query_only=1") {
		params = append(params, "_query_only=1")
	}
	if !slices.Contains(params, "_foreign_keys=ON") {
		params = append(params, "_foreign_keys=ON")
	}
	return base + "?" + strings.Join(params, "&")
}

// loadKnownFiles builds knownFilePaths from a log file: each line is a relative or absolute path to a file.
func loadKnownFiles(path string) {
	log.Printf("Loading known files")
	knownFilePaths = map[string][]string{}
	dir := filepath.Dir(path)
	f, err := os.Open(path)
	if err != nil {
		log.Printf("known file log open: %v", err)
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		p := strings.TrimSpace(s.Text())
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		base := filepath.Base(p)
		target := filepath.Join(dir, p)
		if !filepath.IsAbs(target) {
			target = filepath.Join(dfLogRoot, target)
			target = filepath.Clean(target)
			target, err = filepath.Rel(dfLogRoot, target)
			if err != nil {
				log.Printf("Not able to resolve clean relative path for %v: %v", p, err)
			}
		}
		knownFilePaths[base] = append(knownFilePaths[base], target)
	}
	if err := s.Err(); err != nil {
		log.Printf("known file log scan: %v", err)
	}
	log.Printf("known file log loaded %d filenames", len(knownFilePaths))
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func parsePageParams(r *http.Request, defSize int) (page, size int) {
	q := r.URL.Query()
	page = atoiDefault(q.Get("page"), 1)
	if page < 1 {
		page = 1
	}
	size = atoiDefault(q.Get("size"), defSize)
	if size < 1 || size > 200 {
		size = defSize
	}
	return
}

func handle404(w http.ResponseWriter, r *http.Request) {
	renderError(r.Context(), w, &types.Perf{}, http.StatusNotFound, nil)
}

func handleStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=86400")
	staticFSHandler.ServeHTTP(w, r)
}

func handleAbout(w http.ResponseWriter, r *http.Request) {
	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		model := map[string]any{"Perf": *perf}
		return render(ctx, w, "about.gohtml", model)
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func handleBrowse(w http.ResponseWriter, r *http.Request) {
	if !(r.URL.Path == "/" || r.URL.Path == "/api/galleries") {
		renderError(r.Context(), w, &types.Perf{}, http.StatusNotFound, nil)
		return
	}
	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		page, size := parsePageParams(r, 40)
		offset := (page - 1) * size
		var total int
		if err := withSQL(ctx, func() error {
			return db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				  FROM album
				 WHERE fetch_count > 0
			`).Scan(&total)
		}); err != nil {
			return err
		}
		var list []types.Album
		if err := withSQL(ctx, func() error {
			rows, err := db.QueryContext(ctx, `
				SELECT a.album_id
				     , a.ripper_id
				     , r.name AS ripper_name
				     , r.host AS ripper_host
				     , a.gid
				     , a.uploader
				     , a.title
				     , a.description
				     , a.created_ts
				     , a.modified_ts
				     , a.hidden
				     , a.removed
				     , a.last_fetch_ts
				     , a.inserted_ts
				     , (
				    SELECT COUNT(*)
				      FROM map_album_remote_file marf
				      JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id AND rf.fetched = 1
				     WHERE marf.album_id = a.album_id
				       AND rf.fetched = 1
				       ) AS file_count
				     , (
				    SELECT rf.remote_file_id
				      FROM map_album_remote_file marf
				      JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
				     WHERE marf.album_id = a.album_id
				       AND rf.fetched = 1
				     ORDER BY marf.remote_file_id
				     LIMIT 1
				       ) AS thumb_remote_file_id
				  FROM album a
				  JOIN ripper r ON r.ripper_id = a.ripper_id
				-- WHERE a.fetch_count > 0 -- not as important as remote_file.fetched=1
				 ORDER BY a.album_id
				 LIMIT ? OFFSET ?
			`, size, offset)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var a types.Album
				var f types.File
				var thumbFileId sql.NullInt64
				if err := rows.Scan(
					&a.AlbumId,
					&a.RipperId,
					&a.RipperName,
					&a.RipperHost,
					&a.Gid,
					&a.Uploader,
					&a.Title,
					&a.Description,
					&a.CreatedTs,
					&a.ModifiedTs,
					&a.Hidden,
					&a.Removed,
					&a.LastFetchTs,
					&a.InsertedTs,
					&a.FileCount,
					&thumbFileId,
				); err != nil {
					return err
				}
				// If an album has no fetched files, thumb_remote_file_id will be null
				if thumbFileId.Valid {
					f.FileId = thumbFileId.Int64
					a.Thumb = f
					list = append(list, a)
				}
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		for i := range list {
			thumb := list[i].Thumb
			if err := withSQL(ctx, func() error {
				return db.QueryRowContext(ctx, `
					SELECT rf.filename
					     , mt.name AS mime_type
					  FROM remote_file rf
					  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
					 WHERE rf.remote_file_id = ? AND rf.fetched = 1
			`, thumb.FileId).Scan(&thumb.Filename, &thumb.MimeType)
			}); err != nil {
				return err
			}
			list[i].Thumb = thumb
		}
		// Populate href
		for i := range list {
			list[i].HrefPage = fmt.Sprintf("/gallery/%s/%s", list[i].RipperHost, list[i].Gid)
			list[i].Thumb.HrefPage = fmt.Sprintf("/media/%s/%s/%d", list[i].RipperHost, list[i].Gid, list[i].Thumb.FileId)
			if list[i].Thumb.Filename.Valid {
				list[i].Thumb.HrefMedia = fmt.Sprintf("/media/%s/%s/%s", list[i].RipperHost, list[i].Gid, list[i].Thumb.Filename.String)
			}
		}
		model := types.BrowsePage{Albums: list, Page: page, PageSize: size, Total: total, HasPrev: page > 1, HasNext: offset+len(list) < total, BasePage: types.BasePage{Perf: perf}}
		return render(ctx, w, "browse.gohtml", model)
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
	_ = p
}

// handleGallery handles /gallery/{ripper_host}/{gid}
func handleGallery(w http.ResponseWriter, r *http.Request) {
	ripperHost := r.PathValue("ripper_host")
	gid := r.PathValue("gid")
	if ripperHost == "" || gid == "" {
		renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /gallery/{ripper_host}/{gid}"))
		return
	}
	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var a types.Album
		if err := withSQL(ctx, func() error {
			return db.QueryRowContext(ctx, `
				SELECT a.album_id
				     , a.ripper_id
				     , r.name AS ripper_name
				     , r.host AS ripper_host
				     , a.gid
				     , a.uploader
				     , a.title
				     , a.description
				     , a.created_ts
				     , a.modified_ts
				     , a.hidden
				     , a.removed
				     , a.last_fetch_ts
				     , a.inserted_ts
				  FROM album a
				  JOIN ripper r ON r.ripper_id = a.ripper_id
				 WHERE r.host = ?
				   AND a.gid = ?
			`, ripperHost, gid).Scan(
				&a.AlbumId,
				&a.RipperId,
				&a.RipperName,
				&a.RipperHost,
				&a.Gid,
				&a.Uploader,
				&a.Title,
				&a.Description,
				&a.CreatedTs,
				&a.ModifiedTs,
				&a.Hidden,
				&a.Removed,
				&a.LastFetchTs,
				&a.InsertedTs,
			)
		}); err != nil {
			return err
		}
		page, size := parsePageParams(r, 60)
		offset := (page - 1) * size
		var total int
		if err := withSQL(ctx, func() error {
			return db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				  FROM map_album_remote_file m
				  JOIN remote_file rf ON rf.remote_file_id = m.remote_file_id AND rf.fetched = 1
				 WHERE m.album_id = ?
			`, a.AlbumId).Scan(&total)
		}); err != nil {
			return err
		}
		var files []types.File
		if err := withSQL(ctx, func() error {
			rows, err := db.QueryContext(ctx, `
				SELECT rf.remote_file_id
				     --, r.name AS ripper_name
				     --, r.host AS ripper_host
				     , rf.urlid
				     , rf.filename
				     , mt.name AS mime_type
				     , rf.title
				     , rf.description
				     , rf.uploaded_ts
				     , rf.uploader
				     , rf.hidden
				     , rf.removed
				     , rf.inserted_ts
				  FROM map_album_remote_file marf
				  JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id AND rf.fetched = 1
				  JOIN ripper r ON r.ripper_id = rf.ripper_id
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 WHERE marf.album_id = ?
				 ORDER BY marf.remote_file_id
				 LIMIT ? OFFSET ?
			`, a.AlbumId, size, offset)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var f types.File
				if err := rows.Scan(
					&f.FileId,
					//&f.RipperName, // TODO just take the value from the album we already fetched
					//&f.RipperHost, // TODO just take the value from the album we already fetched
					&f.Urlid,
					&f.Filename,
					&f.MimeType,
					&f.Title,
					&f.Description,
					&f.UploadedTs,
					&f.Uploader,
					&f.Hidden,
					&f.Removed,
					&f.InsertedTs,
				); err != nil {
					return err
				}
				f.AlbumId = a.AlbumId
				files = append(files, f)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		// Fetch tags for album and distinct tags from its files
		var albumTags, fileTags []types.Tag
		if err := withSQL(ctx, func() error {
			rows, e := db.QueryContext(ctx, `
				SELECT t.tag_id, t.name
				  FROM map_album_tag mat
				  JOIN tag t ON t.tag_id = mat.tag_id
				 WHERE mat.album_id = ?
				 ORDER BY t.name
			`, a.AlbumId)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var t types.Tag
				if err := rows.Scan(&t.TagId, &t.Name); err != nil {
					return err
				}
				albumTags = append(albumTags, t)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		if err := withSQL(ctx, func() error {
			rows, e := db.QueryContext(ctx, `
				SELECT t.tag_id, t.name, COUNT(*) as count
				  FROM tag t
				  JOIN map_remote_file_tag mrft ON mrft.tag_id = t.tag_id
				  JOIN map_album_remote_file marf ON marf.remote_file_id = mrft.remote_file_id
				  JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id AND rf.fetched = 1
				 WHERE marf.album_id = ?
				 GROUP BY t.tag_id, t.name
				 ORDER BY count DESC
				 LIMIT 100 -- some albums might have a million tags...
			`, a.AlbumId)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var t types.Tag
				if err := rows.Scan(&t.TagId, &t.Name, &t.Count); err != nil {
					return err
				}
				fileTags = append(fileTags, t)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		// Populate href
		a.HrefPage = fmt.Sprintf("/gallery/%s/%s", a.RipperHost, a.Gid)
		for i := range files {
			files[i].HrefPage = fmt.Sprintf("/gallery/%s/%s/%d", a.RipperHost, a.Gid, files[i].FileId)
			if files[i].Filename.Valid {
				files[i].HrefMedia = fmt.Sprintf("/media/%s/%s/%s", a.RipperHost, a.Gid, files[i].Filename.String)
			}
		}
		model := types.GalleryPage{Album: a, Files: files, Page: page, PageSize: size, Total: total, HasPrev: page > 1, HasNext: offset+len(files) < total, AlbumTags: albumTags, FileTags: fileTags, BasePage: types.BasePage{Perf: perf}}
		return render(ctx, w, "gallery.gohtml", model)
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleGallery handles /gallery/{ripper_host}/{gid}/{file_id}
func handleGalleryFile(w http.ResponseWriter, r *http.Request) {
	ripperHost := r.PathValue("ripper_host")
	gid := r.PathValue("gid")
	fileIdString := r.PathValue("file_id")
	if ripperHost == "" || gid == "" || fileIdString == "" {
		renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /gallery/{ripper_host}/{gid}/{file_id}"))
		return
	}
	fileId, err := strconv.ParseInt(fileIdString, 10, 64)
	if err != nil || fileId <= 0 {
		renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("file_id must be a positive integer"))
		return
	}

	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var a types.Album
		if err := withSQL(ctx, func() error {
			return db.QueryRowContext(ctx, `
				SELECT a.album_id
				     , a.ripper_id
				     , r.name AS ripper_name
				     , r.host AS ripper_host
				     , a.gid
				     , a.uploader
				     , a.title
				     , a.description
				     , a.created_ts
				     , a.modified_ts
				     , a.hidden
				     , a.removed
				     , a.last_fetch_ts
				     , a.inserted_ts
				  FROM album a
				  JOIN ripper r ON r.ripper_id = a.ripper_id
				 WHERE r.host = ?
				   AND a.gid = ?
			`, ripperHost, gid).Scan(
				&a.AlbumId,
				&a.RipperId,
				&a.RipperName,
				&a.RipperHost,
				&a.Gid,
				&a.Uploader,
				&a.Title,
				&a.Description,
				&a.CreatedTs,
				&a.ModifiedTs,
				&a.Hidden,
				&a.Removed,
				&a.LastFetchTs,
				&a.InsertedTs,
			)
		}); err != nil {
			return err
		}
		var f types.File
		if err := withSQL(ctx, func() error {
			return db.QueryRowContext(ctx, `
				SELECT rf.remote_file_id
				     , r.name AS ripper_name
				     , r.host AS ripper_host
				     , rf.urlid
				     , rf.filename
				     , mt.name AS mime_type
				     , rf.title
				     , rf.description
				     , rf.uploaded_ts
				     , rf.uploader
				     , rf.hidden
				     , rf.removed
				     , rf.inserted_ts
				  FROM remote_file rf
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				  JOIN map_album_remote_file m ON m.remote_file_id = rf.remote_file_id
				  JOIN ripper r on rf.ripper_id = r.ripper_id
				 WHERE m.album_id = ?
				   AND rf.remote_file_id = ?
				   AND rf.fetched = 1
			`, a.AlbumId, fileId).Scan(
				&f.FileId,
				&f.RipperName, // TODO use value from Album
				&f.RipperHost, // TODO use value from Album
				&f.Urlid,
				&f.Filename,
				&f.MimeType,
				&f.Title,
				&f.Description,
				&f.UploadedTs,
				&f.Uploader,
				&f.Hidden,
				&f.Removed,
				&f.InsertedTs,
			)
		}); err != nil {
			return err
		}
		// Prev/Next within this album by remote_file_id
		var prev []types.File
		if err := withSQL(ctx, func() error {
			rows, e := db.QueryContext(ctx, `
				-- Step 1: On the mapping table, seek previous remote_file_id values (< current) with ORDER BY DESC LIMIT 3 using PK (album_id, remote_file_id).
				-- Step 2: Join the small set to remote_file and filter to available rows.
				-- Step 3: Re-order ascending for display as chronological prev list.
				SELECT rf.remote_file_id
				     , rf.urlid
				     , rf.filename
				     , mt.name AS mime_type
				     , rf.title
				     , rf.description
				     , rf.uploaded_ts
				     , rf.uploader
				     , rf.hidden
				     , rf.removed
				  FROM (
				      -- Performance: do the LIMIT on the indexed mapping table first; avoids joining many rows only to drop them.
				      -- Uses composite PK (album_id, remote_file_id) for efficient range+order scan.
				      SELECT marf.remote_file_id
				        FROM map_album_remote_file marf
				        JOIN remote_file rf ON marf.remote_file_id = rf.remote_file_id AND rf.fetched = 1
				       WHERE marf.album_id = ?
				         AND marf.remote_file_id < ?
				       ORDER BY marf.remote_file_id DESC
				       LIMIT 3
				       ) s
				  JOIN remote_file rf ON rf.remote_file_id = s.remote_file_id AND rf.fetched = 1
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 ORDER BY rf.remote_file_id
				`, a.AlbumId, f.FileId)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var pf types.File
				if err := rows.Scan(
					&pf.FileId,
					&pf.Urlid,
					&pf.Filename,
					&pf.MimeType,
					&pf.Title,
					&pf.Description,
					&pf.UploadedTs,
					&pf.Uploader,
					&pf.Hidden,
					&pf.Removed,
				); err != nil {
					return err
				}
				prev = append(prev, pf)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		var next []types.File
		if err := withSQL(ctx, func() error {
			rows, e := db.QueryContext(ctx, `
				SELECT rf.remote_file_id
				     , rf.urlid
				     , rf.filename
				     , mt.name AS mime_type
				     , rf.title
				     , rf.description
				     , rf.uploaded_ts
				     , rf.uploader
				     , rf.hidden
				     , rf.removed
				  FROM map_album_remote_file marf
				  JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id AND rf.fetched = 1
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 WHERE marf.album_id = ?
				   AND marf.remote_file_id > ?
				-- Performance: order and range on mapping table column aligned with PK; avoids sort on rf and uses index for next-page scan.
				 ORDER BY marf.remote_file_id
				 LIMIT 3
				`, a.AlbumId, f.FileId)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var nf types.File
				if err := rows.Scan(
					&nf.FileId,
					&nf.Urlid,
					&nf.Filename,
					&nf.MimeType,
					&nf.Title,
					&nf.Description,
					&nf.UploadedTs,
					&nf.Uploader,
					&nf.Hidden,
					&nf.Removed,
				); err != nil {
					return err
				}
				next = append(next, nf)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		// File tags
		var fileTags []types.Tag
		if err := withSQL(ctx, func() error {
			rows, e := db.QueryContext(ctx, `
				-- Step 1: Drive from map_remote_file_tag to use PK (remote_file_id, tag_id) for fast lookup by file id.
				-- Step 2: Join to tag to fetch tag names for display.
				-- Step 3: Order alphabetically.
				SELECT t.tag_id, t.name
				  FROM map_remote_file_tag mrft
				  JOIN tag t ON t.tag_id = mrft.tag_id
				 WHERE mrft.remote_file_id = ?
				 ORDER BY t.name
				`, f.FileId)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var t types.Tag
				if err := rows.Scan(&t.TagId, &t.Name); err != nil {
					return err
				}
				fileTags = append(fileTags, t)
			}
			return rows.Err()
		}); err != nil {
			return err
		}

		// Populate href
		a.HrefPage = fmt.Sprintf("/gallery/%s/%s", a.RipperHost, a.Gid)
		f.HrefPage = fmt.Sprintf("/gallery/%s/%s/%d", a.RipperHost, a.Gid, f.FileId)
		if f.Filename.Valid {
			f.HrefMedia = fmt.Sprintf("/media/%s/%s/%s", a.RipperHost, a.Gid, f.Filename.String)
		}
		for i := range prev {
			prev[i].HrefPage = fmt.Sprintf("/gallery/%s/%s/%d", a.RipperHost, a.Gid, prev[i].FileId)
			if prev[i].Filename.Valid {
				prev[i].HrefMedia = fmt.Sprintf("/media/%s/%s/%s", a.RipperHost, a.Gid, prev[i].Filename.String)
			}
		}
		for i := range next {
			next[i].HrefPage = fmt.Sprintf("/gallery/%s/%s/%d", a.RipperHost, a.Gid, next[i].FileId)
			if next[i].Filename.Valid {
				next[i].HrefMedia = fmt.Sprintf("/media/%s/%s/%s", a.RipperHost, a.Gid, next[i].Filename.String)
			}
		}

		asyncAlbums := isClientJsOn(r)
		if asyncAlbums {
			model := types.FilePage{File: f, Prev: prev, Next: next, FileTags: fileTags, AsyncAlbums: true, CurrentAlbum: a, ShowPrevNext: true, BasePage: types.BasePage{Perf: perf}}
			return render(ctx, w, "file.gohtml", model)
		}
		// Albums containing this file
		albums, err := getRelatedAlbums(r.Context(), fileId)
		if err != nil {
			return err
		}
		model := types.FilePage{File: f, Prev: prev, Next: next, FileTags: fileTags, Albums: albums, CurrentAlbum: a, ShowPrevNext: true, BasePage: types.BasePage{Perf: perf}}
		return render(ctx, w, "file.gohtml", model)
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleFileStandalone handles /file/{ripper_host}/{file_id}/
func handleFileStandalone(w http.ResponseWriter, r *http.Request) {
	ripperHost := r.PathValue("ripper_host")
	fileIdString := r.PathValue("file_id")
	if ripperHost == "" || fileIdString == "" {
		renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /file/{ripper_host}/{file_id}"))
		return
	}
	fileId, err := strconv.ParseInt(fileIdString, 10, 64)
	if err != nil || fileId <= 0 {
		renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("invalid file id, must be a positive integer"))
		return
	}

	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var f types.File
		if err := withSQL(ctx, func() error {
			f.RipperHost = ripperHost
			return db.QueryRowContext(ctx, `
				SELECT rf.remote_file_id
				     , rf.urlid
				     , rf.filename
				     , mt.name AS mime_type
				     , rf.title
				     , rf.description
				     , rf.uploaded_ts
				     , rf.uploader
				     , rf.hidden
				     , rf.removed
				     , rf.inserted_ts
				  FROM remote_file rf
				  JOIN ripper r ON r.ripper_id = rf.ripper_id
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 WHERE r.host = ?
				   AND rf.remote_file_id = ?
				   AND rf.fetched = 1
			`, ripperHost, fileId).Scan(
				&f.FileId,
				&f.Urlid,
				&f.Filename,
				&f.MimeType,
				&f.Title,
				&f.Description,
				&f.UploadedTs,
				&f.Uploader,
				&f.Hidden,
				&f.Removed,
				&f.InsertedTs,
			)
		}); err != nil {
			return err
		}

		var fileTags []types.Tag
		// Standalone file view: no Prev/Next
		if err := withSQL(ctx, func() error {
			rows, e := db.QueryContext(ctx, `
					SELECT t.tag_id, t.name
					  FROM map_remote_file_tag m
					  JOIN tag t ON t.tag_id = m.tag_id
					 WHERE m.remote_file_id = ?
					 ORDER BY t.name
				`, f.FileId)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var t types.Tag
				if err := rows.Scan(&t.TagId, &t.Name); err != nil {
					return err
				}
				fileTags = append(fileTags, t)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		asyncAlbums := isClientJsOn(r) || getRenderMode(ctx) == RenderJSON
		var albums []types.Album
		if !asyncAlbums {
			albums, err = getRelatedAlbums(ctx, f.FileId)
			if err != nil {
				return err
			}
		}

		// Populate href
		if f.Filename.Valid {
			f.HrefMedia = fmt.Sprintf("/media/%s/%s", ripperHost, f.Filename.String)
		}

		// Regular file page
		model := types.FilePage{File: f, FileTags: fileTags, AsyncAlbums: asyncAlbums, Albums: albums, ShowPrevNext: false, BasePage: types.BasePage{Perf: perf}}
		return render(ctx, w, "file.gohtml", model)
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleFileStandalone handles /file/{ripper_host}/{file_id}/galleries/
func handleFileGalleryFragment(w http.ResponseWriter, r *http.Request) {
	// ripperHost is not necessary for now, but want to keep to make replacing file_id with urlid easy in the future
	ripperHost := r.PathValue("ripper_host")
	fileIdString := r.PathValue("file_id")
	if ripperHost == "" || fileIdString == "" {
		renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /file/{ripper_host}/{file_id}/galleries"))
		return
	}
	fileId, err := strconv.ParseInt(fileIdString, 10, 64)
	if err != nil || fileId <= 0 {
		renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("invalid file id, must be a positive integer"))
		return
	}

	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		f := types.File{FileId: fileId}

		var albums []types.Album
		albums, err = getRelatedAlbums(ctx, fileId)
		if err != nil {
			return err
		}

		model := types.FilePage{File: f, Albums: albums, BasePage: types.BasePage{Perf: perf}}
		return renderFragment(ctx, w, "file_galleries.gohtml", model)
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func getRelatedAlbums(ctx context.Context, fileId int64) ([]types.Album, error) {
	var albums []types.Album
	if err := withSQL(ctx, func() error {
		rows, e := db.QueryContext(ctx, `
			SELECT a.album_id
			     , a.ripper_id
			     , r.name AS ripper_name
			     , r.host AS ripper_host
			     , a.gid
			     , a.uploader
			     , a.title
			     , a.description
			     , a.created_ts
			     , a.modified_ts
			     , a.hidden
			     , a.removed
			     , a.last_fetch_ts
			     , a.inserted_ts
			     , (
			    SELECT COUNT(*)
			      FROM map_album_remote_file marf
			      JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id AND rf.fetched = 1
			     WHERE marf.album_id = a.album_id
			       ) AS file_count
			     , (
			    SELECT rf.remote_file_id
			      FROM map_album_remote_file marf
			      JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id AND rf.fetched = 1
			     WHERE marf.album_id = a.album_id
			-- ORDER BY rf.remote_file_id ASC
			     LIMIT 1
			       ) AS thumb
			  FROM album a
			  JOIN ripper r ON r.ripper_id = a.ripper_id
			  JOIN map_album_remote_file marf ON marf.album_id = a.album_id AND marf.remote_file_id = ?
			 ORDER BY a.album_id
		`, fileId)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var a2 types.Album
			var f2 types.File
			if err := rows.Scan(
				&a2.AlbumId,
				&a2.RipperId,
				&a2.RipperName,
				&a2.RipperHost,
				&a2.Gid,
				&a2.Uploader,
				&a2.Title,
				&a2.Description,
				&a2.CreatedTs,
				&a2.ModifiedTs,
				&a2.Hidden,
				&a2.Removed,
				&a2.LastFetchTs,
				&a2.InsertedTs,
				&a2.FileCount,
				&f2.FileId,
			); err != nil {
				return err
			}
			a2.Thumb = f2
			albums = append(albums, a2)
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}
	for i := range albums {
		thumb := albums[i].Thumb
		if err := withSQL(ctx, func() error {
			return db.QueryRowContext(ctx, `
				SELECT rf.filename
				     , mt.name AS mime_type
				  FROM remote_file rf
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 WHERE rf.remote_file_id = ?
				   AND fetched = 1
			`, thumb.FileId).Scan(&thumb.Filename, &thumb.MimeType)
		}); err != nil {
			return nil, err
		}
		albums[i].Thumb = thumb
	}
	// Populate href
	for i := range albums {
		albums[i].HrefPage = fmt.Sprintf("/gallery/%s/%s", albums[i].RipperHost, albums[i].Gid)
		albums[i].Thumb.HrefPage = fmt.Sprintf("/gallery/%s/%s/%d", albums[i].RipperHost, albums[i].Gid, albums[i].Thumb.FileId)
		if albums[i].Thumb.Filename.Valid {
			albums[i].Thumb.HrefMedia = fmt.Sprintf("/media/%s/%s/%s", albums[i].RipperHost, albums[i].Gid, albums[i].Thumb.Filename.String)
		}
	}
	return albums, nil
}

func handleTagDetail(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag_name")
	tag, err := url.QueryUnescape(tag)
	if err != nil {
		renderError(r.Context(), w, &types.Perf{}, 500, err)
		return
	}
	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var t types.Tag
		if err := withSQL(ctx, func() error {
			return db.QueryRowContext(ctx, `
				SELECT tag_id, name
				  FROM tag
				 WHERE name = ?
			`, tag).Scan(&t.TagId, &t.Name)
		}); err != nil {
			return err
		}
		// Albums for tag (with pagination)
		page, size := parsePageParams(r, 40)
		offset := (page - 1) * size
		var total int
		if err := withSQL(ctx, func() error {
			return db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				  FROM album a
				  JOIN map_album_tag mat ON mat.album_id = a.album_id AND mat.tag_id = ?
			`, t.TagId).Scan(&total)
		}); err != nil {
			return err
		}
		var albums []types.Album
		if err := withSQL(ctx, func() error {
			rows, e := db.QueryContext(ctx, `
				SELECT a.album_id
				     , a.ripper_id
				     , r.name AS ripper_name
				     , r.host AS ripper_host
				     , a.gid
				     , a.uploader
				     , a.title
				     , a.description
				     , a.created_ts
				     , a.modified_ts
				     , a.hidden
				     , a.removed
				     , a.last_fetch_ts
				     , a.inserted_ts
				     , COALESCE(cnt.c, 0) as file_count
				     , rf.filename AS thumb -- TODO use correlated subquery for performance
				  FROM album a
				  JOIN ripper r ON r.ripper_id = a.ripper_id
				  JOIN map_album_tag mat ON mat.album_id = a.album_id AND mat.tag_id = ?
				  LEFT JOIN (
				          SELECT m.album_id, COUNT(*) c, MIN(m.remote_file_id) AS min_rf
				            FROM map_album_remote_file m
				            JOIN remote_file rf2 ON rf2.remote_file_id = m.remote_file_id AND rf2.fetched = 1
				           GROUP BY m.album_id
				            ) cnt ON a.album_id = cnt.album_id
				  LEFT JOIN remote_file rf ON rf.remote_file_id = cnt.min_rf
				 ORDER BY a.album_id
				 LIMIT ? OFFSET ?
			`, t.TagId, size, offset)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var a types.Album
				var f types.File
				if err := rows.Scan(
					&a.AlbumId,
					&a.RipperId,
					&a.RipperName,
					&a.RipperHost,
					&a.Gid,
					&a.Uploader,
					&a.Title,
					&a.Description,
					&a.CreatedTs,
					&a.ModifiedTs,
					&a.Hidden,
					&a.Removed,
					&a.LastFetchTs,
					&a.InsertedTs,
					&a.FileCount,
					&f.FileId,
				); err != nil {
					return err
				}
				a.Thumb = f
				albums = append(albums, a)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		for i := range albums {
			thumb := albums[i].Thumb
			if err := withSQL(ctx, func() error {
				return db.QueryRowContext(ctx, `
					SELECT rf.filename
					     , mt.name AS mime_type
					  FROM remote_file rf
					  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
					 WHERE rf.remote_file_id = ?
				`, thumb.FileId).Scan(&thumb.Filename, &thumb.MimeType)
			}); err != nil {
				return err
			}
			albums[i].Thumb = thumb
		}
		// Files for tag
		var files []types.File
		if err := withSQL(ctx, func() error {
			rows, e := db.QueryContext(ctx, `
				SELECT rf.remote_file_id
				     , r.name AS ripper_name
				     , r.host AS ripper_host
				     , rf.urlid
				     , rf.filename
				     , mt.name AS mime_type
				     , rf.title
				     , rf.description
				     , rf.uploaded_ts
				     , rf.uploader
				     , rf.hidden
				     , rf.removed
				     , rf.inserted_ts
				  FROM remote_file rf
				  JOIN ripper r ON r.ripper_id = rf.ripper_id
				  JOIN map_remote_file_tag m ON m.remote_file_id = rf.remote_file_id AND m.tag_id = ?
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 WHERE rf.fetched = 1
				 ORDER BY m.remote_file_id
			`, t.TagId)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var f types.File
				if err := rows.Scan(
					&f.FileId,
					&f.RipperName,
					&f.RipperHost,
					&f.Urlid,
					&f.Filename,
					&f.MimeType,
					&f.Title,
					&f.Description,
					&f.UploadedTs,
					&f.Uploader,
					&f.Hidden,
					&f.Removed,
					&f.InsertedTs,
				); err != nil {
					return err
				}
				files = append(files, f)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		// Populate href
		for i := range albums {
			albums[i].HrefPage = fmt.Sprintf("/gallery/%s/%s", albums[i].RipperHost, albums[i].Gid)
			albums[i].Thumb.HrefPage = fmt.Sprintf("/gallery/%s/%s/%d", albums[i].RipperHost, albums[i].Gid, albums[i].Thumb.FileId)
			if albums[i].Thumb.Filename.Valid {
				albums[i].Thumb.HrefMedia = fmt.Sprintf("/media/%s/%s/%s", albums[i].RipperHost, albums[i].Gid, albums[i].Thumb.Filename.String)
			}
		}
		for i := range files {
			if files[i].Filename.Valid {
				files[i].HrefMedia = fmt.Sprintf("/media/%s/%s", files[i].RipperHost, files[i].Filename.String)
			}
		}
		model := types.TagDetailPage{Tag: t, Albums: albums, Files: files, Page: page, PageSize: size, Total: total, HasPrev: page > 1, HasNext: offset+len(albums) < total, BasePage: types.BasePage{Perf: perf}}
		return render(ctx, w, "tag.gohtml", model)
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func handleTags(w http.ResponseWriter, r *http.Request) {
	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var imageTags []types.Tag
		if err := withSQL(ctx, func() error {
			rows, err := db.QueryContext(ctx, `
				SELECT t.tag_id
				     , t.name
				     , (
				    SELECT COUNT(*)
				      FROM map_remote_file_tag mrft
				      --JOIN remote_file rf ON rf.remote_file_id = mrft.remote_file_id
				     WHERE t.tag_id = mrft.tag_id -- AND rf.fetched = 1
				    -- filtering on fetched here is quite slow
				       ) AS cnt
				  FROM tag t
				 ORDER BY cnt DESC, t.name ASC
			`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var t types.Tag
				if err := rows.Scan(&t.TagId, &t.Name, &t.Count); err != nil {
					return err
				}
				imageTags = append(imageTags, t)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		var albumTags []types.Tag
		if err := withSQL(ctx, func() error {
			rows, err := db.QueryContext(ctx, `
				SELECT t.tag_id
				     , t.name
				     , COUNT(t.tag_id) AS cnt
				  FROM map_album_tag mat
				  JOIN tag t ON t.tag_id = mat.tag_id
				 GROUP BY mat.tag_id, t.name
				 ORDER BY cnt DESC, t.name ASC
			`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var t types.Tag
				if err := rows.Scan(&t.TagId, &t.Name, &t.Count); err != nil {
					return err
				}
				albumTags = append(albumTags, t)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		model := types.TagsPage{ImageTags: imageTags, AlbumTags: albumTags, BasePage: types.BasePage{Perf: perf}}
		return render(ctx, w, "tags.gohtml", model)
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func isClientJsOn(r *http.Request) bool {
	_, err := r.Cookie("js")
	if errors.Is(err, http.ErrNoCookie) {
		// js cookie is not present in the request
		return false
	} else if err != nil {
		// Unable to read cookies
		return false
	}
	// js cookie was present
	return true
}

// handleRandomGallery selects a random album and redirects to its gallery page.
func handleRandomGallery(w http.ResponseWriter, r *http.Request) {
	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var ripperHost, gid string
		if err := withSQL(ctx, func() error {
			return db.QueryRowContext(ctx, `
				SELECT r.host, a.gid
				  FROM album a
				  JOIN ripper r ON r.ripper_id = a.ripper_id
				 ORDER BY RANDOM()
				 LIMIT 1
			`).Scan(&ripperHost, &gid)
		}); err != nil {
			return err
		}
		http.Redirect(w, r, "/gallery/"+ripperHost+"/"+url.PathEscape(gid), http.StatusFound)
		return nil
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleRandomFile selects a random available file and redirects to its file page.
func handleRandomFile(w http.ResponseWriter, r *http.Request) {
	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var ripperHost string
		var fileId int64
		var gid sql.NullString
		if err := withSQL(ctx, func() error {
			// Correct randomness, but slow-ish (avg 200ms)
			//return db.QueryRowContext(ctx, `
			//	  WITH row_count AS (
			//	      SELECT COUNT(*) as cnt
			//	        FROM remote_file rf
			//	       WHERE rf.fetched = 1
			//	                    )
			//	SELECT r.host, rf.remote_file_id
			//	  FROM remote_file rf
			//	  JOIN ripper r ON r.ripper_id = rf.ripper_id
			//	 WHERE rf.fetched = 1
			//	 ORDER BY remote_file_id
			//	 LIMIT 1 OFFSET (ABS(RANDOM()) % (SELECT cnt FROM row_count))
			//`).Scan(&ripperHost, &fileId)

			// Not the best random distribution if rows are deleted, but fast (avg 1ms)
			return db.QueryRowContext(ctx, `
				SELECT r.host
				     , rf.remote_file_id
				     , (
				    SELECT a.gid
				      FROM album a
				      JOIN map_album_remote_file m ON a.album_id = m.album_id
				     WHERE m.remote_file_id = rf.remote_file_id
				     LIMIT 1
				       ) AS gid
				  FROM remote_file rf
				  JOIN ripper r ON r.ripper_id = rf.ripper_id
				 WHERE remote_file_id >= (ABS(RANDOM()) % (
				     SELECT MAX(remote_file_id)
				       FROM remote_file rf
				      WHERE rf.fetched = 1
				                                          ))
				   AND rf.fetched = 1
				 ORDER BY remote_file_id
				 LIMIT 1
			`).Scan(&ripperHost, &fileId, &gid)
		}); err != nil {
			return err
		}
		fileIdString := strconv.FormatInt(fileId, 10)
		if gid.Valid {
			http.Redirect(w, r, "/gallery/"+ripperHost+"/"+url.PathEscape(gid.String)+"/"+url.PathEscape(fileIdString), http.StatusFound)
		} else {
			http.Redirect(w, r, "/file/"+ripperHost+"/"+url.PathEscape(fileIdString), http.StatusFound)
		}
		return nil
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func cleanJoin(elem ...string) string {
	joined := filepath.Join(elem...)
	// prevent path traversal by resolving and ensuring it stays under mediaRoot
	absRoot, _ := filepath.Abs(mediaRoot)
	absJoined, _ := filepath.Abs(joined)
	if !strings.HasPrefix(absJoined, absRoot) {
		return absRoot
	}
	return absJoined
}

func handleMedia(w http.ResponseWriter, r *http.Request) {
	// path after /media/
	rest := strings.TrimPrefix(r.URL.Path, "/media/")
	rest = strings.TrimLeft(rest, "/")
	parts := strings.Split(rest, "/")
	var tryFiles []string
	// /media/{ripper_host}/{gid}/{filename}
	if len(parts) >= 3 {
		ripperHost := parts[0]
		gid := parts[1]
		name := parts[2]

		// prefer direct path under mediaRoot/ripperHost_gid/
		preferredPath := cleanJoin(mediaRoot, ripperHost+"_"+gid, name)
		tryFiles = append(tryFiles, preferredPath)

		// first fallback: ripme-mangled path
		mangledGid := filesystemSafe(gid)
		mangledName := sanitizedFilename(name)
		if mangledGid != gid || mangledName != name {
			mangledPath := cleanJoin(mediaRoot, ripperHost+"_"+mangledGid, mangledName)
			tryFiles = append(tryFiles, mangledPath)
		}

		// fallback to knownFilePaths by name
		if list, ok := knownFilePaths[name]; ok {
			for _, p := range list {
				tryFiles = append(tryFiles, cleanJoin(dfLogRoot, p))
			}
		}
	} else if len(parts) >= 2 { // fallback: /media/{ripper_host}/{filename}
		ripperHost := parts[0]
		name := parts[1]
		// prefer direct path under mediaRoot
		tryFiles = append(tryFiles, cleanJoin(mediaRoot, ripperHost, name))

		// first fallback: ripme-mangled path
		mangledName := sanitizedFilename(name)
		if mangledName != name {
			mangledPath := cleanJoin(mediaRoot, ripperHost, mangledName)
			tryFiles = append(tryFiles, mangledPath)
		}

		// fallback to knownFilePaths by name
		if list, ok := knownFilePaths[name]; ok {
			for _, p := range list {
				tryFiles = append(tryFiles, cleanJoin(dfLogRoot, p))
			}
		}
	} else if len(parts) == 1 && parts[0] != "" { // last resort: find by filename only
		name := parts[0]
		if list, ok := knownFilePaths[name]; ok {
			for _, p := range list {
				tryFiles = append(tryFiles, cleanJoin(dfLogRoot, p))
			}
		}
	}

	for _, fp := range tryFiles {
		if st, err := os.Stat(fp); err == nil && st.Mode().IsRegular() {
			// Compute ETag from size and modtime
			etag := fmt.Sprintf("\"%x-%x\"", st.ModTime().Unix(), st.Size())
			w.Header().Set("ETag", etag)
			w.Header().Set("Last-Modified", st.ModTime().UTC().Format(http.TimeFormat))
			// Set sensible cache headers for media files
			w.Header().Set("Cache-Control", "public, max-age=86400")
			if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			if ims := r.Header.Get("If-Modified-Since"); ims != "" {
				if t, err := time.Parse(http.TimeFormat, ims); err == nil {
					if !st.ModTime().After(t) {
						w.WriteHeader(http.StatusNotModified)
						return
					}
				}
			}
			// Use ServeContent to respect range requests
			f, err := os.Open(fp)
			if err != nil {
				break
			}
			defer f.Close()
			http.ServeContent(w, r, filepath.Base(fp), st.ModTime(), f)
			return
		}
	}
	renderError(r.Context(), w, &types.Perf{}, http.StatusNotFound, nil)
}

var filesystemSafeRe = regexp.MustCompile("[^a-zA-Z0-9-.,_ ]")

// from ripme Utils.filesystemSafe; used on gid
func filesystemSafe(path string) string {
	path = filesystemSafeRe.ReplaceAllString(path, "")
	path = strings.TrimSpace(path)
	if len(path) > 100 {
		path = path[:99] // obviously a bug, but copying the bug from ripme
	}
	return path
}

var sanitizedFilenameRe = regexp.MustCompile("[\\\\:*?\"<>|]")

// from ripme Utils.sanitizeSaveAs; used on filename
func sanitizedFilename(filename string) string {
	filename = sanitizedFilenameRe.ReplaceAllString(filename, "_")
	return filename
}

func render(ctx context.Context, w http.ResponseWriter, name string, data any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	renderMode := getRenderMode(ctx)
	if renderMode == RenderJSON {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.Header().Set("X-App-Version", Version)
		w.Header().Set("X-App-Commit", Commit)
		w.Header().Set("X-App-Build-Date", BuildDate)
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
	return tpl.ExecuteTemplate(w, name, data)
}

// Same as render, but fragments shouldn't clear the JS cookie
func renderFragment(ctx context.Context, w http.ResponseWriter, name string, data any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	renderMode := getRenderMode(ctx)
	if renderMode == RenderJSON {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.Header().Set("X-App-Version", Version)
		w.Header().Set("X-App-Commit", Commit)
		w.Header().Set("X-App-Build-Date", BuildDate)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Set short-lived cache for HTML pages to allow quick back/forward without staleness
	w.Header().Set("Cache-Control", "private, max-age=60")
	return tpl.ExecuteTemplate(w, name, data)
}

func renderError(ctx context.Context, w http.ResponseWriter, perf *types.Perf, status int, err error) {
	select {
	case <-ctx.Done():
		return
	default:
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		status = http.StatusNotFound
		err = nil
	}
	statusText := fmt.Sprintf("%d %s", status, http.StatusText(status))
	model := types.ErrorPage{StatusText: statusText, BasePage: types.BasePage{Perf: perf}}
	if err != nil {
		model.Message = err.Error()
	} else {
		model.Message = statusText
	}
	w.WriteHeader(status)
	renderMode := getRenderMode(ctx)
	if renderMode == RenderJSON {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-App-Version", Version)
		w.Header().Set("X-App-Commit", Commit)
		w.Header().Set("X-App-Build-Date", BuildDate)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(model)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tpl.ExecuteTemplate(w, "error.gohtml", model)
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		dur := time.Since(start)
		log.Printf("%s %s %v", r.Method, r.URL.Path, dur.Round(time.Millisecond))
	})
}
