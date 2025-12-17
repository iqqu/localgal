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

## Screenshots

[Screenshots on wiki](https://github.com/iqqu/localgal/wiki/Screenshots)

## Getting Started

1. Rip a gallery with [RipMe3](https://github.com/RipMeApp3/ripme)
   * After the rip completes, new files will be created: `ripme.sqlite` and `ripme.downloaded.files.log`. LocalGal reads those files.
2. Download the localgal executable from the release page to the folder containing `ripme.sqlite` and `ripme.downloaded.files.log`
3. Run the localgal executable that you downloaded.
   * Double-click the executable to show the Server Control GUI and click the Start button, or
   * Run the executable in a terminal to start the server without the GUI
4. Open `http://127.0.0.1:5037` in your web browser

For notes on compiling from source, see [docs/developing.md](./docs/developing.md)

## Hotkeys
* f: random file
* g: random gallery
* h or Arrow Left: previous item
* i: toggle fullscreen image
* j: jump to content
* k: jump to top
* l or Arrow Right: next item
* Shift+j: Toggle autojump
* Shift+p: Toggle autoplay
* Ctrl+/: Focus search box

## Pages
* `/`: Browse galleries
* `/gallery/{ripper}/{gid}`: View gallery
* `/gallery/{ripper}/{gid}/{fileid}`: View file of gallery
* `/file/{ripper}/{fileid}`: View individual file
* `/tags`: View all tags
* `/tag/{tag}`: View tag
* `/search`: Search result summary
* `/search/galleries`: Search galleries
* `/search/files`: Search files
* `/search/tags`: Search tags
* `/random/gallery`: Redirect to random gallery
* `/random/file`: Redirect to random file
* `/random/page`: Redirect to random page within the currently viewed page set
* `/media/`: Direct file links
* `/about`: About page
* `/healthz`

### JSON API
In case somebody wants to develop a different UI.  
(accepts the same query parameters used by the HTML pages)
* `/api/galleries`: Browse galleries
* `/api/gallery/{ripper}/{gid}`: View gallery
* `/api/gallery/{ripper}/{gid}/{fileid}`: View file of gallery
* `/api/file/{ripper}/{fileid}`: View individual file
* `/api/file/{ripper}/{fileid}/galleries`: View galleries associated with an individual file
* `/api/tags`: View all tags
* `/api/tag/{tag}`: View tag
* `/api/search`: Search result summary
* `/api/search/galleries`: Search galleries
* `/api/search/files`: Search files
* `/api/search/tags`: Search tags
* `/api/random/gallery`: Redirect to random gallery
* `/api/random/file`: Redirect to random file

Note: there is no `/api/random/page` for now, because that endpoint doesn't work nicely for JSON APIs.

## Environment variables
* `BIND`: listen address, default `127.0.0.1:5037` (to listen on all addresses, specify `:5037`)
* `SQLITE_DSN`: sqlite data source name (connection string), default `file:ripme.sqlite`
* `SLOW_SQL_MS`: duration threshold to log slow sql queries, milliseconds, default `100`
* `MEDIA_ROOT`: rip base directory, default: `./rips`
* `DFLOG`: downloaded file log, default `./ripme.downloaded.files.log`
* `DFLOG_ROOT`: base directory to resolve relative paths in DFLOG from, default directory that DFLOG is in
* `GUI`: force GUI mode with `1` or CLI mode with `0`

## Notes
* If queries take abnormally long, click the "Optimize" button in the Server Control GUI, or run `localgal --optimize`. The command could take some minutes when optimization is needed on large databases, so do not run it while the database is being actively used.
  * Alternatively, manually execute `PRAGMA optimize;` on the database

## Goals
* Be simple
* Be more convenient for browsing RipMe3 galleries than a file manager
* Perform well on large databases

## Non-goals
* User accounts

## TODO
* Simplify Server Control GUI layout code
* Show file dimensions (width/height/duration) if available
* Distinguish local user-defined tags from remote tags
* Reduce duplicated error handling code
* ???

## Wishlist (help welcome)
* Fix GitHub pipeline compilation for amd64 mac
* Better icon
* Faster SQL queries (tip: <https://sqlite.org/cli.html#index_recommendations_sqlite_expert_>)
* Use cancelable PRAGMA optimize (or is this automatically handled by the sqlite driver?)
* More sorting options
  * I think I've pushed sorting as far as it can go without materializing tables
* Read-write functionality:
  * Ignored files
  * Local user-defined tags
  * Local user ratings (1=worst, 5=best)
    <table> <tr> <td>&#128169;</td> <td>&#128078;</td> <td>&#11093;</td> <td>&#128077;</td> <td>&#10084;&#65039;</td> </tr> </table>
