# LocalGal

<img src="./static/localgal.min.svg" alt="LocalGal Icon" style="height:60px"/>

Local Gallery web server, written in Go. Companion to RipMe3 app. Preview version

"Browse your gals with pleasure!"

* Browse galleries of image and video files
* Completely self-contained; does not make external web requests
* Works with JS disabled
* If JS enabled:
  * Hotkeys enabled
  * Auto jump to content enabled

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
* `SQLITE_DSN`: sqlite data source name (connection string), default `file:ripme.sqlite?mode=ro&_query_only=1&_busy_timeout=10000&_foreign_keys=ON`
* `MEDIA_ROOT`: rip base directory, default: `./rips`
* `SLOW_SQL_MS`: duration threshold to log slow sql queries, milliseconds, default `100`

Goals:
* Be simple
* Be more convenient for browsing RipMe3 galleries than a file manager
* Perform well on large databases

Non-goals:
* User accounts

TODO:
* Refactor, clean up duplicate code
