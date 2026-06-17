package server

import (
	"context"
	"database/sql"
	"fmt"
	"golocalgal/internal/types"
	"strings"
)

func (app *App) getUserAlbumHits(ctx context.Context, host string, uploader string, rf types.RatingFilter) (int, error) {
	var albumsTotal int
	rfClause, rfArgs := ratingFilterSQL("a.local_rating", rf)
	replacer := strings.NewReplacer("/*RATING_FILTER*/", rfClause)
	err := app.withSQL(ctx, func(ctx context.Context) error {
		args := []any{host, uploader}
		args = append(args, rfArgs...)
		return app.Db.QueryRowContext(ctx, replacer.Replace(`
			SELECT COUNT(*)
			  FROM album a
			  JOIN ripper r ON r.ripper_id = a.ripper_id
			 WHERE r.host = ?
			   AND a.uploader = ?
			   AND a.cnt_rf > 0
			   /*RATING_FILTER*/
		`), args...).Scan(&albumsTotal)
	})
	return albumsTotal, err
}

func (app *App) getUserAlbumsPage(ctx context.Context, ripperHost string, uploader string, size int, offset int, order string, rf types.RatingFilter) ([]types.Album, error) {
	var albums []types.Album
	if err := app.withSQL(ctx, func(ctx context.Context) error {
		var rows *sql.Rows
		var err error

		var orderBy string
		switch order {
		case SortFetched:
			orderBy = "ORDER BY a.inserted_ts DESC, a.album_id DESC"
		case SortUploaded:
			orderBy = "ORDER BY a.created_ts DESC, a.album_id DESC"
		case SortBytes:
			orderBy = "ORDER BY a.sum_rf_bytes DESC, a.album_id DESC"
		case SortItems:
			orderBy = "ORDER BY a.cnt_rf DESC, a.album_id DESC"
		default:
			orderBy = "ORDER BY a.inserted_ts DESC, a.album_id DESC"
		}
		rfClause, rfArgs := ratingFilterSQL("a.local_rating", rf)
		replacer := strings.NewReplacer("/*ORDER_BY*/", orderBy, "/*RATING_FILTER*/", rfClause)
		args := []any{ripperHost, uploader}
		args = append(args, rfArgs...)
		args = append(args, size, offset)
		//language=sqlite
		rows, err = app.Db.QueryContext(ctx, replacer.Replace(`
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
			 WHERE r.host = ?
			   AND a.uploader = ?
			   /*RATING_FILTER*/
			/*ORDER_BY*/
			 LIMIT ? OFFSET ?
		`), args...)

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
				albums = append(albums, a)
			}
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
		albums[i].Thumb.HrefPage = fmt.Sprintf("/media/%s/%s/%d", albums[i].RipperHost, albums[i].Gid, albums[i].Thumb.FileId)
		if albums[i].Thumb.Filename.Valid {
			albums[i].Thumb.HrefMedia = fmt.Sprintf("/media/%s/%s/%s", albums[i].RipperHost, albums[i].Gid, albums[i].Thumb.Filename.String)
		}
	}
	return albums, nil
}

func (app *App) getUserFileHits(ctx context.Context, host string, uploader string, rf types.RatingFilter) (int, error) {
	var filesTotal int
	rfClause, rfArgs := ratingFilterSQL("rf.local_rating", rf)
	replacer := strings.NewReplacer("/*RATING_FILTER*/", rfClause)
	err := app.withSQL(ctx, func(ctx context.Context) error {
		args := []any{host, uploader}
		args = append(args, rfArgs...)
		return app.Db.QueryRowContext(ctx, replacer.Replace(`
			SELECT COUNT(*)
			  FROM remote_file rf
			  JOIN ripper r ON r.ripper_id = rf.ripper_id
			 WHERE r.host = ?
			   AND rf.uploader = ?
			   AND rf.fetched = 1
			   AND rf.ignored = 0
			   /*RATING_FILTER*/
		`), args...).Scan(&filesTotal)
	})
	return filesTotal, err
}

func (app *App) getUserFilesPage(ctx context.Context, host string, uploader string, size int, offset int, order string, rf types.RatingFilter) ([]types.File, error) {
	var files []types.File
	if err := app.withSQL(ctx, func(ctx context.Context) error {
		var rows *sql.Rows
		var err error

		var orderBy string
		switch order {
		case SortBytes:
			orderBy = "ORDER BY rf.bytes DESC, rf.remote_file_id DESC"
		case SortFetched:
			orderBy = "ORDER BY rf.inserted_ts DESC, rf.remote_file_id DESC"
		case SortUploaded:
			orderBy = "ORDER BY rf.uploaded_ts DESC, rf.remote_file_id DESC"
		default:
			orderBy = "ORDER BY rf.inserted_ts DESC, rf.remote_file_id DESC"
		}
		rfClause, rfArgs := ratingFilterSQL("rf.local_rating", rf)
		replacer := strings.NewReplacer("/*ORDER_BY*/", orderBy, "/*RATING_FILTER*/", rfClause)
		args := []any{host, uploader}
		args = append(args, rfArgs...)
		args = append(args, size, offset)
		//language=sqlite
		rows, err = app.Db.QueryContext(ctx, replacer.Replace(`
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
			 WHERE r.host = ?
			   AND rf.uploader = ?
			   AND rf.fetched = 1
			   AND rf.ignored = 0
			   /*RATING_FILTER*/
			/*ORDER_BY*/
			 LIMIT ? OFFSET ?
		`), args...)

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
			files = append(files, f)
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}
	for i := range files {
		files[i].HrefPage = fmt.Sprintf("/file/%s/%d", files[i].RipperHost, files[i].FileId)
		if files[i].Filename.Valid {
			files[i].HrefMedia = fmt.Sprintf("/media/%s/%s", files[i].RipperHost, files[i].Filename.String)
		}
	}
	return files, nil
}
