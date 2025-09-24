//go:build windows

package gui

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ConsoleKind int

const (
	ConsoleNone       ConsoleKind = iota // no console attached
	ConsoleOwn                           // a fresh console created just for us (typical on double-click)
	ConsoleInherited                     // attached to an existing console (cmd/pwsh)
	ConsoleRedirected                    // stdio is redirected (pipe/file)
)

// shouldStartGuiPlatform side effect: free the console if not launched from a console
func shouldStartGuiPlatform() bool {
	switch detectConsoleKind() {
	case ConsoleInherited:
		return false
	case ConsoleRedirected:
		return false
	case ConsoleOwn, ConsoleNone:
		// not “really” in a console: hide/detach and launch GUI
		_ = freeConsole() // avoid flashing a console if Windows created one
		return true
	}
	return false
}

// kernel32 exported function wrappers
var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
)

func detectConsoleKind() ConsoleKind {
	stdout := stdHandle(windows.STD_OUTPUT_HANDLE)
	stdin := stdHandle(windows.STD_INPUT_HANDLE)

	outIsConsole := isConsole(stdout)
	inIsConsole := isConsole(stdin)

	if outIsConsole || inIsConsole {
		// We have a console. See if it's "only us" (fresh console) or inherited.
		n := consoleProcessCount()
		if n <= 1 {
			return ConsoleOwn
		}
		return ConsoleInherited
	}

	// Not a console screen buffer; check if redirected to pipe/disk
	outType := fileType(stdout)
	inType := fileType(stdin)
	if isRedirectionType(outType) || isRedirectionType(inType) {
		return ConsoleRedirected
	}

	// No console and not redirected → likely launched from GUI context
	return ConsoleNone
}

func stdHandle(kind uint32) windows.Handle {
	h, _ := windows.GetStdHandle(kind)
	return h
}

func isConsole(h windows.Handle) bool {
	if h == 0 || h == windows.InvalidHandle {
		return false
	}
	var mode uint32
	err := windows.GetConsoleMode(h, &mode)
	return err == nil
}

func fileType(h windows.Handle) uint32 {
	if h == 0 || h == windows.InvalidHandle {
		return 0
	}
	n, err := windows.GetFileType(h)
	if err != nil {
		return 0 // Hacky but correct-enough where used in this file
	}
	return n
}

func isRedirectionType(t uint32) bool {
	// FILE_TYPE_CHAR=0x0002 (console), FILE_TYPE_DISK=0x0001, FILE_TYPE_PIPE=0x0003
	return t == windows.FILE_TYPE_DISK || t == windows.FILE_TYPE_PIPE
}

func freeConsole() error {
	r1, _, e1 := procFreeConsole.Call()
	if r1 == 0 {
		return e1
	}
	return nil
}

func consoleProcessCount() uint32 {
	var pids [8]uint32
	r1, _, _ := procGetConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&pids[0])),
		uintptr(len(pids)),
	)
	return uint32(r1)
}
