package main

import (
	"context"
	"flag"
	"fmt"
	"golocalgal/internal/gui"
	"golocalgal/internal/server"
	"golocalgal/internal/types"
	"golocalgal/internal/vars"
	"golocalgal/web"
	"log"
	"net/http"
	"os"
)

// - Putting the version vars in a non-main package requires ldflags to fully qualify the package

// Version metadata populated via -ldflags at build time
var (
	Version   = "dev"
	Commit    = ""
	BuildDate = ""
)

func main() {
	buildInfo := types.BuildInfo{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
	}
	staticFSHandler := http.FileServerFS(web.StaticFS)
	server.SetDefaultDeps(buildInfo, web.TemplatesFS, staticFSHandler)

	var help bool
	flag.BoolVar(&help, "h", false, "show help")
	flag.BoolVar(&help, "help", false, "show help")

	var optimize bool
	flag.BoolVar(&optimize, "optimize", false, "optimize sqlite database (may be very slow)")

	flag.BoolFunc("gui", "run with the gui", func(_ string) error {
		vars.GuiFlag.IsSet = true
		vars.GuiFlag.Value = true
		return nil
	})
	flag.BoolFunc("cli", "run with the cli / without the gui", func(_ string) error {
		vars.GuiFlag.IsSet = true
		vars.GuiFlag.Value = false
		return nil
	})

	flag.BoolFunc("ro", "read-only mode", func(_ string) error {
		vars.RoFlag.IsSet = true
		vars.RoFlag.Value = true
		return nil
	})
	flag.BoolFunc("rw", "read-write mode", func(_ string) error {
		vars.RoFlag.IsSet = true
		vars.RoFlag.Value = false
		return nil
	})

	flag.Parse()
	if help {
		flag.CommandLine.SetOutput(os.Stdout)
		fmt.Println("Usage: localgal [options]")
		fmt.Println("Options:")
		flag.PrintDefaults()
		fmt.Println("Environment Variables:")
		fmt.Println("  BIND:\tlisten address, default `127.0.0.1:5037` (to listen on all addresses, specify `:5037`)")
		fmt.Println("  SQLITE_DSN:\tsqlite data source name (connection string), default `file:ripme.sqlite`")
		fmt.Println("  SLOW_SQL_MS:\tduration threshold to log slow sql queries, milliseconds, default `100`")
		fmt.Println("  MEDIA_ROOT:\trip base directory, default: `./rips`")
		fmt.Println("  DFLOG:\tdownloaded file log, default `./ripme.downloaded.files.log`")
		fmt.Println("  DFLOG_ROOT:\tbase directory to resolve relative paths in DFLOG from, default directory that DFLOG is in")
		fmt.Println("  GUI:\tforce GUI mode with `1` or CLI mode with `0`. flag takes precedence")
		fmt.Println("  RO:\tif `1`, run in read-only mode (no saved ratings). `0` is read-write mode. flag takes precedence")
		fmt.Println("Notes:")
		fmt.Println("  If stdin, stdout, and stderr are not a tty, GUI mode gets chosen by default. In containers, use GUI=0 or -cli")
		fmt.Println("  If environment variables are not specified, localgal looks for the ripme configuration file")
		os.Exit(0)
	}

	if gui.ShouldStartGui() {
		gui.SetupLogPanel()
		gui.Run()
		return
	}

	serverConfig := server.GetServerConfig()

	if optimize {
		err := server.OptimizeDbFromDsn(context.Background(), serverConfig.Dsn)
		if err != nil {
			log.Printf("Unable to optimize db: %v", err)
			os.Exit(1)
			return
		}
		log.Println("Successfully optimized db")
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
