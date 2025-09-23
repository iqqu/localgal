package vars

import (
	"database/sql"
	"embed"
	"golocalgal/types"
	"html/template"
	"net/http"
)

// Global variables

var BuildInfo types.BuildInfo

var TemplatesFS embed.FS

var StaticFSHandler http.Handler

var Db *sql.DB

var Tpl *template.Template

var KnownFilePaths map[string][]string
var MediaRoot string
var DfLogRoot string
var SlowSqlMs int
