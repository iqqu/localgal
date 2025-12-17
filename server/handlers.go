package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"golocalgal/types"
	"golocalgal/vars"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func handle404(w http.ResponseWriter, r *http.Request) {
	renderError(r.Context(), w, &types.Perf{}, http.StatusNotFound, nil)
}

func handleStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=86400")
	vars.StaticFSHandler.ServeHTTP(w, r)
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
			return vars.Db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				  FROM album
				 WHERE fetch_count > 0
			`).Scan(&total)
		}); err != nil {
			return err
		}
		var list []types.Album
		if err := withSQL(ctx, func() error {
			rows, err := vars.Db.QueryContext(ctx, `
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
				     , a.local_rating
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
					&a.LocalRating,
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
				return vars.Db.QueryRowContext(ctx, `
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
			return vars.Db.QueryRowContext(ctx, `
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
				     , a.local_rating
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
				&a.LocalRating,
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
			return vars.Db.QueryRowContext(ctx, `
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
			rows, err := vars.Db.QueryContext(ctx, `
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
				  JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id AND rf.fetched = 1
				  -- JOIN ripper r ON r.ripper_id = rf.ripper_id
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
		var albumTags, fileTags []types.Tag
		if err := withSQL(ctx, func() error {
			rows, e := vars.Db.QueryContext(ctx, `
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
			rows, e := vars.Db.QueryContext(ctx, `
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
			return vars.Db.QueryRowContext(ctx, `
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
			return vars.Db.QueryRowContext(ctx, `
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
			rows, e := vars.Db.QueryContext(ctx, `
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
			rows, e := vars.Db.QueryContext(ctx, `
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
			rows, e := vars.Db.QueryContext(ctx, `
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
	// TODO enable visiting a page by File urlid (need to handle related galleries fragment too)
	fileId, err := strconv.ParseInt(fileIdString, 10, 64)
	if err != nil || fileId <= 0 {
		renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("invalid file id, must be a positive integer"))
		return
	}

	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var f types.File
		if err := withSQL(ctx, func() error {
			f.RipperHost = ripperHost
			return vars.Db.QueryRowContext(ctx, `
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
			rows, e := vars.Db.QueryContext(ctx, `
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
		rows, e := vars.Db.QueryContext(ctx, `
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
			return vars.Db.QueryRowContext(ctx, `
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
			return vars.Db.QueryRowContext(ctx, `
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
			return vars.Db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				  FROM album a
				  JOIN map_album_tag mat ON mat.album_id = a.album_id AND mat.tag_id = ?
			`, t.TagId).Scan(&total)
		}); err != nil {
			return err
		}
		var albums []types.Album
		if err := withSQL(ctx, func() error {
			rows, e := vars.Db.QueryContext(ctx, `
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
				return vars.Db.QueryRowContext(ctx, `
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
		if err := withSQL(ctx, func() error {
			rows, e := vars.Db.QueryContext(ctx, `
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
			rows, err := vars.Db.QueryContext(ctx, `
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
			rows, err := vars.Db.QueryContext(ctx, `
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

func getSearchAlbumHits(ctx context.Context, searchQuery string, evictCache bool) (int, error) {
	var err error
	var albumsTotal int
	maxCacheAgeMs := 300000 // 5 minutes
	queryHash := fmt.Sprintf("%x", sha256.Sum256([]byte(searchQuery)))

	// 1: Evict old entries
	err = withSQL(ctx, func() error {
		_, err := vars.CacheDb.ExecContext(ctx, `
			DELETE
			  FROM search_hits
			 WHERE (UNIXEPOCH('subsec') * 1000) - inserted_ts > ?
			    OR (query_hash = ? AND table_name = 'album' AND ?)
		`, maxCacheAgeMs, queryHash, evictCache)
		return err
	})
	if err != nil {
		return 0, err
	}
	// 2: Get cached entry
	err = withSQL(ctx, func() error {
		return vars.CacheDb.QueryRowContext(ctx, `
			SELECT hits
			  FROM search_hits
			 WHERE query_hash = ?
			   AND table_name = 'album'
			 ORDER BY inserted_ts DESC
			 LIMIT 1
		`, queryHash).Scan(&albumsTotal)
	})
	if err == nil {
		return albumsTotal, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}

	// 3: No entry was cached; get total hits
	err = withSQL(ctx, func() error {
		return vars.Db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			  FROM album_fts5 af5
			 WHERE album_fts5 MATCH ?
			   AND EXISTS(
			     SELECT 1
			       FROM map_album_remote_file marf
			       JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
			      WHERE marf.album_id = af5.ROWID
			        AND rf.fetched = 1
			        AND rf.ignored = 0
			             )
		`, searchQuery).Scan(&albumsTotal)
	})
	if err != nil {
		return albumsTotal, err
	}

	// 4: Cache result
	err = withSQL(ctx, func() error {
		_, err := vars.CacheDb.ExecContext(ctx, `
			INSERT INTO search_hits (query_hash, table_name, hits)
			VALUES (?, 'album', ?)
		`, queryHash, albumsTotal)
		return err
	})
	return albumsTotal, err
}
func getSearchAlbumsPage(ctx context.Context, searchQuery string, size int, offset int) ([]types.Album, error) {
	var albums []types.Album
	if err := withSQL(ctx, func() error {
		rows, err := vars.Db.QueryContext(ctx, `
			  WITH matches AS (
			      SELECT af5.ROWID
			           , BM25(album_fts5, 9.0, 6.0) AS score
			        FROM album_fts5 af5
			       WHERE album_fts5 MATCH ?
			         AND EXISTS(
			           SELECT 1
			             FROM map_album_remote_file marf
			             JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
			            WHERE marf.album_id = af5.ROWID
			              AND rf.fetched = 1
			              AND rf.ignored = 0
			                   )
			       ORDER BY score
			       LIMIT ? OFFSET ?
			                  )
			SELECT m.score
			     , a.album_id
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
			     , a.local_rating
			     , a.last_fetch_ts
			     , a.inserted_ts
			     , (
			    SELECT COUNT(*)
			      FROM map_album_remote_file marf
			      JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
			     WHERE marf.album_id = a.album_id
			       AND rf.fetched = 1
			       AND rf.ignored = 0
			       ) AS file_count
			     , (
			    SELECT rf.remote_file_id
			      FROM map_album_remote_file marf
			      JOIN remote_file rf ON rf.remote_file_id = marf.remote_file_id
			     WHERE marf.album_id = a.album_id
			       AND rf.fetched = 1
			       AND rf.ignored = 0
			     ORDER BY marf.remote_file_id
			     LIMIT 1
			       ) AS thumb_remote_file_id
			  FROM matches m
			  JOIN album a ON a.album_id = m.ROWID
			  JOIN ripper r ON r.ripper_id = a.ripper_id
			 ORDER BY m.score
		`, searchQuery, size, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a types.Album
			var f types.File
			var thumbFileId sql.NullInt64
			var score *float64
			if err := rows.Scan(
				&score,
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
				&a.LocalRating,
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
				albums = append(albums, a)
			}
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}
	for i := range albums {
		thumb := albums[i].Thumb
		if err := withSQL(ctx, func() error {
			return vars.Db.QueryRowContext(ctx, `
				SELECT rf.filename
				     , mt.name AS mime_type
				  FROM remote_file rf
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 WHERE rf.remote_file_id = ?
				   AND rf.fetched = 1
			`, thumb.FileId).Scan(&thumb.Filename, &thumb.MimeType)
		}); err != nil {
			return nil, err
		}
		albums[i].Thumb = thumb
	}
	// Populate href
	for i := range albums {
		albums[i].HrefPage = fmt.Sprintf("/gallery/%s/%s", albums[i].RipperHost, albums[i].Gid)
		albums[i].Thumb.HrefPage = fmt.Sprintf("/media/%s/%s/%d", albums[i].RipperHost, albums[i].Gid, albums[i].Thumb.FileId)
		if albums[i].Thumb.Filename.Valid {
			albums[i].Thumb.HrefMedia = fmt.Sprintf("/media/%s/%s/%s", albums[i].RipperHost, albums[i].Gid, albums[i].Thumb.Filename.String)
		}
	}
	return albums, nil
}

func getSearchFileHits(ctx context.Context, searchQuery string, evictCache bool) (int, error) {
	var err error
	var filesTotal int
	maxCacheAgeMs := 300000 // 5 minutes
	queryHash := fmt.Sprintf("%x", sha256.Sum256([]byte(searchQuery)))

	// 1: Evict old entries
	err = withSQL(ctx, func() error {
		_, err := vars.CacheDb.ExecContext(ctx, `
			DELETE
			  FROM search_hits
			 WHERE (UNIXEPOCH('subsec') * 1000) - inserted_ts > ?
			    OR (query_hash = ? AND table_name = 'remote_file' AND ?)
		`, maxCacheAgeMs, queryHash, evictCache)
		return err
	})
	if err != nil {
		return 0, err
	}
	// 2: Get cached entry
	err = withSQL(ctx, func() error {
		return vars.CacheDb.QueryRowContext(ctx, `
			SELECT hits
			  FROM search_hits
			 WHERE query_hash = ?
			   AND table_name = 'remote_file'
			 ORDER BY inserted_ts DESC
			 LIMIT 1
		`, queryHash).Scan(&filesTotal)
	})
	if err == nil {
		return filesTotal, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}

	// 3: No entry was cached; get total hits
	err = withSQL(ctx, func() error {
		return vars.Db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			  FROM remote_file_fts5 rff5
			  JOIN remote_file rf ON rf.remote_file_id = rff5.ROWID
			 WHERE remote_file_fts5 MATCH ?
			   AND rf.fetched = 1
			   AND rf.ignored = 0
		`, searchQuery).Scan(&filesTotal)
	})
	if err != nil {
		return filesTotal, err
	}

	// 4: Cache result
	err = withSQL(ctx, func() error {
		_, err := vars.CacheDb.ExecContext(ctx, `
			INSERT INTO search_hits (query_hash, table_name, hits)
			VALUES (?, 'remote_file', ?)
		`, queryHash, filesTotal)
		return err
	})
	return filesTotal, err
}

func getSearchTagHits(ctx context.Context, searchQuery string) (int, error) {
	var err error
	var tagsTotal int
	// Not bothering to cache tags; there should be few enough that search is cheap
	err = withSQL(ctx, func() error {
		return vars.Db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			  FROM tag_fts5 tf5
			  JOIN tag t ON t.tag_id = tf5.ROWID
			 WHERE tag_fts5 MATCH ?
			   AND t.local = 0 -- TODO show local tags separately
		`, searchQuery).Scan(&tagsTotal)
	})
	return tagsTotal, err
}

// handleSearch handles /search
func handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	searchQuery := q.Get("q")
	if len(searchQuery) == 0 {
		renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("Search query parameter must not be empty. Example: /search?q=foo"))
		return
	}
	page, size := parsePageParams(r, 60)
	offset := (page - 1) * size
	size = 10
	offset = 0

	// 1: Search albums
	// 2: Search files
	// 3: Search tags

	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		// 1: Search albums
		var albumsTotal int
		albumsTotal, err := getSearchAlbumHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}

		albums, err := getSearchAlbumsPage(ctx, searchQuery, size, offset)
		if err != nil {
			return err
		}

		// 2: Search files
		var filesTotal int
		filesTotal, err = getSearchFileHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}

		var files []types.File
		if err := withSQL(ctx, func() error {
			rows, err := vars.Db.QueryContext(ctx, `
				  WITH matches AS (
				      SELECT rff5.ROWID, BM25(remote_file_fts5, 9.0, 6.0) AS score
				        FROM remote_file_fts5 rff5
				        JOIN remote_file rf ON rf.remote_file_id = rff5.ROWID
				       WHERE remote_file_fts5 MATCH ?
				         AND rf.fetched = 1
				         AND rf.ignored = 0
				       ORDER BY score
				       LIMIT ? OFFSET ?
				                  )
				SELECT m.score
				     , rf.remote_file_id
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
				  FROM matches m
				  JOIN remote_file rf ON rf.remote_file_id = m.ROWID
				  JOIN ripper r ON r.ripper_id = rf.ripper_id
				  LEFT JOIN mime_type mt ON mt.mime_type_id = rf.mime_type_id
				 ORDER BY m.score
			`, searchQuery, size, offset)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var f types.File
				var score *float64
				if err := rows.Scan(
					&score,
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
				files = append(files, f)
			}
			return rows.Err()
		}); err != nil {
			return err
		}
		for i := range files {
			files[i].HrefPage = fmt.Sprintf("/file/%s/%d", files[i].RipperHost, files[i].FileId)
			if files[i].Filename.Valid {
				files[i].HrefMedia = fmt.Sprintf("/media/%s/%s", files[i].RipperHost, files[i].Filename.String)
			}
		}

		// 3: Search tags
		var tagsTotal int
		tagsTotal, err = getSearchTagHits(ctx, searchQuery)
		if err != nil {
			return err
		}

		var tags []types.Tag
		if err := withSQL(ctx, func() error {
			rows, err := vars.Db.QueryContext(ctx, `
				  WITH matches AS (
				      SELECT tf5.ROWID, BM25(tag_fts5) AS score
				        FROM tag_fts5 tf5
				        JOIN tag t ON t.tag_id = tf5.ROWID
				       WHERE tag_fts5 MATCH ?
				         AND t.local = 0 -- TODO show local tags separately
				       ORDER BY score
				       LIMIT ?
				                  )
				SELECT m.score
				     , t.name
				     , (
				    SELECT COUNT(*)
				      FROM map_remote_file_tag mrft
				     WHERE t.tag_id = mrft.tag_id
				       --AND rf.fetched = 1
				    -- filtering on fetched here is quite slow
				       ) AS cnt
				  FROM matches m
				  JOIN tag t ON t.tag_id = m.ROWID
				 WHERE t.local = 0 -- TODO show local tags separately
				 ORDER BY cnt DESC, m.score
			`, searchQuery, 100)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var t types.Tag
				var score *float64
				if err := rows.Scan(
					&score,
					&t.Name,
					&t.Count,
				); err != nil {
					return err
				}
				tags = append(tags, t)
			}
			return rows.Err()
		}); err != nil {
			return err
		}

		model := types.SearchPage{
			Query:       searchQuery,
			Albums:      albums,
			AlbumsTotal: albumsTotal,
			Files:       files,
			FilesTotal:  filesTotal,
			Tags:        tags,
			TagsTotal:   tagsTotal,
			BasePage:    types.BasePage{Perf: perf},
		}
		return render(ctx, w, "search.gohtml", model)
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}

// handleSearchGalleries handles /search/galleries
func handleSearchGalleries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	searchQuery := q.Get("q")
	if len(searchQuery) == 0 {
		renderError(r.Context(), w, &types.Perf{}, http.StatusBadRequest, fmt.Errorf("Search query parameter must not be empty. Example: /search/galleries?q=foo"))
		return
	}
	page, size := parsePageParams(r, 60)
	offset := (page - 1) * size

	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		var albumsTotal int
		albumsTotal, err := getSearchAlbumHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}
		var filesTotal int
		filesTotal, err = getSearchFileHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}
		var tagsTotal int
		tagsTotal, err = getSearchTagHits(ctx, searchQuery)
		if err != nil {
			return err
		}

		albums, err := getSearchAlbumsPage(ctx, searchQuery, size, offset)
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
			BasePage:    types.BasePage{Perf: perf},
		}
		return render(ctx, w, "search_galleries.gohtml", model)
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
			return vars.Db.QueryRowContext(ctx, `
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
			//return vars.Db.QueryRowContext(ctx, `
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
			return vars.Db.QueryRowContext(ctx, `
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
	// absolute paths can't be joined; discard all paths prior to the last absolute path
	lastAbsoluteIndex := 0
	for i := 0; i < len(elem); i++ {
		if strings.HasPrefix(elem[i], "/") {
			lastAbsoluteIndex = i
		}
	}
	joined := filepath.Join(elem[lastAbsoluteIndex:]...)
	// prevent path traversal by resolving and ensuring it stays under mediaRoot
	absRoot, _ := filepath.Abs(vars.MediaRoot)
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
		preferredPath := cleanJoin(vars.MediaRoot, ripperHost+"_"+gid, name)
		tryFiles = append(tryFiles, preferredPath)

		// first fallback: ripme-mangled path
		mangledGid := filesystemSafe(gid)
		mangledName := sanitizedFilename(name)
		if mangledGid != gid || mangledName != name {
			mangledPath := cleanJoin(vars.MediaRoot, ripperHost+"_"+mangledGid, mangledName)
			tryFiles = append(tryFiles, mangledPath)
		}

		// fallback to knownFilePaths by name
		if list, ok := vars.KnownFilePaths[name]; ok {
			for _, p := range list {
				tryFiles = append(tryFiles, cleanJoin(vars.DfLogRoot, p))
			}
		}
	} else if len(parts) >= 2 { // fallback: /media/{ripper_host}/{filename}
		ripperHost := parts[0]
		name := parts[1]
		// prefer direct path under mediaRoot
		tryFiles = append(tryFiles, cleanJoin(vars.MediaRoot, ripperHost, name))

		// first fallback: ripme-mangled path
		mangledName := sanitizedFilename(name)
		if mangledName != name {
			mangledPath := cleanJoin(vars.MediaRoot, ripperHost, mangledName)
			tryFiles = append(tryFiles, mangledPath)
		}

		// fallback to knownFilePaths by name
		if list, ok := vars.KnownFilePaths[name]; ok {
			for _, p := range list {
				tryFiles = append(tryFiles, cleanJoin(vars.DfLogRoot, p))
			}
		}
	} else if len(parts) == 1 && parts[0] != "" { // last resort: find by filename only
		name := parts[0]
		if list, ok := vars.KnownFilePaths[name]; ok {
			for _, p := range list {
				tryFiles = append(tryFiles, cleanJoin(vars.DfLogRoot, p))
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
