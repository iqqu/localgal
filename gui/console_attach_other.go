//go:build !windows
// +build !windows

package gui

// TryAttachParentConsole is a no-op on non-Windows platforms.
func TryAttachParentConsole() {}
