package server

import (
	"bufio"
	"embed"
	"golocalgal/internal/types"
	"golocalgal/internal/vars"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Config holds configuration for starting the HTTP server.
type Config struct {
	Bind            string
	Dsn             string
	MediaRoot       string
	DfLog           string
	DfLogRoot       string
	ReadOnly        bool
	SlowSqlMs       int
	CorsOrigins     string
	BuildInfo       types.BuildInfo
	TemplatesFS     embed.FS
	StaticFSHandler http.Handler
}

const RipMeConfigFile = "rip.properties"

func GetRipMeConfigDir() string {
	if runtime.GOOS == "windows" {
		// RipMe uses %LOCALAPPDATA% instead of %APPDATA% on Windows...
		dir := os.Getenv("LOCALAPPDATA")
		d := filepath.Join(dir, "ripme")
		return d
	}
	// ignore extremely niche error - if a user complains then we'll ask how they want to handle it
	dir, _ := os.UserConfigDir()
	d := filepath.Join(dir, "ripme")
	return d
}

func GetDefaultConfigDir() (string, error) {
	// 1. Check current working directory
	// 2. Check RipMe config dir

	stat, err := os.Stat(RipMeConfigFile)
	if err == nil {
		cwd, err := os.Getwd()
		if err == nil && !stat.IsDir() {
			return cwd, nil
		}
	}

	ripMeConfigDir := GetRipMeConfigDir()
	configFile := filepath.Join(ripMeConfigDir, RipMeConfigFile)
	stat, err = os.Stat(configFile)
	if err == nil && !stat.IsDir() {
		return ripMeConfigDir, nil
	}
	return "", err
}

func parseRipMeConfig(dir string) (ripsDir string, dfLog string, dbPath string, err error) {
	dfLog = filepath.Join(dir, "ripme.downloaded.files.log") // Assume dflog is in config dir TODO make configurable in ripme?
	dbPath = filepath.Join(dir, "ripme.sqlite")              // Assume ripme.sqlite is in config dir
	ripsDir = "./rips"                                       // Assume ripsDir is relative to CWD

	// Check for configured rips.directory
	configPath := filepath.Join(dir, RipMeConfigFile)
	file, err := os.Open(configPath)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	key := "rips.directory"
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, key) {
			_, after, found := strings.Cut(line, "=")
			if found {
				ripsDir = strings.TrimSpace(after)
				break
			}
		}
	}
	return
}

var (
	buildInfo       types.BuildInfo
	templatesFS     embed.FS
	staticFSHandler http.Handler
)

func SetDefaultDeps(bi types.BuildInfo, tfs embed.FS, sfsh http.Handler) {
	buildInfo = bi
	templatesFS = tfs
	staticFSHandler = sfsh
}

func GetServerConfig() Config {
	dfLog := "./ripme.downloaded.files.log"
	sqlitePath := "./ripme.sqlite"
	ripsDir := "./rips"

	configDir, err := GetDefaultConfigDir()
	if err == nil {
		confRipsDir, confDfLog, confSqlitePath, err := parseRipMeConfig(configDir)
		if err == nil {
			ripsDir = confRipsDir
			dfLog = confDfLog
			sqlitePath = confSqlitePath
		}
	}

	slowSqlMs := 100
	if v := vars.EnvSlowSqlMs.GetValue(); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n >= -1 {
			slowSqlMs = n
		}
	}

	dfLog = vars.EnvDflog.GetValueDefault(dfLog)
	defaultDfLogRoot := getDefaultDfLogRoot(dfLog)
	dfLogRoot := vars.EnvDflogRoot.GetValueDefault(defaultDfLogRoot)

	ro := shouldRunReadOnly()

	serverConfig := Config{
		Bind:            vars.EnvBind.GetValueDefault("127.0.0.1:5033"),
		Dsn:             vars.EnvSqliteDsn.GetValueDefault("file:" + sqlitePath),
		MediaRoot:       vars.EnvMediaRoot.GetValueDefault(ripsDir),
		DfLog:           dfLog,
		DfLogRoot:       dfLogRoot,
		ReadOnly:        ro,
		SlowSqlMs:       slowSqlMs,
		CorsOrigins:     vars.EnvCorsOrigins.GetValueDefault(""),
		BuildInfo:       buildInfo,
		TemplatesFS:     templatesFS,
		StaticFSHandler: staticFSHandler,
	}
	return serverConfig
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

func shouldRunReadOnly() bool {
	// Get from CLI flags first
	if vars.RoFlag.IsSet {
		return vars.RoFlag.Value
	}
	// Get from environment second
	v := vars.EnvRo.GetValue()
	switch v {
	case "1", "true", "yes", "ro":
		return true
	case "0", "false", "no", "rw":
		return false
	}
	return false // default
}
