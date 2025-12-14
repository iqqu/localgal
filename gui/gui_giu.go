//go:build giu && !gio

package gui

import (
	"context"
	"fmt"
	"golocalgal/server"
	"golocalgal/vars"
	"log"
	"os"
	"strings"
	"time"

	"github.com/AllenDang/giu"
)

var mw *MainWindow
var quit = false

func Run() {
	mw = newMainWindow()
	log.Print("LocalGal Server Control GUI Ready")
	mw.wRoot.Run(loop)
}

type MainWindow struct {
	wRoot   *giu.MasterWindow
	wWidget *giu.WindowWidget
	cwd     string

	bind      string
	dsn       string
	slowSql   string
	mediaRoot string
	dflog     string
	dflogRoot string
	log       string

	status     string
	running    bool
	optimizing bool

	ctrl         *server.Controller
	lastLogLines []string
}

func newMainWindow() *MainWindow {
	mw := MainWindow{
		wRoot: giu.NewMasterWindow("Overview", 900, 650, 0),
		//wWidget: giu.SingleWindow(),
	}
	//mw.w.SetCloseCallback(func() bool {
	//	giu.
	//		giu.Quit()
	//	return true
	//})
	var err error
	mw.cwd, err = os.Getwd()
	if err != nil {
		mw.cwd = ""
	}

	return &mw
}

const statusIdle = "Idle"

func loop() {
	//if quit {
	//	mw.w.SetShouldClose(true)
	//}
	// Collect logs

	logs := getLastLogLines(30)
	lines := strings.Split(logs, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	// Build widgets for log lines without using cimgui-go
	var logWidgets []giu.Widget
	for i := range lines {
		logWidgets = append(logWidgets, giu.Label(lines[i]).Wrapped(true))
	}

	giu.SingleWindow().Layout(
		giu.Label("Configure and control the server"),
		giu.Separator(),
		giu.Label(vars.EnvBind.Key()),
		giu.InputText(&mw.bind),
		giu.Label("Server listen/bind address, e.g. :5037 or 127.0.0.1:5037").Wrapped(true),

		giu.Label(vars.EnvSqliteDsn.Key()),
		giu.InputText(&mw.dsn),
		giu.Label("SQLite data source name").Wrapped(true),

		giu.Label(vars.EnvSlowSqlMs.Key()),
		giu.InputText(&mw.slowSql),
		giu.Label("Log SQL queries slower than this many milliseconds, or -1 to disable").Wrapped(true),

		giu.Label(vars.EnvMediaRoot.Key()),
		giu.InputText(&mw.mediaRoot),
		giu.Label("Root directory for media files").Wrapped(true),

		giu.Label(vars.EnvDflog.Key()),
		giu.InputText(&mw.dflog),
		giu.Label("Downloaded file log").Wrapped(true),

		giu.Label(vars.EnvDflogRoot.Key()),
		giu.InputText(&mw.dflogRoot),
		giu.Label(fmt.Sprintf("Base directory to resolve relative paths in %s from", vars.EnvDflog.Key())).Wrapped(true),

		giu.Row(
			giu.Button("Start").OnClick(onStart).Disabled(mw.running || mw.optimizing),
			giu.Button("Stop").OnClick(onStop).Disabled(!mw.running || mw.optimizing),
			giu.Button("Optimize").OnClick(onOptimize).Disabled(mw.running || mw.optimizing),
		),
		giu.Label(mw.status),
		giu.Separator(),
		giu.Label("Recent Logs"),
		giu.Child().Size(-1, -1).Layout(
			logWidgets...,
		),
	)

}

func onStart() {
	vars.EnvBind.SetValue(mw.bind)
	vars.EnvSqliteDsn.SetValue(mw.dsn)
	vars.EnvSlowSqlMs.SetValue(mw.slowSql)
	vars.EnvMediaRoot.SetValue(mw.mediaRoot)
	vars.EnvDflog.SetValue(mw.dflog)
	vars.EnvDflogRoot.SetValue(mw.dflogRoot)

	cfg := server.GetServerConfig()
	ctrl, err := server.StartServer(cfg)
	if err != nil {
		mw.status = fmt.Sprintf("Failed to start: %v", err)
		log.Println(mw.status)
		giu.Update()
	} else {
		mw.ctrl = ctrl
		mw.running = true
		mw.status = "Starting..."
		giu.Update()
		go func() {
			select {
			case <-ctrl.Ready():
				mw.status = fmt.Sprintf("Running and listening on %s", cfg.Bind)
				giu.Update() // inside goroutine; force repaint
			case <-ctrl.Done(): // done before ready
				if err := ctrl.Err(); err != nil {
					mw.running = false
					giu.Update() // inside goroutine; force repaint
				}
				return
			}
			select {
			case <-ctrl.Done(): // done after ready
				if err := ctrl.Err(); err != nil {
					mw.running = false
					giu.Update() // inside goroutine; force repaint
				}
			}
		}()
	}

}

func onStop() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := mw.ctrl.Stop(ctx); err != nil {
		mw.status = fmt.Sprintf("Failed to stop: %v", err)
		log.Println(mw.status)
		giu.Update()
	} else {
		mw.status = "Stopped"
		log.Println(mw.status)
		giu.Update()
	}
	cancel()
}

func onOptimize() {
	vars.EnvSqliteDsn.SetValue(mw.dsn)

	mw.optimizing = true
	mw.status = "Optimize started"
	log.Println(mw.status)
	giu.Update()
	go func() {
		cfg := server.GetServerConfig()
		err := server.OptimizeDbFromDsn(cfg.Dsn)
		if err != nil {
			mw.status = fmt.Sprintf("Failed to optimize: %v", err)
			log.Println(mw.status)
		}
		mw.optimizing = false
		giu.Update() // in goroutine; force window repaint
	}()
}

// clamp x within [min, max]
func clamp(min, x, max int) int {
	if x < min {
		return min
	}
	if x > max {
		return max
	}
	return x
}
