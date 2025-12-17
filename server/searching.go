package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"golocalgal/types"
	"golocalgal/vars"
	"net/http"
	"strings"
)

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
func getSearchAlbumsPage(ctx context.Context, searchQuery string, size int, offset int, order string) ([]types.Album, error) {
	var albums []types.Album
	if err := withSQL(ctx, func() error {
		var rows *sql.Rows
		var err error

		if order == SortRank || order == SortDefault {
			// Need to compute bm25 for ranked sort, but no need to enumerate all matches
			rows, err = vars.Db.QueryContext(ctx, `
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
		} else {
			// Need to enumerate all matches for nonranked sort, but no need to compute bm25
			var orderBy string
			switch order {
			case SortFetched:
				orderBy = "ORDER BY a.inserted_ts DESC, a.album_id DESC"
			case SortUploaded:
				orderBy = "ORDER BY a.created_ts DESC, a.album_id DESC"
			}
			rows, err = vars.Db.QueryContext(ctx, strings.Replace(`
				  WITH matches AS (
				      SELECT af5.ROWID
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
				                  )
				SELECT 0 -- placeholder value for score
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
				 --ORDER BY m.score
				  /*ORDER_BY*/
				 LIMIT ? OFFSET ?
			`, "/*ORDER_BY*/", orderBy, 1), searchQuery, size, offset)

		}
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

func getSearchFilesPage(ctx context.Context, searchQuery string, size int, offset int, order string) ([]types.File, error) {
	var files []types.File
	if err := withSQL(ctx, func() error {
		var rows *sql.Rows
		var err error

		if order == SortRank || order == SortDefault {
			// Need to compute bm25 for ranked sort, but no need to enumerate all matches
			rows, err = vars.Db.QueryContext(ctx, `
				  WITH matches AS (
				      SELECT rff5.ROWID, BM25(remote_file_fts5, 9.0, 6.0) AS score
				        FROM remote_file_fts5 rff5
				        JOIN remote_file rf ON rf.remote_file_id = rff5.ROWID
				       WHERE remote_file_fts5 MATCH ?
				         AND rf.fetched = 1
				         AND rf.ignored = 0
				       ORDER BY score, rf.remote_file_id DESC
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
				 ORDER BY m.score, rf.remote_file_id DESC
			`, searchQuery, size, offset)
		} else {
			// Need to enumerate all matches for nonranked sort, but no need to compute bm25
			var orderBy string
			switch order {
			case SortBytes:
				orderBy = "ORDER BY rf.bytes DESC, rf.remote_file_id DESC"
			case SortFetched:
				orderBy = "ORDER BY rf.inserted_ts DESC, rf.remote_file_id DESC"
			case SortUploaded:
				orderBy = "ORDER BY rf.uploaded_ts DESC, rf.remote_file_id DESC"
			}
			rows, err = vars.Db.QueryContext(ctx, strings.Replace(`
				  WITH matches AS (
				      SELECT rff5.ROWID
				        FROM remote_file_fts5 rff5
				        JOIN remote_file rf ON rf.remote_file_id = rff5.ROWID
				       WHERE remote_file_fts5 MATCH ?
				         AND rf.fetched = 1
				         AND rf.ignored = 0
				                  )
				SELECT 0 -- placeholder value for score
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
				 --ORDER BY m.score
				  /*ORDER_BY*/
				 LIMIT ? OFFSET ?
			`, "/*ORDER_BY*/", orderBy, 1), searchQuery, size, offset)
		}

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

// getSearchTagsPage searches for tags. If limit is -1, sqlite returns unlimited matches
func getSearchTagsPage(ctx context.Context, searchQuery string, limit int) ([]types.Tag, error) {
	//limitString := "ALL"
	//if limit > 0 {
	//	limitString = strconv.Itoa(limit)
	//}
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
		`, searchQuery, limit)
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
		return nil, err
	}
	return tags, nil
}

// handleSearch handles /search
func handleSearch(w http.ResponseWriter, r *http.Request) {
	p, err := perfTracker(r.Context(), func(ctx context.Context, perf *types.Perf) error {
		q := r.URL.Query()
		searchQuery := q.Get("q")
		size := 10
		offset := 0
		if len(searchQuery) == 0 {
			model := types.SearchPage{
				BasePage: types.BasePage{Perf: perf},
			}
			return render(r.Context(), w, "search_noquery.gohtml", model)
		}

		// 1: Search albums
		var albumsTotal int
		albumsTotal, err := getSearchAlbumHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}

		albums, err := getSearchAlbumsPage(ctx, searchQuery, size, offset, SortRank)
		if err != nil {
			return err
		}

		// 2: Search files
		var filesTotal int
		filesTotal, err = getSearchFileHits(ctx, searchQuery, false)
		if err != nil {
			return err
		}

		files, err := getSearchFilesPage(ctx, searchQuery, size, offset, SortRank)
		if err != nil {
			return err
		}

		// 3: Search tags
		var tagsTotal int
		tagsTotal, err = getSearchTagHits(ctx, searchQuery)
		if err != nil {
			return err
		}

		tags, err := getSearchTagsPage(ctx, searchQuery, 100)
		if err != nil {
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
			Sort:        SortRank,
			BasePage:    types.BasePage{Perf: perf},
		}
		return render(ctx, w, "search.gohtml", model)
	})
	if err != nil {
		renderError(r.Context(), w, &p, http.StatusInternalServerError, err)
		return
	}
}
