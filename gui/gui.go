package gui

import (
	"golocalgal/vars"
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
