package gui

import (
	"context"
	"fmt"
	"golocalgal/server"
	"golocalgal/vars"
	"image/color"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

func ShouldStartGui() bool {
	// Get from CLI flags first
	if vars.GuiFlag.IsSet {
		return vars.GuiFlag.Value
	}
	// Get from environment second
	v := vars.EnvGui.GetValue()
	switch v {
	case "1", "true", "yes", "gui":
		return true
	case "0", "false", "no", "cli":
		return false
	}
	return shouldStartGuiPlatform()
}

func Run() {
	go guiMain()
	app.Main()
}

type MainWindow struct {
	w            *app.Window
	ops          *op.Ops
	th           *material.Theme
	cwd          string
	bindEd       widget.Editor
	dsnEd        widget.Editor
	slowSqlEd    widget.Editor
	mediaRootEd  widget.Editor
	dflogEd      widget.Editor
	dflogRootEd  widget.Editor
	logEd        widget.Editor
	logList      widget.List
	startBtn     widget.Clickable
	stopBtn      widget.Clickable
	optimizeBtn  widget.Clickable
	status       string
	running      bool
	optimizing   bool
	ctrl         *server.Controller
	lastLogLines []string
}

func newMainWindow() *MainWindow {
	mw := MainWindow{
		w:      &app.Window{},
		ops:    &op.Ops{},
		th:     material.NewTheme(),
		status: statusIdle,
	}
	mw.w.Option(
		app.Title("LocalGal Server"),
		app.Size(unit.Dp(500), unit.Dp(700)),
		app.MinSize(unit.Dp(500), unit.Dp(700)),
	)

	mw.bindEd.SingleLine = true
	mw.dsnEd.SingleLine = true
	mw.slowSqlEd.SingleLine = true
	mw.mediaRootEd.SingleLine = true
	mw.dflogEd.SingleLine = true
	mw.dflogRootEd.SingleLine = true
	mw.logEd.SingleLine = false
	mw.logEd.Submit = false
	mw.logEd.ReadOnly = true
	mw.logList.Axis = layout.Vertical
	mw.logList.ScrollToEnd = true

	serverConfig := server.GetServerConfig()

	mw.bindEd.SetText(serverConfig.Bind)
	mw.dsnEd.SetText(serverConfig.Dsn)
	mw.slowSqlEd.SetText(strconv.Itoa(serverConfig.SlowSqlMs))
	mw.mediaRootEd.SetText(serverConfig.MediaRoot)
	mw.dflogEd.SetText(serverConfig.DfLog)
	mw.dflogRootEd.SetText(serverConfig.DfLogRoot)

	var err error
	mw.cwd, err = os.Getwd()
	if err != nil {
		mw.cwd = ""
	}

	return &mw
}

const statusIdle = "Idle"

func guiMain() {
	mw := newMainWindow()

	for {
		e := mw.w.Event()
		handleEvent(e, mw)
	}
}

func handleEvent(e event.Event, mw *MainWindow) {
	switch ev := e.(type) {
	case app.DestroyEvent:
		if mw.ctrl != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = mw.ctrl.Stop(ctx)
			cancel()
		}
		os.Exit(0)
	case app.FrameEvent:
		gtx := app.NewContext(mw.ops, ev)

		if mw.startBtn.Clicked(gtx) && !mw.running && !mw.optimizing {
			vars.EnvBind.SetValue(mw.bindEd.Text())
			vars.EnvSqliteDsn.SetValue(mw.dsnEd.Text())
			vars.EnvSlowSqlMs.SetValue(mw.slowSqlEd.Text())
			vars.EnvMediaRoot.SetValue(mw.mediaRootEd.Text())
			vars.EnvDflog.SetValue(mw.dflogEd.Text())
			vars.EnvDflogRoot.SetValue(mw.dflogRootEd.Text())

			cfg := server.GetServerConfig()
			ctrl, err := server.StartServer(cfg)
			if err != nil {
				mw.status = fmt.Sprintf("Failed to start: %v", err)
				log.Println(mw.status)
			} else {
				mw.ctrl = ctrl
				mw.running = true
				mw.status = "Starting..."
				go func() {
					select {
					case <-ctrl.Ready():
						mw.status = fmt.Sprintf("Running and listening on %s", cfg.Bind)
						mw.w.Invalidate() // inside goroutine; force repaint
					case <-ctrl.Done(): // done before ready
						if err := ctrl.Err(); err != nil {
							mw.running = false
							mw.w.Invalidate() // inside goroutine; force repaint
						}
						return
					}
					select {
					case <-ctrl.Done(): // done after ready
						if err := ctrl.Err(); err != nil {
							mw.running = false
							mw.w.Invalidate() // inside goroutine; force repaint
						}
					}
				}()
			}
		}
		if mw.stopBtn.Clicked(gtx) && mw.running && !mw.optimizing {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := mw.ctrl.Stop(ctx); err != nil {
				mw.status = fmt.Sprintf("Failed to stop: %v", err)
				log.Println(mw.status)
			} else {
				mw.status = "Stopped"
				log.Println(mw.status)
			}
			cancel()
		}
		if mw.optimizeBtn.Clicked(gtx) && !mw.running && !mw.optimizing {
			vars.EnvSqliteDsn.SetValue(mw.dsnEd.Text())

			mw.optimizing = true
			mw.status = "Optimize started"
			log.Println(mw.status)
			go func() {
				cfg := server.GetServerConfig()
				err := server.OptimizeDbFromDsn(cfg.Dsn)
				if err != nil {
					mw.status = fmt.Sprintf("Failed to optimize: %v", err)
					log.Println(mw.status)
				}
				mw.optimizing = false
				mw.w.Invalidate() // in goroutine; force window repaint
			}()
		}
		if !mw.running && !mw.optimizing {
			mw.status = statusIdle
		}
		readOnly := mw.running || mw.optimizing
		mw.bindEd.ReadOnly = readOnly
		mw.dsnEd.ReadOnly = readOnly
		mw.slowSqlEd.ReadOnly = readOnly
		mw.mediaRootEd.ReadOnly = readOnly
		mw.dflogEd.ReadOnly = readOnly
		mw.dflogRootEd.ReadOnly = readOnly

		// Update log view content each frame
		newLogLines := globalLogBuffer.last(100)
		logLinesAreSame := len(newLogLines) == len(mw.lastLogLines)
		if logLinesAreSame {
			for i := range newLogLines {
				if newLogLines[i] != mw.lastLogLines[i] {
					logLinesAreSame = false
					break
				}
			}
		}

		// Find the first shared log line
		runeStartOffset := 0
		if !logLinesAreSame {
			for i := range mw.lastLogLines {
				if mw.lastLogLines[i] != newLogLines[0] {
					runeStartOffset += utf8.RuneCountInString(mw.lastLogLines[i]) + 1 // +1 for \n
				} else {
					break
				}
			}
		}

		// Preserve as much of the selection/caret as possible across text refreshes
		if !logLinesAreSame {
			startOld, endOld := mw.logEd.Selection()

			newLogText := strings.Join(newLogLines, "\n")
			mw.logEd.SetText(newLogText)

			newRuneLen := utf8.RuneCountInString(newLogText)
			// Map the old offsets onto new offsets
			startNew, endNew := startOld-runeStartOffset, endOld-runeStartOffset
			startNew, endNew = clamp(0, startNew, newRuneLen), clamp(0, endNew, newRuneLen)

			// Commit the new text
			mw.logEd.SetCaret(startNew, endNew)
			mw.lastLogLines = newLogLines
		}

		layout.Inset{Top: unit.Dp(8), Right: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween, Alignment: layout.Start}.Layout(gtx,
						layout.Rigid(material.H6(mw.th, "LocalGal").Layout),
						layout.Flexed(1, layout.Spacer{Width: unit.Dp(2)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical, Spacing: layout.SpaceAround, Alignment: layout.End}.Layout(gtx,
								layout.Rigid(material.Body2(mw.th, "Server Control GUI").Layout),
								layout.Rigid(material.Caption(mw.th, fmt.Sprintf("%s", vars.BuildInfo.Version)).Layout),
							)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if mw.cwd != "" {
						return material.Body2(mw.th, fmt.Sprintf("Paths are relative to:\n%s", mw.cwd)).Layout(gtx)
					}
					return layout.Dimensions{}
				}),
				layout.Rigid(labeledEditor(mw.th, vars.EnvBind.Key(), &mw.bindEd, "Server listen/bind address, e.g. :5037 or 127.0.0.1:5037")),
				layout.Rigid(labeledEditor(mw.th, vars.EnvSqliteDsn.Key(), &mw.dsnEd, "SQLite data source name")),
				layout.Rigid(labeledEditor(mw.th, vars.EnvSlowSqlMs.Key(), &mw.slowSqlEd, "Duration threshold to log slow sql queries, milliseconds, or -1 to disable")),
				layout.Rigid(labeledEditor(mw.th, vars.EnvMediaRoot.Key(), &mw.mediaRootEd, "Root directory for media files")),
				layout.Rigid(labeledEditor(mw.th, vars.EnvDflog.Key(), &mw.dflogEd, "Downloaded file log")),
				layout.Rigid(labeledEditor(mw.th, vars.EnvDflogRoot.Key(), &mw.dflogRootEd, fmt.Sprintf("Base directory to resolve relative paths in %s from", vars.EnvDflog.Key()))),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					// Buttons
					in := layout.Inset{Top: unit.Dp(6)}
					return in.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								btn := material.Button(mw.th, &mw.startBtn, "Start")
								if mw.running || mw.optimizing {
									// Dim the button when running or optimizing
									c := btn.Background
									btn.Background = color.NRGBA{R: c.R, G: c.G, B: c.B, A: 100}
								}
								return btn.Layout(gtx)
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								btn := material.Button(mw.th, &mw.stopBtn, "Stop")
								if !mw.running || mw.optimizing {
									c := btn.Background
									btn.Background = color.NRGBA{R: c.R, G: c.G, B: c.B, A: 100}
								}
								return btn.Layout(gtx)
							}),
							layout.Flexed(1, layout.Spacer{Width: unit.Dp(8)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								ins := layout.Inset{Top: unit.Dp(2), Right: unit.Dp(4), Bottom: unit.Dp(2), Left: unit.Dp(4)}
								return ins.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle, Spacing: layout.SpaceAround}.Layout(gtx,
										layout.Rigid(material.Caption(mw.th, "Optimize may take some minutes").Layout),
										layout.Rigid(material.Caption(mw.th, "on large (~1GiB) databases").Layout),
									)
								})
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								btn := material.Button(mw.th, &mw.optimizeBtn, "Optimize DB")
								btn.Background = color.NRGBA{R: 0xff, G: 0x20, B: 0x20, A: 0xff}
								if mw.running || mw.optimizing {
									c := btn.Background
									btn.Background = color.NRGBA{R: c.R, G: c.G, B: c.B, A: 100}
								}
								return btn.Layout(gtx)
							}),
						)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					in := layout.Inset{Top: unit.Dp(6)}
					return in.Layout(gtx, material.Body2(mw.th, mw.status).Layout)
				}),
				layout.Rigid(material.Body1(mw.th, "Recent logs").Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					// Log area
					//lines := strings.Split(mw.lastLogText, "\n")
					//for len(lines) > 0 && lines[len(lines)-1] == "" {
					//	lines = lines[:len(lines)-1]
					//}
					border := widget.Border{Color: color.NRGBA{R: 0xc8, G: 0xc8, B: 0xc8, A: 0xff}, Width: unit.Dp(1), CornerRadius: unit.Dp(4)}
					return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						in := layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4), Left: unit.Dp(6), Right: unit.Dp(0)}
						return in.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									lst := material.List(mw.th, &mw.logList)
									//return lst.Layout(gtx, len(lines), func(gtx layout.Context, i int) layout.Dimensions {
									//	lab := material.Label(mw.th, unit.Sp(12), lines[i])
									//	return lab.Layout(gtx)
									//})
									return lst.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
										editor := material.Editor(mw.th, &mw.logEd, "")
										editor.TextSize = unit.Sp(12)
										return editor.Layout(gtx)
									})
								}),
							)
						})
					})
				}),
			)
		})
		ev.Frame(gtx.Ops)
	}
}

func labeledEditor(theme *material.Theme, label string, ed *widget.Editor, help string) func(gtx layout.Context) layout.Dimensions {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Body1(theme, label)
				return lbl.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				in := layout.Inset{Top: unit.Dp(1), Bottom: unit.Dp(1)}
				return in.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					border := widget.Border{Color: color.NRGBA{R: 0xc8, G: 0xc8, B: 0xc8, A: 0xff}, Width: unit.Dp(1), CornerRadius: unit.Dp(4)}
					pad := layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6)}
					return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return pad.Layout(gtx, material.Editor(theme, ed, "").Layout)
					})
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				hel := material.Caption(theme, help)
				col := theme.Palette.Fg
				col.A = 0xee
				hel.Color = col
				in := layout.Inset{Bottom: unit.Dp(2)}
				return in.Layout(gtx, hel.Layout)
			}),
		)
	}
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
