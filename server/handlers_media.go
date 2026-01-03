//go:build !placeholders

package server

import (
	"context"
	"errors"
	"fmt"
	"golocalgal/types"
	"net/http"
	"strings"
)

func (app *App) handleMedia(w http.ResponseWriter, r *http.Request) {
	rCtx := r.Context()
	p, err := app.perfTracker(rCtx, func(ctx context.Context, perf *types.Perf) error {
		//model := map[string]any{"Perf": *perf}
		//return app.render(ctx, w, "about.gohtml", &model)

		var ripperHost string
		var name string
		var gid string
		var mangledName string

		// path after /media/
		rest := strings.TrimPrefix(r.URL.Path, "/media/")
		rest = strings.TrimLeft(rest, "/")
		parts := strings.Split(rest, "/")
		var tryFiles []string
		// /media/{ripper_host}/{gid}/{filename}
		if len(parts) >= 3 {
			ripperHost = parts[0]
			gid = parts[1]
			name = parts[2]

			// prefer direct path under mediaRoot/ripperHost_gid/
			preferredPath := app.cleanJoin(app.MediaRoot, ripperHost+"_"+gid, name)
			tryFiles = append(tryFiles, preferredPath)

			// first fallback: ripme-mangled path
			mangledGid := filesystemSafe(gid)
			mangledName = sanitizedFilename(name)
			if mangledGid != gid || mangledName != name {
				mangledPath := app.cleanJoin(app.MediaRoot, ripperHost+"_"+mangledGid, mangledName)
				tryFiles = append(tryFiles, mangledPath)
			}

			// fallback to knownFilePaths by name
			if list, ok := app.KnownFilePaths[name]; ok {
				for _, p := range list {
					tryFiles = append(tryFiles, app.cleanJoin(app.DfLogRoot, p))
				}
			}
		} else if len(parts) >= 2 { // fallback: /media/{ripper_host}/{filename}
			ripperHost = parts[0]
			name = parts[1]
			// prefer direct path under mediaRoot
			tryFiles = append(tryFiles, app.cleanJoin(app.MediaRoot, ripperHost, name))

			// first fallback: ripme-mangled path
			mangledName = sanitizedFilename(name)
			if mangledName != name {
				mangledPath := app.cleanJoin(app.MediaRoot, ripperHost, mangledName)
				tryFiles = append(tryFiles, mangledPath)
			}

			// fallback to knownFilePaths by name
			if list, ok := app.KnownFilePaths[name]; ok {
				for _, p := range list {
					tryFiles = append(tryFiles, app.cleanJoin(app.DfLogRoot, p))
				}
			}
		} else if len(parts) == 1 && parts[0] != "" { // last resort: find by filename only
			name = parts[0]
			if list, ok := app.KnownFilePaths[name]; ok {
				for _, p := range list {
					tryFiles = append(tryFiles, app.cleanJoin(app.DfLogRoot, p))
				}
			}
		}

		for _, fp := range tryFiles {
			sent := sendFile(fp, w, r)
			if sent {
				return nil
			}
		}
		tryFiles = nil

		// final fallback: check database for likely path
		if len(ripperHost) > 0 && len(name) > 0 && len(mangledName) > 0 {
			var oldestGid string
			err := app.withSQL(ctx, func() error {
				return app.Db.QueryRowContext(ctx, `
					SELECT a.gid
					  FROM remote_file rf
					  JOIN ripper r ON r.ripper_id = rf.ripper_id
					  JOIN map_album_remote_file marf ON marf.remote_file_id = rf.remote_file_id
					  JOIN album a ON a.album_id = marf.album_id
					 WHERE r.host = ?
					   AND rf.filename IN (?, ?)
					   AND a.fetch_count > 0
					 ORDER BY a.inserted_ts
					 LIMIT 1
				`, ripperHost, name, mangledName).Scan(&oldestGid)
			})
			if err == nil && oldestGid != gid {
				preferredPathOldestGid := app.cleanJoin(app.MediaRoot, ripperHost+"_"+oldestGid, name)
				tryFiles = append(tryFiles, preferredPathOldestGid)
				mangledOldestGid := filesystemSafe(oldestGid)
				if mangledOldestGid != oldestGid || mangledName != name {
					mangledPath := app.cleanJoin(app.MediaRoot, ripperHost+"_"+mangledOldestGid, mangledName)
					tryFiles = append(tryFiles, mangledPath)
				}
			}
		}

		for _, fp := range tryFiles {
			sent := sendFile(fp, w, r)
			if sent {
				return nil
			}
		}
		tryFiles = nil

		return fmt.Errorf("not found")
	})
	if err != nil {
		_ = p // TODO add performance response headers...
		ctxErr := rCtx.Err()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(ctxErr, context.Canceled) || errors.Is(ctxErr, context.DeadlineExceeded) {
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
}
