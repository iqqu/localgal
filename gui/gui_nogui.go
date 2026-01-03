//go:build !(gio || giu)

package gui

func shouldStartGuiPlatform() bool {
	return false
}

func Run() {}

func SetupLogPanel() {}
