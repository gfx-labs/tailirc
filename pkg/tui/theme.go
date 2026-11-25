package tui

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Theme holds the tview color tags the UI writes. Each is a color name or
// #rrggbb, usable as "[" + value + "]".
type Theme struct {
	Foreground string
	Background string
	Dim        string // timestamps and status lines (color8)
	Self       string // own nick (color3)
	Other      string // other nicks (color2)
}

var defaultTheme = Theme{Dim: "gray", Self: "yellow", Other: "green"}

// loadTheme reads colors from X resources: `xrdb -query` when an X server is
// reachable, else ~/.Xresources or ~/.Xdefaults. Missing entries keep the
// terminal's own colors.
func loadTheme() Theme {
	res := xresources()
	t := defaultTheme
	pick := func(dst *string, names ...string) {
		for _, n := range names {
			if v, ok := res[n]; ok {
				*dst = v
				return
			}
		}
	}
	pick(&t.Foreground, "foreground")
	pick(&t.Background, "background")
	pick(&t.Dim, "color8")
	pick(&t.Self, "color3")
	pick(&t.Other, "color2")
	return t
}

// apply sets tview's base styles so widgets use the theme.
func (t Theme) apply() {
	if t.Background != "" {
		bg := tcell.GetColor(t.Background)
		tview.Styles.PrimitiveBackgroundColor = bg
		tview.Styles.ContrastBackgroundColor = bg
	}
	if t.Foreground != "" {
		fg := tcell.GetColor(t.Foreground)
		tview.Styles.PrimaryTextColor = fg
		tview.Styles.SecondaryTextColor = fg
	}
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// xresources returns color resources keyed by the last name component
// ("color2", "foreground"), keeping only #rrggbb values. Class-specific
// entries such as "URxvt.color2" are used only when no generic one exists.
func xresources() map[string]string {
	var data []byte
	if os.Getenv("DISPLAY") != "" {
		if out, err := exec.Command("xrdb", "-query").Output(); err == nil {
			data = out
		}
	}
	if data == nil {
		home, _ := os.UserHomeDir()
		for _, name := range []string{".Xresources", ".Xdefaults"} {
			if b, err := os.ReadFile(filepath.Join(home, name)); err == nil {
				data = b
				break
			}
		}
	}
	generic := map[string]string{}
	specific := map[string]string{}
	defines := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "#define"); ok {
			if f := strings.Fields(rest); len(f) == 2 {
				defines[f[0]] = f[1]
			}
			continue
		}
		if line == "" || line[0] == '!' || line[0] == '#' {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if d, ok := defines[value]; ok {
			value = d
		}
		if !hexColor.MatchString(value) {
			continue
		}
		i := strings.LastIndexAny(name, ".*")
		key := strings.ToLower(name[i+1:])
		if strings.Trim(name[:i+1], ".*") == "" {
			generic[key] = value
		} else if _, seen := specific[key]; !seen {
			specific[key] = value
		}
	}
	for k, v := range specific {
		if _, ok := generic[k]; !ok {
			generic[k] = v
		}
	}
	return generic
}
