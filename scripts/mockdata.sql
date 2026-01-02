-- ====================
--  Mock data generator
-- ====================
PRAGMA temp_store = MEMORY;

BEGIN TRANSACTION;

--------------------------------------------------------------------
-- ripper
--------------------------------------------------------------------
SELECT datetime() || ' insert into ripper';
INSERT INTO ripper (name, host)
VALUES ('FlickrRipper', 'flickr.com')
     , ('VimeoRipper', 'vimeo.com')
     , ('YoutubeRipper', 'youtube.com')
     , ('PhotobucketRipper', 'photobucket.com');

--------------------------------------------------------------------
-- mime_type
--------------------------------------------------------------------
SELECT datetime() || ' insert into mime_type';
INSERT INTO mime_type (name)
VALUES ('image/jpeg')
     , ('image/png')
     , ('image/jxl')
     , ('video/mp4')
--, ('video/webm') -- Commented for now, because the placeholder videos are only mp4
;

--------------------------------------------------------------------
-- tag
--------------------------------------------------------------------
SELECT datetime() || ' insert into tag';
INSERT INTO tag (name, local)
VALUES ('nature', 0)
     , ('portrait', 0)
     , ('landscape', 0)
     , ('action', 0)
     , ('night', 0)
     , ('macro', 0)
     , ('architecture', 0)
     , ('animal', 0)
     , ('sports', 0)
     , ('abstract', 0);

--------------------------------------------------------------------
-- temp tables for random strings
--------------------------------------------------------------------
CREATE TEMP TABLE adjectives
(
    adju TEXT
);
INSERT INTO adjectives (adju)
VALUES ('Golden')
     , ('Silent')
     , ('Misty')
     , ('Bright')
     , ('Ancient')
     , ('Majestic')
     , ('Vibrant')
     , ('Ethereal')
     , ('Stormy')
     , ('Hidden')
     , ('Radiant')
     , ('Noisy')
     , ('Whispering')
     , ('Bold')
     , ('Soft');

CREATE TEMP TABLE nouns
(
    noun TEXT
);
INSERT INTO nouns (noun)
VALUES ('Mountain')
     , ('River')
     , ('Forest')
     , ('Desert')
     , ('Valley')
     , ('Ocean')
     , ('Sky')
     , ('Sunset')
     , ('Star')
     , ('Breeze')
     , ('Garden')
     , ('Lake')
     , ('Canyon')
     , ('Glacier')
     , ('Village');

CREATE TEMP TABLE activities
(
    act TEXT
);
INSERT INTO activities (act)
VALUES ('exploration')
     , ('documentary')
     , ('adventure')
     , ('journey')
     , ('photography')
     , ('time lapse')
     , ('safari')
     , ('voyage')
     , ('wilderness')
     , ('trails');

CREATE TEMP TABLE locations
(
    loc TEXT
);
INSERT INTO locations (loc)
VALUES ('in the Himalayas')
     , ('by the Sahara')
     , ('across the Amazon')
     , ('within the Black Forest')
     , ('along the Mekong')
     , ('at the edge of the Arctic')
     , ('beneath the Mediterranean')
     , ('in the heart of Kyoto')
     , ('within the Grand Canyon')
     , ('at the base of Machu Picchu');

CREATE TEMP TABLE uploaders
(
    username TEXT
);
INSERT INTO uploaders (username)
VALUES ('user1')
     , ('user2')
     , ('user3')
     , ('user4')
     , ('user5')
     , ('user6')
     , ('user7')
     , ('user8')
     , ('user9')
     , ('user10');

--------------------------------------------------------------------
-- album titles & descriptions
--------------------------------------------------------------------
SELECT datetime() || ' creating random album titles';
CREATE TEMP TABLE album_titles
(
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT
);
INSERT INTO album_titles (title)
SELECT a.adju || ' ' || b.noun || ' Gallery'
  FROM adjectives a
  JOIN nouns b
 ORDER BY random()
 LIMIT 700;

SELECT datetime() || ' creating random album descriptions';
CREATE TEMP TABLE album_descriptions
(
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    description TEXT
);
INSERT INTO album_descriptions (description)
SELECT a.adju || ' ' || b.noun || ' captured during a '
           || c.act || ' at ' || d.loc
  FROM adjectives a
  JOIN nouns b
  JOIN activities c
  JOIN locations d
 ORDER BY random()
 LIMIT 700;

--------------------------------------------------------------------
-- insert album rows
--------------------------------------------------------------------
SELECT datetime() || ' insert into album';
  WITH RECURSIVE seq_album(i) AS (
      SELECT 1
       UNION ALL
      SELECT i + 1
        FROM seq_album
       WHERE i < 700
                                 )
     , now_ts AS (
      SELECT strftime('%s', 'now') * 1000 AS ts
                                 )
INSERT
  INTO album ( ripper_id, gid, url, uploader, title, description, created_ts, modified_ts, fetch_count, hidden, removed
             , local_rating, sum_rf_bytes, cnt_rf, last_fetch_ts, inserted_ts)
SELECT ((i - 1) % 4) + 1
     , 'album' || i
     , 'https://example.com/album' || i || '/'
     , (
    SELECT username
      FROM uploaders
     WHERE i = i -- correlate to outer row so each subquery gets a different RANDOM()
     ORDER BY RANDOM()
     LIMIT 1
       )
     , (
    SELECT title
      FROM album_titles
     WHERE id = ((i - 1) % (
         SELECT COUNT(*)
           FROM album_titles
                           )) + 1
       )
     , (
    SELECT description
      FROM album_descriptions
     WHERE id = ((i - 1) % (
         SELECT COUNT(*)
           FROM album_descriptions
                           )) + 1
       )
     , (now_ts.ts - (i * 86400000))
     , (now_ts.ts - (i * 43200000))
     , 1
     , 0
     , 0
     , (
    SELECT CASE
               WHEN r.val < 5 THEN 5
               WHEN r.val < 10 THEN 4
               WHEN r.val < 15 THEN 3
               WHEN r.val < 17 THEN 2
               WHEN r.val < 18 THEN 1
               END
      FROM (
          SELECT ABS(RANDOM()) % 100 AS val
           ) r
       )
     , 0
     , 0
     , NULL
     , (now_ts.ts - (i * 8640000))
  FROM seq_album
  JOIN now_ts;

--------------------------------------------------------------------
-- remote_file titles & descriptions
--------------------------------------------------------------------
SELECT datetime() || ' creating random remote_file titles';
CREATE TEMP TABLE remote_file_titles
(
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT
);
INSERT INTO remote_file_titles (title)
SELECT a.adju || ' ' || b.noun || ' '
           || CASE WHEN abs(random()) % 2 = 0 THEN 'Photo' ELSE 'Video' END
  FROM adjectives a
  JOIN nouns b
 ORDER BY random()
 LIMIT 300;

SELECT datetime() || ' creating random remote_file descriptions';
CREATE TEMP TABLE remote_file_descriptions
(
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    description TEXT
);
INSERT INTO remote_file_descriptions (description)
SELECT a.adju || ' ' || b.noun || ' captured during a '
           || c.act || ' at ' || d.loc
  FROM adjectives a
  JOIN nouns b
  JOIN activities c
  JOIN locations d
 ORDER BY random()
 LIMIT 22500;

SELECT datetime() || ' choosing how many files each album gets';
CREATE TEMP TABLE album_file_counts
(
    album_id   INTEGER PRIMARY KEY,
    file_count INTEGER NOT NULL
);
INSERT INTO album_file_counts (album_id, file_count)
SELECT album.album_id
     , (ABS(random()) % 396) + 5 -- 5 to 400
  FROM album;

-- the total number of files in all albums
SELECT datetime() || ' numbering each file for insertion';
CREATE TEMP TABLE album_counts AS
SELECT afc.album_id
     , a.ripper_id
     , afc.file_count
     , SUM(afc.file_count) OVER (ORDER BY afc.album_id) AS cumulative_end
  FROM album_file_counts afc
  JOIN album a ON a.album_id = afc.album_id;

-- to create map_album_remote_file
SELECT datetime() || ' associating each file with an album';
CREATE TEMP TABLE file_rows
(
    album_id  INTEGER,
    ripper_id INTEGER,
    file_id   INTEGER
);
  WITH RECURSIVE numbers(n) AS (
      SELECT 1
       UNION ALL
      SELECT n + 1
        FROM numbers
       WHERE n < (
           SELECT SUM(file_count)
             FROM album_counts
                 )
                               )
     , album_ranges AS (
      SELECT album_id
           , ripper_id
           , file_count
           , SUM(file_count) OVER (ORDER BY album_id) AS cumulative_end
           , COALESCE(SUM(file_count) OVER (ORDER BY album_id)
                          - file_count, 0) AS cumulative_start
        FROM album_counts
                               )
INSERT
  INTO file_rows
SELECT a.album_id
     , a.ripper_id
     , n.n AS file_id
  FROM album_ranges a
  JOIN numbers n
          ON n.n BETWEEN a.cumulative_start + 1
          AND a.cumulative_end;

-- CTEs to build the per-file data set
  WITH RECURSIVE numbers(n) AS (
      SELECT 1
       UNION ALL
      SELECT n + 1
        FROM numbers
       WHERE n < (
           SELECT SUM(file_count)
             FROM album_counts
                 )
                               )
     , album_ranges AS (
      SELECT album_id
           , ripper_id
           , file_count
           , SUM(file_count) OVER (ORDER BY album_id) AS cumulative_end
           , COALESCE(SUM(file_count) OVER (ORDER BY album_id)
                          - file_count, 0) AS cumulative_start
        FROM album_counts
                               )
     , now_ts(ts) AS (
      SELECT strftime('%s', 'now') * 1000
                               )
-- Insert the remote_file rows
INSERT
  INTO remote_file ( ripper_id, urlid, url_base, url_path, filename, mime_type_id, bytes, width_px, height_px
                   , duration_ms, title, description, uploaded_ts, uploader, aux, hidden, removed, fetched, ignored
                   , local_rating, inserted_ts)
SELECT fr.ripper_id
     , 'album' || fr.album_id || '_file' || fr.file_id
     , 'https://example.com'
     , '/album' || fr.album_id || '/file' || fr.file_id
     , 'file_' || fr.file_id || '.' || (
    SELECT SUBSTR(name, INSTR(name, '/') + 1)
      FROM mime_type
     WHERE mime_type_id = ((fr.file_id - 1) % (
         SELECT COUNT(*)
           FROM mime_type
                                              )) + 1
                                       )
     , ((fr.file_id - 1) % (
    SELECT COUNT(*)
      FROM mime_type
                           )) + 1
     , ((fr.file_id * 12345) % 5000000) + 500000
     , 800 + ((fr.file_id * 3) % 1200)
     , 600 + ((fr.file_id * 7) % 900)
     , NULL
     , (
    SELECT title
      FROM remote_file_titles
     WHERE id = ((fr.file_id - 1) % (
         SELECT COUNT(*)
           FROM remote_file_titles
                                    )) + 1
       )
     , (
    SELECT description
      FROM remote_file_descriptions
     WHERE id = ((fr.file_id - 1) % (
         SELECT COUNT(*)
           FROM remote_file_descriptions
                                    )) + 1
       )
     , (now_ts.ts - (fr.file_id * 7200000))
     , (
    SELECT username
      FROM uploaders
     WHERE fr.file_id = fr.file_id -- correlate to outer row so each subquery gets a different RANDOM()
     ORDER BY RANDOM()
     LIMIT 1
       )
     , NULL
     , ABS(RANDOM()) % 100 < 5
     , ABS(RANDOM()) % 100 < 5
     , ABS(RANDOM()) % 100 < 95
     , ABS(RANDOM()) % 100 < 5
     , (
    SELECT CASE
               WHEN r.val < 5 THEN 5 -- 5% chance
               WHEN r.val < 10 THEN 4 -- 5% chance
               WHEN r.val < 15 THEN 3 -- 5% chance
               WHEN r.val < 17 THEN 2 -- 2% chance
               WHEN r.val < 18 THEN 1 -- 1% chance
               END
      FROM (
          SELECT ABS(RANDOM()) % 100 AS val
           ) r
       )
     , now_ts.ts
  FROM file_rows fr
  JOIN now_ts;

--------------------------------------------------------------------
-- map_album_remote_file
--------------------------------------------------------------------
SELECT datetime() || ' insert into map_album_remote_file';
-- For every remote_file, pick 1-5 albums with the same ripper id
  WITH RECURSIVE
      -- Pick how many additional albums each file will belong to
      file_assoc_count(remote_file_id, assoc_n) AS (
          SELECT remote_file_id
               , (abs(random()) % 5)
            FROM remote_file
                                                   )
     ,
      -- build a list of (remote_file_id, album_id) pairs
      assoc_rows(remote_file_id, seq, album_id) AS (
          -- first album
          SELECT f.remote_file_id
               , 1
               , (
              SELECT album_id
                FROM album a
               WHERE a.ripper_id = rf.ripper_id
               ORDER BY random()
               LIMIT 1
                 )
            FROM file_assoc_count f
            JOIN remote_file rf ON rf.remote_file_id = f.remote_file_id
           UNION ALL
   -- next album
          SELECT r.remote_file_id
               , r.seq
               , (
              SELECT album_id
                FROM album a
               WHERE a.ripper_id = rf.ripper_id
               ORDER BY random()
               LIMIT 1
                 )
            FROM assoc_rows r
            JOIN file_assoc_count f ON f.remote_file_id = r.remote_file_id
            JOIN remote_file rf ON rf.remote_file_id = r.remote_file_id
           WHERE r.seq < f.assoc_n
             AND f.assoc_n > 0
                                                   )
INSERT
  INTO map_album_remote_file (album_id, remote_file_id)
SELECT DISTINCT album_id, remote_file_id
  FROM assoc_rows;

--------------------------------------------------------------------
-- map_album_tag
--------------------------------------------------------------------
SELECT datetime() || ' insert into map_album_tag';
  WITH RECURSIVE seq_al(i) AS (
      SELECT 1
       UNION ALL
      SELECT i + 1
        FROM seq_al
       WHERE i < 700
                              )
     , tag_map(i, t) AS (
      SELECT i, ((i - 1) % 10) + 1
        FROM seq_al
       UNION ALL
      SELECT i, ((i + 1 - 1) % 10) + 1
        FROM seq_al
       UNION ALL
      SELECT i, ((i + 2 - 1) % 10) + 1
        FROM seq_al
                              )
INSERT
  INTO map_album_tag (album_id, tag_id)
SELECT i, t
  FROM tag_map
 WHERE t <= 10;

--------------------------------------------------------------------
-- map_remote_file_tag
--------------------------------------------------------------------
SELECT datetime() || ' insert into map_remote_file_tag';
  WITH RECURSIVE seq_f(j) AS (
      SELECT 1
       UNION ALL
      SELECT j + 1
        FROM seq_f
       WHERE j < 13000
                             )
     , file_tags(j, t) AS (
      SELECT j, ((j - 1) % 10) + 1
        FROM seq_f
       UNION ALL
      SELECT j, ((j + 1 - 1) % 10) + 1
        FROM seq_f
                             )
INSERT
  INTO map_remote_file_tag (remote_file_id, tag_id)
SELECT j, t
  FROM file_tags
 WHERE t <= 10;

--------------------------------------------------------------------
-- rebuild FTS indexes
--------------------------------------------------------------------
INSERT INTO album_fts5(album_fts5)
VALUES ('rebuild');
INSERT INTO remote_file_fts5(remote_file_fts5)
VALUES ('rebuild');
INSERT INTO tag_fts5(tag_fts5)
VALUES ('rebuild');

COMMIT;
ANALYZE; -- querying is slow if not analyzed
