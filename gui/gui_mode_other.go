//go:build !windows

package gui

import (
	"os"

	"github.com/mattn/go-isatty"
)

type ConsoleKind int

func shouldStartGuiPlatform() bool {
	tty := isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) ||
		isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd()) ||
		isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	return !tty
}
