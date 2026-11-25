// Package tui is a small terminal chat client for a tailirc server.
package tui

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/gfx-labs/tailirc/pkg/ircclient"
)

// Config configures a session.
type Config struct {
	// Channel is joined at start. Defaults to #general.
	Channel string
	// Info is shown at the top of the message view, for example the server
	// address when hosting.
	Info string
	// Addr is the server's tailcat address. Alt-Shift-C copies Addr#channel.
	Addr string
	// DisplayNick maps a nick from the server to the name shown. Nil shows
	// nicks unchanged.
	DisplayNick func(string) string
}

// UI is a running terminal session. Logf may be used from any goroutine,
// including before Run connects, to show status lines.
type UI struct {
	app      *tview.Application
	messages *tview.TextView
	input    *tview.InputField
	title    *tview.TextView
	theme    Theme
	display  func(string) string
	addr     string
	screen   tcell.Screen

	mu      sync.Mutex
	client  *ircclient.Client
	channel string
}

// New builds the UI without starting it.
func New(config Config) *UI {
	if config.Channel == "" {
		config.Channel = "#general"
	}
	theme := loadTheme()
	theme.apply()
	u := &UI{
		theme:    theme,
		display:  config.DisplayNick,
		app:      tview.NewApplication(),
		messages: tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(true),
		input:    tview.NewInputField().SetLabel("> ").SetFieldBackgroundColor(tcell.ColorDefault),
		title:    tview.NewTextView().SetDynamicColors(true),
		addr:     config.Addr,
		channel:  config.Channel,
	}
	u.messages.SetMaxLines(5000)
	if config.Info != "" {
		u.system("%s", config.Info)
	}
	u.system("commands: /join #channel, /quit. PgUp/PgDn scroll. Alt-Shift-C copies address#channel.")
	u.setTitle()

	u.input.SetDoneFunc(func(key tcell.Key) {
		if key != tcell.KeyEnter {
			return
		}
		line := u.input.GetText()
		u.input.SetText("")
		u.submit(line)
	})
	u.app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyRune && ev.Modifiers()&tcell.ModAlt != 0 && ev.Rune() == 'C' {
			u.copyTarget()
			return nil
		}
		switch ev.Key() {
		case tcell.KeyPgUp, tcell.KeyPgDn:
			u.messages.InputHandler()(ev, nil)
			return nil
		}
		return ev
	})
	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(u.title, 1, 0, false).
		AddItem(u.messages, 0, 1, false).
		AddItem(u.input, 1, 0, true)
	u.app.SetRoot(layout, true)
	return u
}

// Logf shows a status line. It is safe from any goroutine.
func (u *UI) Logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	u.app.QueueUpdateDraw(func() { u.system("%s", line) })
}

// system writes a status line. Call only from the UI goroutine or before Run.
func (u *UI) system(format string, args ...any) {
	fmt.Fprintf(u.messages, "[%s]-- %s[-]\n", u.theme.Dim, tview.Escape(fmt.Sprintf(format, args...)))
}

// setTitle updates the title bar. Call only from the UI goroutine or before Run.
func (u *UI) setTitle() {
	u.mu.Lock()
	name, ch := "", u.channel
	if u.client != nil {
		name = u.client.Nick
	}
	u.mu.Unlock()
	u.title.SetText(fmt.Sprintf("[::b]tailirc[::-] %s  [%s]%s[-]  [%s]%s  Alt-Shift-C copy[-]",
		tview.Escape(ch), u.theme.Self, u.nick(name), u.theme.Dim, tview.Escape(shortAddr(u.addr)+ch)))
}

func (u *UI) nick(n string) string {
	if u.display == nil || n == "" {
		return n
	}
	return u.display(n)
}

func shortAddr(addr string) string {
	if len(addr) > 20 {
		return addr[:16] + "..."
	}
	return addr
}

// copyTarget copies addr#channel to the clipboard. Call only from the UI goroutine.
func (u *UI) copyTarget() {
	if u.addr == "" {
		u.system("no server address to copy")
		return
	}
	u.mu.Lock()
	target := u.addr + u.channel
	u.mu.Unlock()
	if err := copyToClipboard(u.screen, target); err != nil {
		u.system("copy failed: %v", err)
		return
	}
	u.system("copied %s", shortAddr(u.addr)+u.channel)
}

func (u *UI) submit(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	u.mu.Lock()
	c, ch := u.client, u.channel
	u.mu.Unlock()
	if strings.HasPrefix(line, "/") {
		cmd, arg, _ := strings.Cut(line[1:], " ")
		switch cmd {
		case "quit", "q":
			u.app.Stop()
		case "join", "j":
			arg = strings.TrimSpace(arg)
			if !strings.HasPrefix(arg, "#") {
				arg = "#" + arg
			}
			if len(arg) < 2 {
				u.system("usage: /join #channel")
				return
			}
			u.mu.Lock()
			u.channel = arg
			u.mu.Unlock()
			u.setTitle()
			if c != nil {
				u.enter(c, arg)
			}
		default:
			u.system("unknown command /%s", cmd)
		}
		return
	}
	if c == nil {
		u.system("not connected yet")
		return
	}
	if err := c.Send(ch, line); err != nil {
		u.system("send: %v", err)
	}
}

// enter joins channel and replays its history.
func (u *UI) enter(c *ircclient.Client, channel string) {
	u.messages.Clear()
	if err := c.Join(channel); err != nil {
		u.system("join: %v", err)
		return
	}
	if err := c.RequestHistory(channel, 0); err != nil {
		u.system("history: %v", err)
	}
}

func (u *UI) show(ev ircclient.Event) {
	u.mu.Lock()
	current, me := u.channel, ""
	if u.client != nil {
		me = u.client.Nick
	}
	u.mu.Unlock()
	m := ev.Message
	isMe := m.Nick == me
	m.Nick = u.nick(m.Nick)
	if !strings.EqualFold(m.Target, current) && ev.Kind != ircclient.EventQuit && ev.Kind != ircclient.EventError {
		if ev.Kind == ircclient.EventMessage && !ev.History {
			u.system("new message in %s from %s", m.Target, m.Nick)
		}
		return
	}
	ts := m.Time.Local().Format("15:04:05")
	switch ev.Kind {
	case ircclient.EventMessage:
		color := u.theme.Other
		if isMe {
			color = u.theme.Self
		}
		fmt.Fprintf(u.messages, "[%s]%s[-] [%s]<%s>[-] %s\n", u.theme.Dim, ts, color, m.Nick, tview.Escape(m.Text))
	case ircclient.EventJoin:
		if !isMe {
			u.system("%s joined", m.Nick)
		}
	case ircclient.EventPart:
		u.system("%s left", m.Nick)
	case ircclient.EventQuit:
		u.system("%s quit", m.Nick)
	case ircclient.EventError:
		u.system("server: %s", m.Text)
	}
	if !ev.History {
		u.messages.ScrollToEnd()
	}
}

// Run runs the UI until the user quits or ctx ends. connect is called in the
// background after the screen is up, so status lines from Logf are visible
// while it dials. If the connection fails or later closes, the UI shows why
// and stays open until the user quits.
func (u *UI) Run(ctx context.Context, connect func(context.Context) (net.Conn, error)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		u.app.Stop()
	}()
	go u.session(ctx, connect)
	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	u.screen = screen
	u.app.SetScreen(screen)
	return u.app.Run()
}

func (u *UI) session(ctx context.Context, connect func(context.Context) (net.Conn, error)) {
	u.Logf("connecting...")
	conn, err := connect(ctx)
	if err != nil {
		u.Logf("connect failed: %v", err)
		return
	}
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	c, err := ircclient.Register(conn)
	if err != nil {
		u.Logf("register failed: %v", err)
		return
	}
	u.mu.Lock()
	u.client = c
	u.mu.Unlock()
	u.app.QueueUpdateDraw(func() {
		u.setTitle()
		u.mu.Lock()
		channel := u.channel
		u.mu.Unlock()
		u.enter(c, channel)
	})
	err = c.Listen(func(ev ircclient.Event) {
		u.app.QueueUpdateDraw(func() { u.show(ev) })
	})
	u.mu.Lock()
	u.client = nil
	u.mu.Unlock()
	if ctx.Err() == nil {
		u.Logf("disconnected: %v. /quit to exit", err)
	}
}
