package vars

import (
	"database/sql"
	"embed"
	"golocalgal/types"
	"html/template"
	"net/http"
	"os"
)

type Env string

func (key Env) GetValue() string {
	return os.Getenv(string(key))
}
func (key Env) GetValueDefault(def string) string {
	v := os.Getenv(string(key))
	if v == "" {
		return def
	}
	return v
}

const (
	EnvBind      Env = "BIND"
	EnvSqliteDsn Env = "SQLITE_DSN"
	EnvSlowSqlMs Env = "SLOW_SQL_MS"
	EnvMediaRoot Env = "MEDIA_ROOT"
	EnvDflog     Env = "DFLOG"
	EnvDflogRoot Env = "DFLOG_ROOT"
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
