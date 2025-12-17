package vars

import (
	"database/sql"
	"embed"
	"golocalgal/types"
	"html/template"
	"log"
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
func (key Env) SetValue(value string) {
	err := os.Setenv(string(key), value)
	if err != nil {
		// Usually only happens when the key is invalid or the value contains a null character
		// Not expected to be a problem
		log.Printf("Unable to set environment variable value; %v", err)
	}
}

func (key Env) Key() string {
	return string(key)
}

const (
	EnvBind      Env = "BIND"
	EnvSqliteDsn Env = "SQLITE_DSN"
	EnvSlowSqlMs Env = "SLOW_SQL_MS"
	EnvMediaRoot Env = "MEDIA_ROOT"
	EnvDflog     Env = "DFLOG"
	EnvDflogRoot Env = "DFLOG_ROOT"
	EnvGui       Env = "GUI"
)

// Global variables

var GuiFlag struct {
	IsSet bool
	Value bool
}

var BuildInfo types.BuildInfo

var TemplatesFS embed.FS

var StaticFSHandler http.Handler

var Db *sql.DB
var CacheDb *sql.DB

var Tpl *template.Template

var KnownFilePaths map[string][]string
var MediaRoot string
var DfLogRoot string
var SlowSqlMs int
