package tui

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// copyToClipboard sets the clipboard with the first available local tool
// (wl-copy, xclip, xsel, pbcopy). It also sends OSC 52 through the terminal,
// which reaches the local clipboard over SSH in terminals that support it.
func copyToClipboard(screen tcell.Screen, text string) error {
	if screen != nil {
		screen.SetClipboard([]byte(text))
	}
	var tools [][]string
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		tools = append(tools, []string{"wl-copy"})
	}
	if os.Getenv("DISPLAY") != "" {
		tools = append(tools, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	tools = append(tools, []string{"pbcopy"})
	for _, tool := range tools {
		if _, err := exec.LookPath(tool[0]); err != nil {
			continue
		}
		cmd := exec.Command(tool[0], tool[1:]...)
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
	if screen == nil {
		return errors.New("no clipboard tool found")
	}
	return nil
}
