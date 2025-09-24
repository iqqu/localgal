//go:build windows
// +build windows

package gui

import (
	"log"
	"os"
	"syscall"
)

var (
	modkernel32       = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole = modkernel32.NewProc("AttachConsole")
	procSetStdHandle  = modkernel32.NewProc("SetStdHandle")
)

const (
	// Windows standard handle constants (negative values cast to uintptr)
	STD_INPUT_HANDLE  = ^uintptr(9)  // -10
	STD_OUTPUT_HANDLE = ^uintptr(10) // -11
	STD_ERROR_HANDLE  = ^uintptr(11) // -12
)

// TryAttachParentConsole tries to attach the process to the parent console, if any.
// If successful, it rebinds os.Stdout/os.Stderr/os.Stdin to the console handles so
// prints and logs go to the launching terminal. On failure or when no parent console
// exists, it silently does nothing.
func TryAttachParentConsole() {
	// ATTACH_PARENT_PROCESS is defined as (DWORD)-1
	const ATTACH_PARENT_PROCESS = ^uint32(0)

	if r, _, err := procAttachConsole.Call(uintptr(ATTACH_PARENT_PROCESS)); r != 0 {
		// Reopen CONOUT$ for stdout/stderr and update OS-level std handles
		if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stdout = f
			os.Stderr = f
			log.SetOutput(f)
			// Update the process standard handles so any Windows API users write to console
			_, _, _ = procSetStdHandle.Call(uintptr(STD_OUTPUT_HANDLE), f.Fd())
			_, _, _ = procSetStdHandle.Call(uintptr(STD_ERROR_HANDLE), f.Fd())
		}
		// Reopen CONIN$ for stdin and update OS-level std handle
		if f, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
			os.Stdin = f
			_, _, _ = procSetStdHandle.Call(uintptr(STD_INPUT_HANDLE), f.Fd())
		}
	} else {
		_ = err // ignore when attach fails or there's no parent console
	}
}
