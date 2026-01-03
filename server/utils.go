package server

import (
	"context"
	"golocalgal/types"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"time"
)

const DefaultPageSize = 30

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func getPageParams(w http.ResponseWriter, r *http.Request, url *url.URL) (page, size int) {
	defaultPageSize := DefaultPageSize
	defaultSize, err := r.Cookie("defaultPageSize")
	if err == nil {
		defaultPageSize = atoiDefault(defaultSize.Value, defaultPageSize)
	}

	page, size = parsePageParams(url, defaultPageSize)
	if defaultPageSize != size {
		http.SetCookie(w, &http.Cookie{
			Name:     "defaultPageSize",
			Value:    strconv.Itoa(size),
			Path:     "/",
			SameSite: http.SameSiteStrictMode,
		})
	}
	return page, size
}

func parsePageParams(url *url.URL, defSize int) (page, size int) {
	q := url.Query()
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

func getPageCount(itemCount int64, pageSize int64) int64 {
	if itemCount <= 0 {
		itemCount = 0
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	return (itemCount + pageSize - 1) / pageSize
}

type perfKey struct{}

// perfTracker helps measure SQL and request timings
// It attaches a Perf tracker to the provided parent context to preserve request cancellation/deadlines.
func (app *App) perfTracker(parent context.Context, next func(ctx context.Context, p *types.Perf) error) (types.Perf, error) {
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

func (app *App) withSQL(ctx context.Context, fn func(ctx context.Context) error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	perf, _ := ctx.Value(perfKey{}).(*types.Perf)
	start := time.Now()
	err := fn(ctx)
	elapsed := time.Since(start)
	if perf != nil {
		perf.SQLCount++
		perf.SQLTime += elapsed
	}
	// Slow query logging: threshold via SLOW_SQL_MS env (default 100ms)
	if app.SlowSqlMs >= 0 && elapsed >= time.Duration(app.SlowSqlMs)*time.Millisecond {
		if _, file, line, ok := runtime.Caller(1); ok {
			log.Printf("SLOW SQL at %s:%d: %v (>= %dms)", file, line, elapsed.Round(time.Millisecond), app.SlowSqlMs)
		} else {
			log.Printf("SLOW SQL: %v (>= %dms)", elapsed.Round(time.Millisecond), app.SlowSqlMs)
		}
	}
	return err
}
