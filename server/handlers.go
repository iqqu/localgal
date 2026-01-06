package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"golocalgal/types"
	"math/rand/v2"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mattn/go-sqlite3"
)

func (app *App) handle404(w http.ResponseWriter, r *http.Request) {
	app.renderError(r.Context(), w, &types.Perf{}, http.StatusNotFound, nil)
}

func (app *App) handleStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=86400")
	app.StaticFSHandler.ServeHTTP(w, r)
}

func (app *App) handleAbout(w http.ResponseWriter, r *http.Request) {
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		model := map[string]any{"Perf": *perf}
		app.render(ctx, w, "about.gohtml", &model)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func (app *App) handleStats(w http.ResponseWriter, r *http.Request) {
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var dbBytes int64
		var schemaVersion string
		var albumCount int
		var fileCount int
		var tagCount int

		err := app.withSQL(ctx, func(ctx context.Context) error {
			return app.Db.QueryRowContext(ctx, `
				SELECT (
				           SELECT page_size
				             FROM pragma_page_size()
				       ) * (
				           SELECT page_count
				             FROM pragma_page_count()
				           ) AS main_db_bytes
				     , COALESCE((
				                    SELECT version
				                      FROM flyway_schema_history
				                     WHERE success = 1
				                     ORDER BY installed_rank DESC
				                     LIMIT 1
				                ), 'unknown') AS schema_version
				     , (
				    SELECT COUNT(*)
				      FROM album
				       ) AS album_count
				     , (
				    SELECT COUNT(*)
				      FROM remote_file
				       ) AS file_count
				     , (
				    SELECT COUNT(*)
				      FROM tag
				       ) AS tag_count
			`).Scan(&dbBytes, &schemaVersion, &albumCount, &fileCount, &tagCount)
		})
		if err != nil {
			return err
		}

		model := types.StatsPage{
			DbBytes:       dbBytes,
			SchemaVersion: schemaVersion,
			GalleryCount:  albumCount,
			FileCount:     fileCount,
			TagCount:      tagCount,
			BasePage:      &types.BasePage{Perf: perf},
		}
		app.render(ctx, w, "stats.gohtml", &model)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func (app *App) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if !(r.URL.Path == "/" || r.URL.Path == "/api/galleries") {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusNotFound, nil)
		return
	}
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		page, size := getPageParams(w, r, r.URL)
		offset := (page - 1) * size
		sort := getSortGalleries(w, r)

		total, err := app.getTotalAlbumCount(ctx)
		if err != nil {
			return err
		}
		var list []types.Album
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			var orderByPage string
			var orderByAgg string
			switch sort {
			case SortFetched:
				orderByPage = "ORDER BY (a.last_fetch_ts IS NULL), a.last_fetch_ts DESC, a.inserted_ts DESC, a.album_id DESC"
				orderByAgg = "ORDER BY (p.last_fetch_ts IS NULL), p.last_fetch_ts DESC, p.inserted_ts DESC, p.album_id DESC"
			case SortUploaded:
				orderByPage = "ORDER BY (a.created_ts IS NULL), (a.modified_ts IS NULL), a.created_ts DESC, a.modified_ts DESC, a.album_id DESC"
				orderByAgg = "ORDER BY (p.created_ts IS NULL), (p.modified_ts IS NULL), p.created_ts DESC, p.modified_ts DESC, p.album_id DESC"
			case SortBytes:
				orderByPage = "ORDER BY a.sum_rf_bytes DESC, a.album_id DESC"
				orderByAgg = "ORDER BY p.sum_rf_bytes DESC, p.album_id DESC"
			case SortItems:
				orderByPage = "ORDER BY a.cnt_rf DESC, a.album_id DESC"
				orderByAgg = "ORDER BY p.cnt_rf DESC, p.album_id DESC"
			default:
				orderByPage = "ORDER BY (a.last_fetch_ts IS NULL), a.last_fetch_ts DESC, a.inserted_ts DESC, a.album_id DESC"
				orderByAgg = "ORDER BY (p.last_fetch_ts IS NULL), p.last_fetch_ts DESC, p.inserted_ts DESC, p.album_id DESC"
			}
			replacer := strings.NewReplacer("/*ORDER_BY_PAGE*/", orderByPage, "/*ORDER_BY_AGG*/", orderByAgg)
			//language=sqlite
			rows, err := app.Db.QueryContext(ctx, replacer.Replace(`
				  WITH page AS (
				      SELECT a.album_id
				           , a.ripper_id
				           , a.gid
				           , a.uploader
				           , a.title
				           , a.description
				           , a.created_ts
				           , a.modified_ts
				           , a.fetch_count
				           , a.hidden
				           , a.removed
				           , a.local_rating
				           , a.sum_rf_bytes
				           , a.cnt_rf
				           , a.last_fetch_ts
				           , a.inserted_ts
				        FROM album a
				       WHERE EXISTS(
				           SELECT 1
				             FROM map_album_remote_file marf
				             JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
				            WHERE marf.album_id = a.album_id
				              AND rf.fetched = 1
				              AND rf.ignored = 0
				                   )
				-- ORDER BY a.album_id
				/*ORDER_BY_PAGE*/
				       LIMIT ? OFFSET ?
				               )
				--      , agg AS (
				--       SELECT marf.album_id
				--            , COUNT(*) AS file_count
				--            , MAX(rf.remote_file_id) AS thumb_remote_file_id
				--            , SUM(rf.bytes) AS album_bytes
				--         FROM map_album_remote_file marf
				--         JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
				--        WHERE marf.album_id IN (
				--            SELECT album_id
				--              FROM page
				--                               )
				--          AND rf.fetched = 1
				--          AND rf.ignored = 0
				--        GROUP BY marf.album_id
				--                )
				SELECT p.album_id
				     , p.ripper_id
				     , r.name AS ripper_name
				     , r.host AS ripper_host
				     , p.gid
				     , p.uploader
				     , p.title
				     , p.description
				     , p.created_ts
				     , p.modified_ts
				     , p.fetch_count
				     , p.hidden
				     , p.removed
				     , p.local_rating
				     , p.sum_rf_bytes
				     , p.cnt_rf
				     , p.last_fetch_ts
				     , p.inserted_ts
				    --, COALESCE(agg.file_count, 0) AS file_count
				    --, COALESCE(agg.album_bytes, 0) AS album_bytes
				    --, agg.thumb_remote_file_id
				     , (
				    SELECT rf.remote_file_id
				      FROM map_album_remote_file marf
				      JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
				     WHERE marf.album_id = p.album_id
				       AND rf.fetched = 1
				       AND rf.ignored = 0
				     ORDER BY marf.remote_file_id DESC
				     LIMIT 1
				       ) AS thumb_remote_file_id
				  FROM page p
				  JOIN ripper r ON r.ripper_id = p.ripper_id
				  -- ORDER BY p.album_id
				  /*ORDER_BY_AGG*/
			`), size, offset)
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
					&a.FetchCount,
					&a.Hidden,
					&a.Removed,
					&a.LocalRating,
					&a.Bytes,
					&a.FileCount,
					&a.LastFetchTs,
					&a.InsertedTs,
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
			if err := app.withSQL(ctx, func(ctx context.Context) error {
				return app.Db.QueryRowContext(ctx, `
					SELECT rf.filename
					     , mt.name AS mime_type
					  FROM remote_file rf
					  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
					 WHERE rf.remote_file_id = ?
					   AND rf.fetched = 1
					   AND rf.ignored = 0
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
		// For speed, unfetched and ignored files are included in the total album count
		// Albums without files are not included in the list, but are included in the page size,
		// so to prevent the next page button from being shown when the last page has empty albums,
		// calculate HasNext based on the total page count instead of using the list size
		totalPageCount := getPageCount(int64(total), int64(size))
		model := types.BrowsePage{
			Albums:   list,
			Page:     page,
			PageSize: size,
			Total:    total,
			HasPrev:  page > 1,
			HasNext:  totalPageCount > int64(page),
			Sort:     sort,
			BasePage: &types.BasePage{Perf: perf},
		}
		app.render(ctx, w, "browse.gohtml", &model)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func (app *App) getTotalAlbumCount(ctx context.Context) (int, error) {
	var total int
	err := app.withSQL(ctx, func(ctx context.Context) error {
		return app.Db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			  FROM album a
			 WHERE a.cnt_rf > 0
		`).Scan(&total)
	})
	return total, err
}

// handleGallery handles /gallery/{ripper_host}/{gid}
func (app *App) handleGallery(w http.ResponseWriter, r *http.Request) {
	ripperHost := r.PathValue("ripper_host")
	gid := r.PathValue("gid")
	if ripperHost == "" || gid == "" {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /gallery/{ripper_host}/{gid}"))
		return
	}
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var a types.Album
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			return app.Db.QueryRowContext(ctx, `
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
				     , a.fetch_count
				     , a.hidden
				     , a.removed
				     , a.local_rating
				     , a.sum_rf_bytes
				     , a.cnt_rf
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
				&a.FetchCount,
				&a.Hidden,
				&a.Removed,
				&a.LocalRating,
				&a.Bytes,
				&a.FileCount,
				&a.LastFetchTs,
				&a.InsertedTs,
			)
		}); err != nil {
			return err
		}
		page, size := getPageParams(w, r, r.URL)
		offset := (page - 1) * size
		sort := getSortFiles(w, r)

		//var total int
		//var albumBytes int64
		//if err := app.withSQL(ctx, func(ctx context.Context) error {
		//	return app.Db.QueryRowContext(ctx, `
		//		SELECT COUNT(*)
		//		     , COALESCE(SUM(rf.bytes), 0)
		//		  FROM map_album_remote_file marf
		//		  JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
		//		 WHERE marf.album_id = ?
		//		   AND rf.fetched = 1
		//		   AND rf.ignored = 0
		//	`, a.AlbumId).Scan(&total, &albumBytes)
		//}); err != nil {
		//	return err
		//}
		var files []types.File
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			var orderBy string
			switch sort {
			// TODO: order by local_rating?
			case SortFetched:
				orderBy = "ORDER BY rf.inserted_ts DESC, rf.remote_file_id DESC"
			case SortBytes:
				orderBy = "ORDER BY (rf.bytes IS NULL), rf.bytes DESC, rf.remote_file_id DESC"
			case SortUploaded:
				orderBy = "ORDER BY (rf.uploaded_ts IS NULL), rf.uploaded_ts DESC, rf.remote_file_id DESC"
			default:
				orderBy = "ORDER BY rf.inserted_ts DESC, rf.remote_file_id DESC"
			}
			rows, err := app.Db.QueryContext(ctx, strings.Replace(`
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
				     , rf.bytes
				     , rf.local_rating
				     , rf.inserted_ts
				  FROM map_album_remote_file marf
				  JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
				  -- JOIN ripper r ON r.ripper_id = rf.ripper_id
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 WHERE marf.album_id = ?
				   AND rf.fetched = 1
				   AND rf.ignored = 0
				 -- ORDER BY marf.remote_file_id
				 /*ORDER_BY*/
				 LIMIT ? OFFSET ?
			`, "/*ORDER_BY*/", orderBy, 1), a.AlbumId, size, offset)
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
					&f.Bytes,
					&f.LocalRating,
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
		var albumTags []types.Tag
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			rows, e := app.Db.QueryContext(ctx, `
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

		// Populate href
		a.HrefPage = fmt.Sprintf("/gallery/%s/%s", a.RipperHost, a.Gid)
		for i := range files {
			files[i].HrefPage = fmt.Sprintf("/gallery/%s/%s/%d", a.RipperHost, a.Gid, files[i].FileId)
			if files[i].Filename.Valid {
				files[i].HrefMedia = fmt.Sprintf("/media/%s/%s/%s", a.RipperHost, a.Gid, files[i].Filename.String)
			}
		}

		total := a.FileCount
		albumBytes := a.Bytes

		asyncFileTags := isClientJsOn(r)
		if asyncFileTags {
			model := types.GalleryPage{
				Album:         a,
				Files:         files,
				Page:          page,
				PageSize:      size,
				Total:         total,
				HasPrev:       page > 1,
				HasNext:       offset+len(files) < total,
				AlbumTags:     albumTags,
				AsyncFileTags: true,
				AlbumBytes:    albumBytes,
				Sort:          sort,
				BasePage:      &types.BasePage{Perf: perf},
			}
			app.render(ctx, w, "gallery.gohtml", &model)
			return nil
		}

		fileTags, err := app.getGalleryFileTags(ctx, ripperHost, gid)
		if err != nil {
			return err
		}
		model := types.GalleryPage{
			Album:      a,
			Files:      files,
			Page:       page,
			PageSize:   size,
			Total:      total,
			HasPrev:    page > 1,
			HasNext:    offset+len(files) < total,
			AlbumTags:  albumTags,
			FileTags:   fileTags,
			AlbumBytes: albumBytes,
			Sort:       sort,
			BasePage:   &types.BasePage{Perf: perf},
		}
		app.render(ctx, w, "gallery.gohtml", &model)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleGalleryFileTagsFragment handles /gallery-file-tags/{ripper_host}/{gid}
func (app *App) handleGalleryFileTagsFragment(w http.ResponseWriter, r *http.Request) {
	ripperHost := r.PathValue("ripper_host")
	gid := r.PathValue("gid")
	if ripperHost == "" || gid == "" {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /gallery-file-tags/{ripper_host}/{gid}"))
		return
	}
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		// Fetch tags for album and distinct tags from its files
		var fileTags []types.Tag
		fileTags, err := app.getGalleryFileTags(ctx, ripperHost, gid)
		if err != nil {
			return err
		}
		model := types.GalleryPage{
			FileTags: fileTags,
			BasePage: &types.BasePage{Perf: perf},
		}
		app.renderFragment(ctx, w, "gallery_file_tags.gohtml", &model)
		return nil
	})
	if err != nil {
		err = fmt.Errorf("unable to load gallery's file tags: %w", err)
		app.renderErrorFragment(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func (app *App) getGalleryFileTags(ctx context.Context, ripperHost string, gid string) ([]types.Tag, error) {
	var fileTags []types.Tag
	if err := app.withSQL(ctx, func(ctx context.Context) error {
		rows, e := app.Db.QueryContext(ctx, `
				SELECT t.name, COUNT(*) as count
				  FROM tag t
				  JOIN map_remote_file_tag mrft ON mrft.tag_id = t.tag_id
				  JOIN map_album_remote_file marf ON marf.remote_file_id = mrft.remote_file_id
				  JOIN album a ON a.album_id = marf.album_id
				  JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
				  JOIN ripper r ON r.ripper_id = a.ripper_id
				 WHERE a.gid = ?
				   AND r.host = ?
				   AND rf.fetched = 1
				   AND rf.ignored = 0
				 GROUP BY t.tag_id
				 ORDER BY count DESC
				 LIMIT 100 -- some albums might have a million tags...
			`, gid, ripperHost)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var t types.Tag
			if err := rows.Scan(&t.Name, &t.Count); err != nil {
				return err
			}
			fileTags = append(fileTags, t)
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}
	return fileTags, nil
}

// handleGallery handles /gallery/{ripper_host}/{gid}/{file_id}
func (app *App) handleGalleryFile(w http.ResponseWriter, r *http.Request) {
	ripperHost := r.PathValue("ripper_host")
	gid := r.PathValue("gid")
	fileIdString := r.PathValue("file_id")
	if ripperHost == "" || gid == "" || fileIdString == "" {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /gallery/{ripper_host}/{gid}/{file_id}"))
		return
	}
	fileId, err := strconv.ParseInt(fileIdString, 10, 64)
	if err != nil || fileId <= 0 {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("file_id must be a positive integer"))
		return
	}

	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var a types.Album
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			return app.Db.QueryRowContext(ctx, `
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
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			return app.Db.QueryRowContext(ctx, `
				SELECT rf.remote_file_id
				     , r.name AS ripper_name
				     , r.host AS ripper_host
				     , rf.urlid
				     , rf.filename
				     , mt.name AS mime_type
				     , rf.bytes
				     , rf.title
				     , rf.description
				     , rf.uploaded_ts
				     , rf.uploader
				     , rf.hidden
				     , rf.removed
				     , rf.local_rating
				     , rf.inserted_ts
				  FROM remote_file rf
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				  JOIN map_album_remote_file m ON m.remote_file_id = rf.remote_file_id
				  JOIN ripper r on rf.ripper_id = r.ripper_id
				 WHERE m.album_id = ?
				   AND rf.remote_file_id = ?
				   AND rf.fetched = 1
				   AND rf.ignored = 0
			`, a.AlbumId, fileId).Scan(
				&f.FileId,
				&f.RipperName, // TODO use value from Album
				&f.RipperHost, // TODO use value from Album
				&f.Urlid,
				&f.Filename,
				&f.MimeType,
				&f.Bytes,
				&f.Title,
				&f.Description,
				&f.UploadedTs,
				&f.Uploader,
				&f.Hidden,
				&f.Removed,
				&f.LocalRating,
				&f.InsertedTs,
			)
		}); err != nil {
			return err
		}

		sort := getSortFiles(w, r)
		// Prev/Next within this album by remote_file_id
		var prev []types.File
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			var prevOrderKey1 string
			var prevOrderKey2 string
			switch sort {
			case SortFetched:
				prevOrderKey1 = `
				         AND (
				           rf.inserted_ts > t.inserted_ts
				               OR (rf.inserted_ts = t.inserted_ts
				               AND rf.remote_file_id > t.remote_file_id)
				           )
				       ORDER BY rf.inserted_ts ASC, rf.remote_file_id ASC
				`
				prevOrderKey2 = "ORDER BY rf.inserted_ts DESC, rf.remote_file_id DESC"
			case SortUploaded:
				prevOrderKey1 = `
				         AND (
				           (t.uploaded_ts IS NOT NULL
				               AND (rf.uploaded_ts > t.uploaded_ts
				                   OR (rf.uploaded_ts = t.uploaded_ts
				                       AND rf.remote_file_id > t.remote_file_id)))
				               OR
				           (t.uploaded_ts IS NULL
				               AND (rf.uploaded_ts IS NOT NULL
				                   OR rf.remote_file_id > t.remote_file_id))
				           )
				       ORDER BY (rf.uploaded_ts IS NULL) DESC, rf.uploaded_ts ASC, rf.remote_file_id ASC
				`
				prevOrderKey2 = "ORDER BY (rf.uploaded_ts IS NULL) ASC, rf.uploaded_ts DESC, rf.remote_file_id DESC"
			case SortBytes:
				prevOrderKey1 = `
				         AND (
				           (t.bytes IS NOT NULL
				               AND (rf.bytes > t.bytes
				                   OR (rf.bytes = t.bytes
				                       AND rf.remote_file_id > t.remote_file_id)))
				               OR
				           (t.bytes IS NULL
				               AND (rf.bytes IS NOT NULL
				                   OR rf.remote_file_id > t.remote_file_id))
				           )
				       ORDER BY (rf.bytes IS NULL) DESC, rf.bytes ASC, rf.remote_file_id ASC
				`
				prevOrderKey2 = "ORDER BY (rf.bytes IS NULL) ASC, rf.bytes DESC, rf.remote_file_id DESC"
			default:
				prevOrderKey1 = `
				         AND (
				           rf.inserted_ts > t.inserted_ts
				               OR (rf.inserted_ts = t.inserted_ts
				               AND rf.remote_file_id > t.remote_file_id)
				           )
				       ORDER BY rf.inserted_ts ASC, rf.remote_file_id ASC
				`
				prevOrderKey2 = "ORDER BY rf.inserted_ts DESC, rf.remote_file_id DESC"
			}

			replacer := strings.NewReplacer(
				"/*PREV_ORDER_KEY_INNER*/",
				prevOrderKey1,
				"/*PREV_ORDER_KEY_OUTER*/",
				prevOrderKey2,
			)
			//language=sqlite
			rows, e := app.Db.QueryContext(ctx, replacer.Replace(`
				-- Step 1: On the mapping table, seek previous remote_file_id values (< current) with ORDER BY DESC LIMIT 3 using PK (album_id, remote_file_id).
				-- Step 2: Join the small set to remote_file and filter to available rows.
				-- Step 3: Re-order ascending for display as chronological prev list.
				  WITH reversed AS (
				        WITH target AS (
				            SELECT t.remote_file_id
				                 , t.inserted_ts
				                 , t.uploaded_ts
				                 , t.bytes
				              FROM remote_file t
				             WHERE t.remote_file_id = ?
				                       )
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
				           , rf.bytes
				           , rf.local_rating
				           , rf.inserted_ts
				        FROM map_album_remote_file marf
				        JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
				        LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				        JOIN target t
				       WHERE marf.album_id = ?
				         AND rf.fetched = 1
				         AND rf.ignored = 0
				         /*PREV_ORDER_KEY_INNER*/
				       LIMIT 3
				                   )
				SELECT *
				  FROM reversed rf
				  /*PREV_ORDER_KEY_OUTER*/
			`), f.FileId, a.AlbumId)
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
					&pf.Bytes,
					&pf.LocalRating,
					&pf.InsertedTs,
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
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			var nextOrderKey string
			switch sort {
			case SortFetched:
				nextOrderKey = `
				   AND (
				     rf.inserted_ts < t.inserted_ts
				         OR (rf.inserted_ts = t.inserted_ts
				         AND rf.remote_file_id < t.remote_file_id)
				     )
				 ORDER BY rf.inserted_ts DESC, rf.remote_file_id DESC
				`
			case SortUploaded:
				nextOrderKey = `
				   AND (
				     (t.uploaded_ts IS NOT NULL
				         AND (rf.uploaded_ts IS NULL
				             OR rf.uploaded_ts < t.uploaded_ts
				             OR (rf.uploaded_ts = t.uploaded_ts
				                 AND rf.remote_file_id < t.remote_file_id)))
				         OR
				     (t.uploaded_ts IS NULL AND rf.uploaded_ts IS NULL
				         AND rf.remote_file_id < t.remote_file_id)
				     )
				 ORDER BY (rf.uploaded_ts IS NULL) ASC, rf.uploaded_ts DESC, rf.remote_file_id DESC
				`
			case SortBytes:
				nextOrderKey = `
				   AND (
				     (t.bytes IS NOT NULL
				         AND (rf.bytes IS NULL
				             OR rf.bytes < t.bytes
				             OR (rf.bytes = t.bytes
				                 AND rf.remote_file_id < t.remote_file_id)))
				         OR
				     (t.bytes IS NULL AND rf.bytes IS NULL
				         AND rf.remote_file_id < t.remote_file_id)
				     )
				 ORDER BY (rf.bytes IS NULL) ASC, rf.bytes DESC, rf.remote_file_id DESC
				`
			default:
				nextOrderKey = `
				   AND (
				     rf.inserted_ts < t.inserted_ts
				         OR (rf.inserted_ts = t.inserted_ts
				         AND rf.remote_file_id < t.remote_file_id)
				     )
				 ORDER BY rf.inserted_ts DESC, rf.remote_file_id DESC
				`
			}

			replacer := strings.NewReplacer("/*NEXT_ORDER_KEY*/", nextOrderKey)
			//language=sqlite
			rows, e := app.Db.QueryContext(ctx, replacer.Replace(`
				  WITH target AS (
				      SELECT t.remote_file_id
				           , t.inserted_ts
				           , t.uploaded_ts
				           , t.bytes
				        FROM remote_file t
				       WHERE t.remote_file_id = ?
				                 )
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
				     , rf.bytes
				     , rf.local_rating
				     , rf.inserted_ts
				  FROM map_album_remote_file marf
				  JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				  JOIN target t
				 WHERE marf.album_id = ?
				   AND rf.fetched = 1
				   AND rf.ignored = 0
				   /*NEXT_ORDER_KEY*/
				 LIMIT 3
			`), f.FileId, a.AlbumId)
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
					&nf.Bytes,
					&nf.LocalRating,
					&nf.InsertedTs,
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
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			rows, e := app.Db.QueryContext(ctx, `
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

		autoplay := isClientAutoplayOn(r)
		asyncAlbums := isClientJsOn(r)
		if asyncAlbums {
			model := types.FilePage{
				File:         f,
				Prev:         prev,
				Next:         next,
				FileTags:     fileTags,
				AsyncAlbums:  true,
				CurrentAlbum: a,
				ShowPrevNext: true,
				Autoplay:     autoplay,
				BasePage:     &types.BasePage{Perf: perf},
			}
			app.render(ctx, w, "file.gohtml", &model)
			return nil
		}
		// Albums containing this file
		albums, err := app.getRelatedAlbums(ctx, ripperHost, fileId)
		if err != nil {
			return err
		}
		model := types.FilePage{
			File:         f,
			Prev:         prev,
			Next:         next,
			FileTags:     fileTags,
			Albums:       albums,
			CurrentAlbum: a,
			ShowPrevNext: true,
			Autoplay:     autoplay,
			BasePage:     &types.BasePage{Perf: perf},
		}
		app.render(ctx, w, "file.gohtml", &model)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleFileStandalone handles /file/{ripper_host}/{file_id}/
func (app *App) handleFileStandalone(w http.ResponseWriter, r *http.Request) {
	ripperHost := r.PathValue("ripper_host")
	fileIdString := r.PathValue("file_id")
	if ripperHost == "" || fileIdString == "" {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /file/{ripper_host}/{file_id}"))
		return
	}
	// TODO enable visiting a page by File urlid (need to handle related galleries fragment too)
	fileId, err := strconv.ParseInt(fileIdString, 10, 64)
	if err != nil || fileId <= 0 {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("invalid file id, must be a positive integer"))
		return
	}

	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var f types.File
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			f.RipperHost = ripperHost
			return app.Db.QueryRowContext(ctx, `
				SELECT rf.remote_file_id
				     , rf.urlid
				     , rf.filename
				     , mt.name AS mime_type
				     , rf.bytes
				     , rf.title
				     , rf.description
				     , rf.uploaded_ts
				     , rf.uploader
				     , rf.hidden
				     , rf.removed
				     , rf.local_rating
				     , rf.inserted_ts
				  FROM remote_file rf
				  JOIN ripper r ON r.ripper_id = rf.ripper_id
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 WHERE r.host = ?
				   AND rf.remote_file_id = ?
				   AND rf.fetched = 1
				   AND rf.ignored = 0
			`, ripperHost, fileId).Scan(
				&f.FileId,
				&f.Urlid,
				&f.Filename,
				&f.MimeType,
				&f.Bytes,
				&f.Title,
				&f.Description,
				&f.UploadedTs,
				&f.Uploader,
				&f.Hidden,
				&f.Removed,
				&f.LocalRating,
				&f.InsertedTs,
			)
		}); err != nil {
			return err
		}

		var fileTags []types.Tag
		// Standalone file view: no Prev/Next
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			rows, e := app.Db.QueryContext(ctx, `
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
			albums, err = app.getRelatedAlbums(ctx, ripperHost, f.FileId)
			if err != nil {
				return err
			}
		}

		// Populate href
		if f.Filename.Valid {
			f.HrefMedia = fmt.Sprintf("/media/%s/%s", ripperHost, f.Filename.String)
		}

		// Regular file page
		model := types.FilePage{File: f, FileTags: fileTags, AsyncAlbums: asyncAlbums, Albums: albums, ShowPrevNext: false, BasePage: &types.BasePage{Perf: perf}}
		app.render(ctx, w, "file.gohtml", &model)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleFileStandalone handles /file/{ripper_host}/{file_id}/galleries/
func (app *App) handleFileGalleryFragment(w http.ResponseWriter, r *http.Request) {
	// ripperHost is not necessary for now, but want to keep to make replacing file_id with urlid easy in the future
	ripperHost := r.PathValue("ripper_host")
	fileIdString := r.PathValue("file_id")
	if ripperHost == "" || fileIdString == "" {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /file/{ripper_host}/{file_id}/galleries"))
		return
	}
	fileId, err := strconv.ParseInt(fileIdString, 10, 64)
	if err != nil || fileId <= 0 {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("invalid file id, must be a positive integer"))
		return
	}

	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		f := types.File{FileId: fileId}

		var albums []types.Album
		albums, err = app.getRelatedAlbums(ctx, ripperHost, fileId)
		if err != nil {
			return err
		}

		model := types.FilePage{File: f, Albums: albums, BasePage: &types.BasePage{Perf: perf}}
		app.renderFragment(ctx, w, "file_galleries.gohtml", &model)
		return nil
	})
	if err != nil {
		err = fmt.Errorf("unable to load file's related galleries: %w", err)
		app.renderErrorFragment(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func (app *App) getRelatedAlbums(ctx context.Context, ripperHost string, fileId int64) ([]types.Album, error) {
	var albums []types.Album
	if err := app.withSQL(ctx, func(ctx context.Context) error {
		rows, e := app.Db.QueryContext(ctx, `
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
			     , a.fetch_count
			     , a.hidden
			     , a.removed
			     , a.last_fetch_ts
			     , a.inserted_ts
			     , a.cnt_rf
			     , (
			    SELECT rf.remote_file_id
			      FROM map_album_remote_file marf
			      JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
			     WHERE marf.album_id = a.album_id
			       AND rf.fetched = 1
			       AND rf.ignored = 0
			-- ORDER BY rf.remote_file_id ASC
			     LIMIT 1
			       ) AS thumb
			  FROM album a
			  JOIN ripper r ON r.ripper_id = a.ripper_id
			  JOIN map_album_remote_file marf ON marf.album_id = a.album_id
			 WHERE marf.remote_file_id = ?
			   AND r.host = ?
			 ORDER BY a.album_id
		`, fileId, ripperHost)
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
				&a2.FetchCount,
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
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			return app.Db.QueryRowContext(ctx, `
				SELECT rf.filename
				     , mt.name AS mime_type
				  FROM remote_file rf
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 WHERE rf.remote_file_id = ?
				   AND rf.fetched = 1
				   AND rf.ignored = 0
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

func (app *App) handleTagDetail(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag_name")
	tag, err := url.QueryUnescape(tag)
	if err != nil {
		app.renderError(r.Context(), w, &types.Perf{}, 500, err)
		return
	}
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var t types.Tag
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			return app.Db.QueryRowContext(ctx, `
				SELECT tag_id, name
				  FROM tag
				 WHERE name = ?
			`, tag).Scan(&t.TagId, &t.Name)
		}); err != nil {
			return err
		}
		// Albums for tag (with pagination)
		page, size := getPageParams(w, r, r.URL)
		offset := (page - 1) * size
		var total int
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			return app.Db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				  FROM album a
				  JOIN map_album_tag mat ON mat.album_id = a.album_id AND mat.tag_id = ?
			`, t.TagId).Scan(&total)
		}); err != nil {
			return err
		}
		var albums []types.Album
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			rows, e := app.Db.QueryContext(ctx, `
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
				     , a.fetch_count
				     , a.hidden
				     , a.removed
				     , a.last_fetch_ts
				     , a.inserted_ts
				     , COALESCE(cnt.c, 0) as file_count
				     , rf.remote_file_id AS thumb
				  FROM album a
				  JOIN ripper r ON r.ripper_id = a.ripper_id
				  JOIN map_album_tag mat ON mat.album_id = a.album_id AND mat.tag_id = ?
				  LEFT JOIN (
				          SELECT m.album_id, COUNT(*) c, m.remote_file_id AS min_rf
				            FROM map_album_remote_file m
				            JOIN remote_file rf2 ON rf2.remote_file_id = m.remote_file_id
				           WHERE rf2.fetched = 1
				             AND rf2.ignored = 0
				           GROUP BY m.album_id
				            ) cnt ON a.album_id = cnt.album_id
				  LEFT JOIN remote_file rf ON rf.remote_file_id = cnt.min_rf
				 WHERE rf.fetched = 1
				   AND rf.ignored = 0
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
					&a.FetchCount,
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
			if err := app.withSQL(ctx, func(ctx context.Context) error {
				return app.Db.QueryRowContext(ctx, `
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
		// TODO: Add albums containing files for tag

		// Files for tag
		var files []types.File
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			rows, e := app.Db.QueryContext(ctx, `
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
				  JOIN map_remote_file_tag m ON m.remote_file_id = rf.remote_file_id
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 WHERE m.tag_id = ?
				   AND rf.fetched = 1
				   AND rf.ignored = 0
				 ORDER BY m.remote_file_id
				 LIMIT 100 -- TODO paginate files too
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
		model := types.TagDetailPage{Tag: t, Albums: albums, Files: files, Page: page, PageSize: size, Total: total, HasPrev: page > 1, HasNext: offset+len(albums) < total, BasePage: &types.BasePage{Perf: perf}}
		app.render(ctx, w, "tag.gohtml", &model)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func (app *App) handleTags(w http.ResponseWriter, r *http.Request) {
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var imageTags []types.Tag
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			rows, err := app.Db.QueryContext(ctx, `
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
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			rows, err := app.Db.QueryContext(ctx, `
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
		model := types.TagsPage{ImageTags: imageTags, AlbumTags: albumTags, BasePage: &types.BasePage{Perf: perf}}
		app.render(ctx, w, "tags.gohtml", &model)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleSearch handles /search
func (app *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	searchQuery := q.Get("q")
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		size := 10
		offset := 0
		if len(searchQuery) == 0 {
			model := types.SearchPage{
				BasePage: &types.BasePage{Perf: perf},
			}
			app.render(ctx, w, "search_noquery.gohtml", &model)
			return nil
		}

		var albumIdMatches []types.Album
		{ // Just a block for code folding
			if err := app.withSQL(ctx, func(ctx context.Context) error {
				rows, err := app.Db.QueryContext(ctx, `
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
					     , a.fetch_count
					     , a.hidden
					     , a.removed
					     , a.local_rating
					     , a.sum_rf_bytes
					     , a.cnt_rf
					     , a.last_fetch_ts
					     , a.inserted_ts
					     , (
					    SELECT rf.remote_file_id
					      FROM map_album_remote_file marf
					      JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
					     WHERE marf.album_id = a.album_id
					       AND rf.fetched = 1
					       AND rf.ignored = 0
					     ORDER BY marf.remote_file_id DESC
					     LIMIT 1
					       ) AS thumb_remote_file_id
					  FROM album a
					  JOIN ripper r ON r.ripper_id = a.ripper_id
					 WHERE a.gid COLLATE NOCASE = ?
					 ORDER BY a.album_id DESC
				`, searchQuery)
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
						&a.FetchCount,
						&a.Hidden,
						&a.Removed,
						&a.LocalRating,
						&a.Bytes,
						&a.FileCount,
						&a.LastFetchTs,
						&a.InsertedTs,
						&thumbFileId,
					); err != nil {
						return err
					}
					// If an album has no fetched files, thumb_remote_file_id will be null
					if thumbFileId.Valid {
						f.FileId = thumbFileId.Int64
						a.Thumb = f
						albumIdMatches = append(albumIdMatches, a)
					}
				}
				return rows.Err()
			}); err != nil {
				return err
			}
			for i := range albumIdMatches {
				thumb := albumIdMatches[i].Thumb
				if err := app.withSQL(ctx, func(ctx context.Context) error {
					return app.Db.QueryRowContext(ctx, `
					SELECT rf.filename
					     , mt.name AS mime_type
					  FROM remote_file rf
					  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
					 WHERE rf.remote_file_id = ?
					   AND rf.fetched = 1
					   AND rf.ignored = 0
				`, thumb.FileId).Scan(&thumb.Filename, &thumb.MimeType)
				}); err != nil {
					return err
				}
				albumIdMatches[i].Thumb = thumb
			}
			// Populate href
			for i := range albumIdMatches {
				albumIdMatches[i].HrefPage = fmt.Sprintf("/gallery/%s/%s", albumIdMatches[i].RipperHost, albumIdMatches[i].Gid)
				albumIdMatches[i].Thumb.HrefPage = fmt.Sprintf("/media/%s/%s/%d", albumIdMatches[i].RipperHost, albumIdMatches[i].Gid, albumIdMatches[i].Thumb.FileId)
				if albumIdMatches[i].Thumb.Filename.Valid {
					albumIdMatches[i].Thumb.HrefMedia = fmt.Sprintf("/media/%s/%s/%s", albumIdMatches[i].RipperHost, albumIdMatches[i].Gid, albumIdMatches[i].Thumb.Filename.String)
				}
			}
		}

		var fileIdMatches []types.File
		{
			if err := app.withSQL(ctx, func(ctx context.Context) error {
				rows, err := app.Db.QueryContext(ctx, `
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
					     , rf.bytes
					     , rf.local_rating
					     , rf.inserted_ts
					  FROM remote_file rf
					  JOIN ripper r ON r.ripper_id = rf.ripper_id
					  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
					 WHERE rf.urlid COLLATE NOCASE = ?
					   AND rf.fetched = 1
					   AND rf.ignored = 0
					 ORDER BY rf.remote_file_id DESC
				`, searchQuery)
				if err != nil {
					return err
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
						&f.Bytes,
						&f.LocalRating,
						&f.InsertedTs,
					); err != nil {
						return err
					}
					f.HrefPage = fmt.Sprintf("/file/%s/%d", f.RipperHost, f.FileId)
					f.HrefMedia = fmt.Sprintf("/media/%s/%s", f.RipperHost, f.Filename.String)
					fileIdMatches = append(fileIdMatches, f)
				}
				return rows.Err()
			}); err != nil {
				return err
			}
		}

		var userIdMatches []types.User
		{
			if err := app.withSQL(ctx, func(ctx context.Context) error {
				rows, err := app.Db.QueryContext(ctx, `
				SELECT DISTINCT both.uploader, r.host
				  FROM (
				      SELECT rf.uploader, rf.ripper_id
				        FROM remote_file rf
				       WHERE rf.uploader IS NOT NULL
				         AND rf.fetched = 1
				         AND rf.ignored = 0
				       UNION ALL
				      SELECT a.uploader, a.ripper_id
				        FROM album a
				       WHERE a.uploader IS NOT NULL
				         AND a.cnt_rf > 0
				       ) both
				  JOIN ripper r ON r.ripper_id = both.ripper_id
				 WHERE uploader COLLATE NOCASE = ?;
				`, searchQuery)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var u types.User
					if err := rows.Scan(&u.UserName, &u.RipperHost); err != nil {
						return err
					}
					userIdMatches = append(userIdMatches, u)
				}
				return rows.Err()
			}); err != nil {
				return err
			}
		}

		// 1: Search albums
		var albumsTotal int
		albumsTotal, err := app.getSearchAlbumHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}

		albums, err := app.getSearchAlbumsPage(ctx, searchQuery, size, offset, SortRank)
		if err != nil {
			return err
		}

		// 2: Search files
		var filesTotal int
		filesTotal, err = app.getSearchFileHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}

		files, err := app.getSearchFilesPage(ctx, searchQuery, size, offset, SortRank)
		if err != nil {
			return err
		}

		// 3: Search tags
		var tagsTotal int
		tagsTotal, err = app.getSearchTagHits(ctx, searchQuery)
		if err != nil {
			return err
		}

		tags, err := app.getSearchTagsPage(ctx, searchQuery, 100)
		if err != nil {
			return err
		}

		model := types.SearchPage{
			Query:          searchQuery,
			AlbumIdMatches: albumIdMatches,
			FileIdMatches:  fileIdMatches,
			UserIdMatches:  userIdMatches,
			Albums:         albums,
			AlbumsTotal:    albumsTotal,
			Files:          files,
			FilesTotal:     filesTotal,
			Tags:           tags,
			TagsTotal:      tagsTotal,
			Sort:           SortRank,
			BasePage:       &types.BasePage{Perf: perf},
		}
		app.render(ctx, w, "search.gohtml", &model)
		return nil
	})
	var se sqlite3.Error
	if errors.As(err, &se) {
		model := types.SearchErrorPage{
			Query:    searchQuery,
			Message:  err.Error(),
			BasePage: &types.BasePage{Perf: &p},
		}
		app.render(r.Context(), w, "search_error.gohtml", &model)
		return
	}
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleSearchGalleries handles /search/galleries
func (app *App) handleSearchGalleries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	searchQuery := q.Get("q")
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		if len(searchQuery) == 0 {
			model := types.SearchPage{
				BasePage: &types.BasePage{Perf: perf},
			}
			app.render(ctx, w, "search_noquery.gohtml", &model)
			return nil
		}
		page, size := getPageParams(w, r, r.URL)
		offset := (page - 1) * size

		var albumsTotal int
		albumsTotal, err := app.getSearchAlbumHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}
		var filesTotal int
		filesTotal, err = app.getSearchFileHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}
		var tagsTotal int
		tagsTotal, err = app.getSearchTagHits(ctx, searchQuery)
		if err != nil {
			return err
		}

		order := getSortSearchGalleries(w, r)
		albums, err := app.getSearchAlbumsPage(ctx, searchQuery, size, offset, order)
		if err != nil {
			return err
		}

		model := types.SearchPage{
			Query:       searchQuery,
			Albums:      albums,
			AlbumsTotal: albumsTotal,
			FilesTotal:  filesTotal,
			TagsTotal:   tagsTotal,
			HasPrev:     page > 1,
			HasNext:     offset+len(albums) < albumsTotal,
			Page:        page,
			PageSize:    size,
			Sort:        order,
			BasePage:    &types.BasePage{Perf: perf},
		}
		app.render(ctx, w, "search_galleries.gohtml", &model)
		return nil
	})
	var se sqlite3.Error
	if errors.As(err, &se) {
		model := types.SearchErrorPage{
			Query:    searchQuery,
			Message:  err.Error(),
			BasePage: &types.BasePage{Perf: &p},
		}
		app.render(r.Context(), w, "search_error.gohtml", &model)
		return
	}
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleSearchFiles handles /search/files
func (app *App) handleSearchFiles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	searchQuery := q.Get("q")
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		if len(searchQuery) == 0 {
			model := types.SearchPage{
				BasePage: &types.BasePage{Perf: perf},
			}
			app.render(ctx, w, "search_noquery.gohtml", &model)
			return nil
		}
		page, size := getPageParams(w, r, r.URL)
		offset := (page - 1) * size

		var albumsTotal int
		albumsTotal, err := app.getSearchAlbumHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}
		var filesTotal int
		filesTotal, err = app.getSearchFileHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}
		var tagsTotal int
		tagsTotal, err = app.getSearchTagHits(ctx, searchQuery)
		if err != nil {
			return err
		}

		order := getSortSearchFiles(w, r)
		files, err := app.getSearchFilesPage(ctx, searchQuery, size, offset, order)
		if err != nil {
			return err
		}

		model := types.SearchPage{
			Query:       searchQuery,
			Files:       files,
			AlbumsTotal: albumsTotal,
			FilesTotal:  filesTotal,
			TagsTotal:   tagsTotal,
			HasPrev:     page > 1,
			HasNext:     offset+len(files) < filesTotal,
			Page:        page,
			PageSize:    size,
			Sort:        order,
			BasePage:    &types.BasePage{Perf: perf},
		}
		app.render(ctx, w, "search_files.gohtml", &model)
		return nil
	})
	var se sqlite3.Error
	if errors.As(err, &se) {
		model := types.SearchErrorPage{
			Query:    searchQuery,
			Message:  err.Error(),
			BasePage: &types.BasePage{Perf: &p},
		}
		app.render(r.Context(), w, "search_error.gohtml", &model)
		return
	}
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleSearchTags handles /search/tags
func (app *App) handleSearchTags(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	searchQuery := q.Get("q")
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		if len(searchQuery) == 0 {
			model := types.SearchPage{
				BasePage: &types.BasePage{Perf: perf},
			}
			app.render(ctx, w, "search_noquery.gohtml", &model)
			return nil
		}

		var albumsTotal int
		albumsTotal, err := app.getSearchAlbumHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}
		var filesTotal int
		filesTotal, err = app.getSearchFileHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}
		var tagsTotal int
		tagsTotal, err = app.getSearchTagHits(ctx, searchQuery)
		if err != nil {
			return err
		}

		tags, err := app.getSearchTagsPage(ctx, searchQuery, -1)
		if err != nil {
			return err
		}

		model := types.SearchPage{
			Query:       searchQuery,
			Tags:        tags,
			AlbumsTotal: albumsTotal,
			FilesTotal:  filesTotal,
			TagsTotal:   tagsTotal,
			BasePage:    &types.BasePage{Perf: perf},
		}
		app.render(ctx, w, "search_tags.gohtml", &model)
		return nil
	})
	var se sqlite3.Error
	if errors.As(err, &se) {
		model := types.SearchErrorPage{
			Query:    searchQuery,
			Message:  err.Error(),
			BasePage: &types.BasePage{Perf: &p},
		}
		app.render(r.Context(), w, "search_error.gohtml", &model)
		return
	}
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleUser handles /user/{ripper_host}/{user_name}
func (app *App) handleUser(w http.ResponseWriter, r *http.Request) {
	ripperHost := r.PathValue("ripper_host")
	userName := r.PathValue("user_name")
	if ripperHost == "" || userName == "" {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /user/{ripper_host}/{user_name}"))
		return
	}
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		size := 10
		offset := 0

		var albumsTotal int
		albumsTotal, err := app.getUserAlbumHits(ctx, ripperHost, userName)
		if err != nil {
			return err
		}

		albums, err := app.getUserAlbumsPage(ctx, ripperHost, userName, size, offset, SortFetched)
		if err != nil {
			return err
		}

		var filesTotal int
		filesTotal, err = app.getUserFileHits(ctx, ripperHost, userName)
		if err != nil {
			return err
		}

		files, err := app.getUserFilesPage(ctx, ripperHost, userName, size, offset, SortFetched)
		if err != nil {
			return err
		}

		model := types.UserPage{
			Host:        ripperHost,
			User:        userName,
			Albums:      albums,
			AlbumsTotal: albumsTotal,
			Files:       files,
			FilesTotal:  filesTotal,
			Sort:        SortFetched,
			BasePage:    &types.BasePage{Perf: perf},
		}
		app.render(ctx, w, "user.gohtml", &model)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleUser handles /user/{ripper_host}/{user_name}/galleries
func (app *App) handleUserGalleries(w http.ResponseWriter, r *http.Request) {
	ripperHost := r.PathValue("ripper_host")
	userName := r.PathValue("user_name")
	if ripperHost == "" || userName == "" {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /user/{ripper_host}/{user_name}/galleries"))
		return
	}
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		page, size := getPageParams(w, r, r.URL)
		offset := (page - 1) * size
		order := getSortGalleries(w, r)

		var albumsTotal int
		albumsTotal, err := app.getUserAlbumHits(ctx, ripperHost, userName)
		if err != nil {
			return err
		}

		var filesTotal int
		filesTotal, err = app.getUserFileHits(ctx, ripperHost, userName)
		if err != nil {
			return err
		}

		albums, err := app.getUserAlbumsPage(ctx, ripperHost, userName, size, offset, order)
		if err != nil {
			return err
		}

		model := types.UserPage{
			Host:        ripperHost,
			User:        userName,
			Albums:      albums,
			AlbumsTotal: albumsTotal,
			FilesTotal:  filesTotal,
			HasPrev:     page > 1,
			HasNext:     offset+len(albums) < albumsTotal,
			Page:        page,
			PageSize:    size,
			Sort:        order,
			BasePage:    &types.BasePage{Perf: perf},
		}
		app.render(ctx, w, "user_galleries.gohtml", &model)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleUser handles /user/{ripper_host}/{user_name}/files
func (app *App) handleUserFiles(w http.ResponseWriter, r *http.Request) {
	ripperHost := r.PathValue("ripper_host")
	userName := r.PathValue("user_name")
	if ripperHost == "" || userName == "" {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /user/{ripper_host}/{user_name}/files"))
		return
	}
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		page, size := getPageParams(w, r, r.URL)
		offset := (page - 1) * size
		order := getSortFiles(w, r)

		var albumsTotal int
		albumsTotal, err := app.getUserAlbumHits(ctx, ripperHost, userName)
		if err != nil {
			return err
		}

		var filesTotal int
		filesTotal, err = app.getUserFileHits(ctx, ripperHost, userName)
		if err != nil {
			return err
		}

		files, err := app.getUserFilesPage(ctx, ripperHost, userName, size, offset, order)
		if err != nil {
			return err
		}

		model := types.UserPage{
			Host:        ripperHost,
			User:        userName,
			Files:       files,
			FilesTotal:  filesTotal,
			AlbumsTotal: albumsTotal,
			HasPrev:     page > 1,
			HasNext:     offset+len(files) < filesTotal,
			Page:        page,
			PageSize:    size,
			Sort:        order,
			BasePage:    &types.BasePage{Perf: perf},
		}
		app.render(ctx, w, "user_files.gohtml", &model)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
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

func isClientAutoplayOn(r *http.Request) bool {
	cookie, err := r.Cookie("autoplay")
	if errors.Is(err, http.ErrNoCookie) {
		// autoplay cookie is not present in the request
		return false
	} else if err != nil {
		// Unable to read cookies
		return false
	}
	// autoplay cookie was present
	return cookie.Value == "1"
}

// handleRandomGallery selects a random album and redirects to its gallery page.
func (app *App) handleRandomGallery(w http.ResponseWriter, r *http.Request) {
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var ripperHost, gid string
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			return app.Db.QueryRowContext(ctx, `
				SELECT r.host, a.gid
				  FROM album a
				  JOIN ripper r ON r.ripper_id = a.ripper_id
				 ORDER BY RANDOM()
				 LIMIT 1
			`).Scan(&ripperHost, &gid)
		}); err != nil {
			return err
		}
		http.Redirect(w, r, "/gallery/"+ripperHost+"/"+url.PathEscape(gid), http.StatusTemporaryRedirect)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleRandomFile selects a random available file and redirects to its file page.
func (app *App) handleRandomFile(w http.ResponseWriter, r *http.Request) {
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var ripperHost string
		var fileId int64
		var gid sql.NullString
		if err := app.withSQL(ctx, func(ctx context.Context) error {
			// Correct randomness, but slow-ish (avg 200ms)
			//return app.Db.QueryRowContext(ctx, `
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
			return app.Db.QueryRowContext(ctx, `
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
				        AND rf.ignored = 0
				                                          ))
				   AND rf.fetched = 1
				   AND rf.ignored = 0
				 ORDER BY remote_file_id
				 LIMIT 1
			`).Scan(&ripperHost, &fileId, &gid)
		}); err != nil {
			return err
		}
		fileIdString := strconv.FormatInt(fileId, 10)
		if gid.Valid {
			http.Redirect(w, r, "/gallery/"+ripperHost+"/"+url.PathEscape(gid.String)+"/"+url.PathEscape(fileIdString), http.StatusTemporaryRedirect)
		} else {
			http.Redirect(w, r, "/file/"+ripperHost+"/"+url.PathEscape(fileIdString), http.StatusTemporaryRedirect)
		}
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

var matchGalleryFile = regexp.MustCompile(`^/gallery/([^/]+)/([^/]+)/(.+)/?$`)
var matchGallery = regexp.MustCompile(`^/gallery/([^/]+)/(.+)/?$`)
var matchFile = regexp.MustCompile(`^/file/`)
var matchSearchGalleries = regexp.MustCompile(`^/search/galleries/?$`)
var matchSearchFiles = regexp.MustCompile(`^/search/files/?$`)
var matchBrowse = regexp.MustCompile(`^/[^/]*$`)

// handleRandomPage selects a random page within the current set of pages.
func (app *App) handleRandomPage(w http.ResponseWriter, r *http.Request) {
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		referer := r.Referer()
		if len(referer) == 0 {
			http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
			return nil
		}
		parsedUrl, err := url.Parse(referer)
		if err != nil {
			http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
			return nil
		}
		if r.Host != parsedUrl.Host {
			// Request came from some other server. Suspicious!
			http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		}
		path := parsedUrl.Path
		if len(path) == 0 {
			// Go back
			http.Redirect(w, r, parsedUrl.String(), http.StatusTemporaryRedirect)
			return nil
		}

		if m := matchGalleryFile.FindStringSubmatch(path); m != nil {
			ripperHost := m[1]
			gid := m[2]
			fileId := m[3]
			nextFileId, err := app.getRandomGalleryFilePage(ctx, ripperHost, gid, fileId)
			if err != nil {
				return err
			}
			app.httpRedirect(ctx, w, r, perf, fmt.Sprintf("/gallery/%s/%s/%d", ripperHost, gid, nextFileId), http.StatusTemporaryRedirect)
			return nil
		}
		if m := matchGallery.FindStringSubmatch(path); m != nil {
			ripperHost := m[1]
			gid := m[2]
			page, size := getPageParams(w, r, parsedUrl)
			sort := getUrlSortGalleries(parsedUrl)
			nextPage, err := app.getRandomGalleryPage(ctx, ripperHost, gid, page, size)
			if err != nil {
				return err
			}
			app.httpRedirect(ctx, w, r, perf, fmt.Sprintf("/gallery/%s/%s?page=%d&size=%d&sort=%s", ripperHost, gid, nextPage, size, sort), http.StatusTemporaryRedirect)
			return nil
		}
		if matchFile.MatchString(path) {
			http.Redirect(w, r, "/random/file", http.StatusTemporaryRedirect)
			return nil
		}
		if matchSearchGalleries.MatchString(path) {
			searchQuery := parsedUrl.Query().Get("q")
			if len(searchQuery) == 0 {
				http.Redirect(w, r, parsedUrl.String(), http.StatusTemporaryRedirect)
				return nil
			}
			page, size := getPageParams(w, r, parsedUrl)
			sort := getUrlSortSearchGalleries(parsedUrl)
			nextPage, err := app.getRandomSearchGalleryPage(ctx, searchQuery, page, size)
			if err != nil {
				return err
			}
			app.httpRedirect(ctx, w, r, perf, fmt.Sprintf("/search/galleries?page=%d&size=%d&sort=%s&q=%s", nextPage, size, sort, searchQuery), http.StatusTemporaryRedirect)
			return nil
		}
		if matchSearchFiles.MatchString(path) {
			searchQuery := parsedUrl.Query().Get("q")
			if len(searchQuery) == 0 {
				http.Redirect(w, r, parsedUrl.String(), http.StatusTemporaryRedirect)
				return nil
			}
			page, size := getPageParams(w, r, parsedUrl)
			sort := getUrlSortSearchFiles(parsedUrl)
			nextPage, err := app.getRandomSearchFilePage(ctx, searchQuery, page, size)
			if err != nil {
				return err
			}
			app.httpRedirect(ctx, w, r, perf, fmt.Sprintf("/search/files?page=%d&size=%d&sort=%s&q=%s", nextPage, size, sort, searchQuery), http.StatusTemporaryRedirect)
			return nil
		}
		if matchBrowse.MatchString(path) {
			page, size := getPageParams(w, r, parsedUrl)
			sort := getUrlSortGalleries(parsedUrl)
			nextPage, err := app.getRandomBrowsePage(ctx, page, size)
			if err != nil {
				return err
			}
			app.httpRedirect(ctx, w, r, perf, fmt.Sprintf("/?page=%d&size=%d&sort=%s", nextPage, size, sort), http.StatusTemporaryRedirect)
			return nil
		}
		// Not supported on this URL. Go back
		http.Redirect(w, r, parsedUrl.String(), http.StatusTemporaryRedirect)
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

func (app *App) getRandomGalleryFilePage(ctx context.Context, ripperHost string, gid string, fileId string) (int64, error) {
	var nextFileId sql.NullInt64
	err := app.withSQL(ctx, func(ctx context.Context) error {
		return app.Db.QueryRowContext(ctx, `
		  WITH row_count AS (
		      SELECT COUNT(*) cnt
		        FROM remote_file rf
		        JOIN map_album_remote_file marf ON rf.remote_file_id = marf.remote_file_id
		        JOIN album a ON a.album_id = marf.album_id
		        JOIN ripper r ON r.ripper_id = rf.ripper_id
		       WHERE r.host = ?
		         AND a.gid = ?
		         AND rf.remote_file_id != ?
		         AND rf.fetched = 1
		         AND rf.ignored = 0
		                    )
		SELECT rf.remote_file_id
		  FROM remote_file rf
		  JOIN map_album_remote_file marf ON rf.remote_file_id = marf.remote_file_id
		  JOIN album a ON a.album_id = marf.album_id
		  JOIN ripper r ON r.ripper_id = rf.ripper_id
		 WHERE r.host = ?
		   AND a.gid = ?
		   AND rf.remote_file_id != ?
		   AND rf.fetched = 1
		   AND rf.ignored = 0
		 LIMIT 1 OFFSET (ABS(RANDOM()) % (
		     SELECT cnt
		       FROM row_count
		                                 ))
		`, ripperHost, gid, fileId, ripperHost, gid, fileId).Scan(&nextFileId)
	})
	if err != nil {
		return 0, err
	}
	if nextFileId.Valid {
		return nextFileId.Int64, nil
	}
	return 0, fmt.Errorf("gallery file not found")
}

func (app *App) getRandomGalleryPage(ctx context.Context, ripperHost string, gid string, page int, size int) (int64, error) {
	var count int64
	err := app.withSQL(ctx, func(ctx context.Context) error {
		return app.Db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			  FROM album a
			  JOIN map_album_remote_file marf ON marf.album_id = a.album_id
			  JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
			  JOIN ripper r ON r.ripper_id = a.ripper_id
			 WHERE r.host = ?
			   AND a.gid = ?
			   AND rf.fetched = 1
			   AND rf.ignored = 0
		`, ripperHost, gid).Scan(&count)
	})
	if err != nil {
		return 0, err
	}
	pageCount := getPageCount(count, int64(size))
	if pageCount <= 1 {
		return 1, nil
	}
	n := rand.Int64N(pageCount - 1)
	nextPage := n + 1
	if nextPage >= int64(page) {
		nextPage += 1
	}
	return nextPage, nil
}

func (app *App) getRandomSearchGalleryPage(ctx context.Context, searchQuery string, page int, size int) (int64, error) {
	totalHits, err := app.getSearchAlbumHits(ctx, searchQuery, false)
	if err != nil {
		return 0, err
	}
	pageCount := getPageCount(int64(totalHits), int64(size))
	if pageCount <= 1 {
		return 1, nil
	}
	n := rand.Int64N(pageCount - 1)
	nextPage := n + 1
	if nextPage >= int64(page) {
		nextPage += 1
	}
	return nextPage, nil
}

func (app *App) getRandomSearchFilePage(ctx context.Context, searchQuery string, page int, size int) (int64, error) {
	totalHits, err := app.getSearchFileHits(ctx, searchQuery, false)
	if err != nil {
		return 0, err
	}
	pageCount := getPageCount(int64(totalHits), int64(size))
	if pageCount <= 1 {
		return 1, nil
	}
	n := rand.Int64N(pageCount - 1)
	nextPage := n + 1
	if nextPage >= int64(page) {
		nextPage += 1
	}
	return nextPage, nil
}

func (app *App) getRandomBrowsePage(ctx context.Context, page int, size int) (int64, error) {
	totalHits, err := app.getTotalAlbumCount(ctx)
	if err != nil {
		return 0, err
	}
	pageCount := getPageCount(int64(totalHits), int64(size))
	if pageCount <= 1 {
		return 1, nil
	}
	n := rand.Int64N(pageCount - 1)
	nextPage := n + 1
	if nextPage >= int64(page) {
		nextPage += 1
	}
	return nextPage, nil
}

func (app *App) cleanJoin(elem ...string) string {
	// absolute paths can't be joined; discard all paths prior to the last absolute path
	lastAbsoluteIndex := 0
	for i := 0; i < len(elem); i++ {
		if strings.HasPrefix(elem[i], "/") {
			lastAbsoluteIndex = i
		}
	}
	joined := filepath.Join(elem[lastAbsoluteIndex:]...)
	// prevent path traversal by resolving and ensuring it stays under mediaRoot
	absRoot, _ := filepath.Abs(app.MediaRoot)
	absJoined, _ := filepath.Abs(joined)
	if !strings.HasPrefix(absJoined, absRoot) {
		return absRoot
	}
	return absJoined
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

// handleFilePost handles POST /file/{ripper_host}/{file_id}
func (app *App) handleFilePost(w http.ResponseWriter, r *http.Request) {
	if app.DbRw == nil {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusForbidden, fmt.Errorf("database is read-only, cannot save rating"))
		return
	}
	ripperHost := r.PathValue("ripper_host")
	fileIdString := r.PathValue("file_id")
	if ripperHost == "" || fileIdString == "" {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /file/{ripper_host}/{file_id}"))
		return
	}
	fileId, err := strconv.ParseInt(fileIdString, 10, 64)
	if err != nil || fileId <= 0 {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("invalid file id"))
		return
	}
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		ratingString := r.FormValue("rating")
		if ratingString == "unset" {
			return app.withSQL(ctx, func(ctx context.Context) error {
				_, err := app.DbRw.ExecContext(ctx, `
					UPDATE remote_file
					   SET local_rating = NULL
					  FROM ripper r
					 WHERE remote_file_id = ?
					   AND r.ripper_id = remote_file.ripper_id
					   AND r.host = ?
				`, fileId, ripperHost)
				return err
			})
		}
		if len(ratingString) > 0 {
			rating, err := strconv.Atoi(ratingString)
			if err != nil || rating < 1 || rating > 5 {
				return fmt.Errorf(`invalid rating, must be 1-5 or "unset"`)
				// TODO change perfTracker callback to be (statusCode, error)
				//return app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("invalid rating, must be 1-5 or unset"))
			}
			return app.withSQL(ctx, func(ctx context.Context) error {
				_, err := app.DbRw.ExecContext(ctx, `
					UPDATE remote_file
					   SET local_rating = ?
					  FROM ripper r
					 WHERE remote_file_id = ?
					   AND r.ripper_id = remote_file.ripper_id
					   AND r.host = ?
				`, rating, fileId, ripperHost)
				return err
			})
		}
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}

	app.httpRedirect(r.Context(), w, r, &p, r.Referer(), http.StatusSeeOther)
}

// handleGalleryPost handles POST /gallery/{ripper_host}/{gid}
func (app *App) handleGalleryPost(w http.ResponseWriter, r *http.Request) {
	if app.DbRw == nil {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusForbidden, fmt.Errorf("database is read-only, cannot save rating"))
		return
	}
	ripperHost := r.PathValue("ripper_host")
	gid := r.PathValue("gid")
	if ripperHost == "" || gid == "" {
		app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("expected values for all path parts: /gallery/{ripper_host}/{gid}"))
		return
	}
	p, err := app.perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		ratingString := r.FormValue("rating")
		if ratingString == "unset" {
			return app.withSQL(ctx, func(ctx context.Context) error {
				_, err := app.DbRw.ExecContext(ctx, `
					UPDATE album
					   SET local_rating = NULL
					  FROM ripper r
					 WHERE gid = ?
					   AND r.ripper_id = album.ripper_id
					   AND r.host = ?
				`, gid, ripperHost)
				return err
			})
		}
		if len(ratingString) > 0 {
			rating, err := strconv.Atoi(ratingString)
			if err != nil || rating < 1 || rating > 5 {
				return fmt.Errorf(`invalid rating, must be 1-5 or "unset"`)
				// TODO change perfTracker callback to be (statusCode, error)
				//return app.renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("invalid rating, must be 1-5 or unset"))
			}
			return app.withSQL(ctx, func(ctx context.Context) error {
				_, err := app.DbRw.ExecContext(ctx, `
					UPDATE album
					   SET local_rating = ?
					  FROM ripper r
					 WHERE gid = ?
					   AND r.ripper_id = album.ripper_id
					   AND r.host = ?
				`, rating, gid, ripperHost)
				return err
			})
		}
		return nil
	})
	if err != nil {
		app.renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}

	app.httpRedirect(r.Context(), w, r, &p, r.Referer(), http.StatusSeeOther)
}
