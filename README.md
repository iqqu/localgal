# LocalGal

<img src="./static/localgal.min.svg" alt="LocalGal Icon" style="height:60px"/>

Local Gallery web server, written in Go. Companion to RipMe3 app. Preview version

"Browse your gals with pleasure!"

* Browse galleries of image and video files
* Completely self-contained; does not make external web requests
* Browsable with just keyboard or just mouse
* Works with JS disabled
* If JS enabled:
  * Hotkeys enabled
  * Auto jump to content enabled

[Screenshots](https://github.com/iqqu/localgal/wiki/Screenshots)

Pages:
* `/`: Browse galleries
* `/gallery/{ripper}/{gid}`: View gallery
* `/gallery/{ripper}/{gid}/{urlid}`: View file of gallery
* `/file/{ripper}/{urlid}`: View individual file
* `/tags`: View all tags
* `/tag/{tag}`: View tag
* `/random/gallery`: Redirect to random gallery
* `/random/file`: Redirect to random file
* `/media/`: Direct file links
* `/about`: About page
* `/healthz`

JSON API Pages:
(accepts the same query parameters used by the HTML pages)
* `/api/galleries`: Browse galleries
* `/api/gallery/{ripper}/{gid}`: View gallery
* `/api/gallery/{ripper}/{gid}/{urlid}`: View file of gallery
* `/api/file/{ripper}/{urlid}`: View individual file
* `/api/file/{ripper}/{urlid}/galleries`: View galleries associated with an individual file
* `/api/tags`: View all tags
* `/api/tag/{tag}`: View tag
* `/api/random/gallery`: Redirect to random gallery
* `/api/random/file`: Redirect to random file

Hotkeys:
* f: random file
* g: random gallery
* h or Arrow Left: previous item
* i: toggle fullscreen image
* j: jump to content
* k: jump to top
* l or Arrow Right: next item
* Shift+j: Toggle autojump

Environment variables:
* `BIND`: listen address, default `127.0.0.1:5037` (to listen on all addresses, specify `:5037`)
* `SQLITE_DSN`: sqlite data source name (connection string), default `file:ripme.sqlite`
* `SLOW_SQL_MS`: duration threshold to log slow sql queries, milliseconds, default `100`
* `MEDIA_ROOT`: rip base directory, default: `./rips`
* `DFLOG`: downloaded file log, default `./ripme.downloaded.files.log`
* `DFLOG_ROOT`: base directory to resolve relative paths in DFLOG from, default directory that DFLOG is in

Notes:
* If queries take abnormally long, run `localgal --optimize`. The command could take some minutes when optimization is needed on large databases, so do not run it while the database is being actively used.
  * Alternatively, manually execute `PRAGMA optimize;` on the database

Goals:
* Be simple
* Be more convenient for browsing RipMe3 galleries than a file manager
* Perform well on large databases

Non-goals:
* User accounts

TODO:
* Improve code organization

Wishlist (help welcome):
* Better icon
* Native sqlite driver (for performance) with cross-compilation (for release job)
* Faster SQL query for /tags
