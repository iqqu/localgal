package server

import (
	"bufio"
	"database/sql"
	"fmt"
	"golocalgal/vars"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/mem"

	_ "github.com/mattn/go-sqlite3"
)

func GetDb(cfg Config) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", cfg.Dsn)
	if err != nil {
		log.Printf("open db: %v", err)
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite preferred in many cases
	return db, nil
}

func initDB(db *sql.DB, filename string) error {
	var pragmas []string

	// Not sure why anyone would try :memory:, but handle it anyway
	if ":memory:" != filename {
		stat, err := os.Stat(filename)
		if err != nil {
			return err
		}
		vm, _ := mem.VirtualMemory()
		statKib := max(0, stat.Size()/1024)
		availableKib := vm.Available / 1024
		maxCacheKib := min(uint64(statKib), availableKib, 2097152)
		pragmas = append(pragmas, fmt.Sprintf("PRAGMA cache_size=%d", maxCacheKib))
		//pragmas = append(pragmas, "PRAGMA cache_size=2097152;")  // kibibytes; 2GiB
	}

	// Set pragmas for large DBs
	//pragmas = append(pragmas, "PRAGMA journal_mode=WAL;")
	//pragmas = append(pragmas, "PRAGMA synchronous=NORMAL;")
	pragmas = append(pragmas, "PRAGMA temp_store=MEMORY;")
	//pragmas = append(pragmas, "PRAGMA mmap_size=268435456;") // 256MB

	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			log.Printf("pragma error %q: %v", p, err)
		}
	}
	return nil
}

func ForceReadOnlyDsn(dsn string) string {
	base, query, _ := strings.Cut(dsn, "?")
	params := strings.Split(query, "&")
	if !slices.Contains(params, "mode=ro") {
		params = append(params, "mode=ro")
	}
	if !slices.Contains(params, "_query_only=1") {
		params = append(params, "_query_only=1")
	}
	return base + "?" + strings.Join(params, "&")
}

func ForceReadWriteDsn(dsn string) string {
	base, query, _ := strings.Cut(dsn, "?")
	params := strings.Split(query, "&")
	newParams := make([]string, len(params))
	excluded := []string{"mode=ro", "_query_only=1"}
	for _, param := range params {
		if !slices.Contains(excluded, param) {
			newParams = append(newParams, param)
		}
	}
	return base + "?" + strings.Join(newParams, "&")
}

func ForceForeignKeysDsn(dsn string) string {
	base, query, _ := strings.Cut(dsn, "?")
	params := strings.Split(query, "&")
	if !slices.Contains(params, "_foreign_keys=ON") {
		params = append(params, "_foreign_keys=ON")
	}
	return base + "?" + strings.Join(params, "&")
}

func getFileFromDsn(dsn string) string {
	_, afterFile, _ := strings.Cut(dsn, "file:")
	filename, _, _ := strings.Cut(afterFile, "?")
	return filename
}

func OptimizeDb(db *sql.DB) error {
	start := time.Now()
	log.Printf("db optimize started")
	_, err := db.Exec("PRAGMA optimize")
	elapsed := time.Since(start)
	log.Printf("db optimize took: %v\n", elapsed)
	if err != nil {
		log.Printf("db optimize failed: %v", err)
		return err
	}
	log.Printf("db optimize succeeded")
	return nil
}

// loadKnownFiles builds knownFilePaths from a log file: each line is a relative or absolute path to a file.
func loadKnownFiles(path string) {
	log.Printf("Loading known files")
	vars.KnownFilePaths = map[string][]string{}
	dir := filepath.Dir(path)
	f, err := os.Open(path)
	if err != nil {
		log.Printf("known file log open: %v", err)
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		p := strings.TrimSpace(s.Text())
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		base := filepath.Base(p)
		target := filepath.Join(dir, p)
		if !filepath.IsAbs(target) {
			target = filepath.Join(vars.DfLogRoot, target)
			target = filepath.Clean(target)
			target, err = filepath.Rel(vars.DfLogRoot, target)
			if err != nil {
				log.Printf("Not able to resolve clean relative path for %v: %v", p, err)
			}
		}
		vars.KnownFilePaths[base] = append(vars.KnownFilePaths[base], target)
	}
	if err := s.Err(); err != nil {
		log.Printf("known file log scan: %v", err)
	}
	log.Printf("known file log loaded %d filenames", len(vars.KnownFilePaths))
}
