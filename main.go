package main

import (
	"embed"
	"flag"
	"fmt"
	"golocalgal/server"
	"golocalgal/types"
	"golocalgal/vars"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// Version metadata populated via -ldflags at build time
var (
	Version   = "dev"
	Commit    = ""
	BuildDate = ""
)

//go:embed templates/*
var TemplatesFS embed.FS

//go:embed static/*
var StaticFS embed.FS

// The Go runtime calls init() before main().
//   - Putting the version vars in a non-main package requires ldflags to fully qualify the package
//   - Embedding doesn't support ../, and I don't want another top-level go file,
//     so we leave the declarations here and put them into the vars package in init().
func init() {
	vars.StaticFSHandler = http.FileServerFS(StaticFS)
	vars.TemplatesFS = TemplatesFS
	vars.BuildInfo = types.BuildInfo{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
	}
}

func main() {
	var help bool
	flag.BoolVar(&help, "h", false, "show help")
	flag.BoolVar(&help, "help", false, "show help")
	var optimize bool
	flag.BoolVar(&optimize, "optimize", false, "optimize sqlite database (may be very slow)")
	flag.Parse()
	if help {
		flag.CommandLine.SetOutput(os.Stdout)
		fmt.Println("Usage: localgal [options]")
		fmt.Println("Options:")
		flag.PrintDefaults()
		fmt.Println("Environment Variables:")
		fmt.Println("  BIND:\tlisten address, default `127.0.0.1:5037` (to listen on all addresses, specify `:5037`)")
		fmt.Println("  SQLITE_DSN:\tsqlite data source name (connection string), default `file:ripme.sqlite`")
		fmt.Println("  MEDIA_ROOT:\trip base directory, default: `./rips`")
		fmt.Println("  SLOW_SQL_MS:\tduration threshold to log slow sql queries, milliseconds, default `100`")
		fmt.Println("  DFLOG:\tdownloaded file log, default `./ripme.downloaded.files.log`")
		fmt.Println("  DFLOG_ROOT:\tbase directory to resolve relative paths in DFLOG from, default directory that DFLOG is in")
		os.Exit(0)
	}


	dsn := getEnv("SQLITE_DSN", "file:ripme.sqlite?_busy_timeout=10000")
	dsn = server.ForceForeignKeysDsn(dsn)
	dsnReadOnly := server.ForceReadOnlyDsn(dsn)

	slowSqlMs := 100
	if v := os.Getenv("SLOW_SQL_MS"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n >= -1 {
			slowSqlMs = n
		}
	}

	dfLog := getEnv("DFLOG", "./ripme.downloaded.files.log")
	defaultDfLogRoot := getDefaultDfLogRoot(dfLog)
	dfLogRoot := getEnv("DFLOG_ROOT", defaultDfLogRoot)

	serverConfig := server.Config{
		Bind:      getEnv("BIND", "127.0.0.1:5037"),
		Dsn:       dsnReadOnly,
		MediaRoot: getEnv("MEDIA_ROOT", "./rips"),
		DfLog:     dfLog,
		DfLogRoot: dfLogRoot,
		SlowSqlMs: slowSqlMs,
	}

	if optimize {
		serverConfig.Dsn = dsn
		db, err := server.GetDb(serverConfig)
		if err != nil {
			log.Fatal(err)
		}
		err = server.OptimizeDb(db)
		if err != nil {
			os.Exit(1)
			return
		}
		os.Exit(0)
		return
	}

	ctrl, err := server.StartServer(serverConfig)
	if err != nil {
		log.Fatal(err)
	}

	<-ctrl.Done()
	if err := ctrl.Err(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getDefaultDfLogRoot(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(filepath.Dir(path))
	}
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal("Unable to get cwd: %w", err)
	}
	return filepath.Clean(filepath.Dir(filepath.Join(wd, path)))
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
