package server

import (
	"context"
	"golocalgal/types"
	"golocalgal/vars"
	"log"
	"net/url"
	"runtime"
	"strconv"
	"time"
)

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
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

type perfKey struct{}

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
	if vars.SlowSqlMs >= 0 && elapsed >= time.Duration(vars.SlowSqlMs)*time.Millisecond {
		if _, file, line, ok := runtime.Caller(1); ok {
			log.Printf("SLOW SQL at %s:%d: %v (>= %dms)", file, line, elapsed.Round(time.Millisecond), vars.SlowSqlMs)
		} else {
			log.Printf("SLOW SQL: %v (>= %dms)", elapsed.Round(time.Millisecond), vars.SlowSqlMs)
		}
	}
	return err
}
