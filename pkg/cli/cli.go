// Package cli implements the tailirc command line.
package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/tailscale/tailcat"
	"github.com/urfave/cli/v3"
	"tailscale.com/types/logger"

	"github.com/gfx-labs/tailirc/pkg/ircclient"
	"github.com/gfx-labs/tailirc/pkg/ircd"
	"github.com/gfx-labs/tailirc/pkg/tckeys"
	"github.com/gfx-labs/tailirc/pkg/tui"
)

// IRCPort is the port the IRC server listens on inside the tailcat tunnel.
const IRCPort = 6667

// Run runs the tailirc command with args (including the program name).
func Run(ctx context.Context, args []string) error {
	return newCommand().Run(ctx, args)
}

func newCommand() *cli.Command {
	verbose := &cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "log tailcat diagnostics"}
	clientKey := &cli.StringFlag{
		Name:  "key",
		Value: "client-default",
		Usage: "client key name or path, from `tailcat genkey --client`",
	}
	namesFlag := &cli.StringFlag{
		Name:  "names",
		Usage: "file of name@key or name@hexprefix entries used to show nicks (default: $CONFIG/tailcat/allowed-ircs, if it exists)",
	}
	return &cli.Command{
		Name:  "tailirc",
		Usage: "IRC over tailcat, with messages kept in memory by the host",
		Flags: []cli.Flag{verbose},
		Commands: []*cli.Command{
			{
				Name:  "serve",
				Usage: "host an IRC server over tailcat",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "key",
						Value: "default",
						Usage: "server key name or path, from `tailcat genkey`; 'new' for an ephemeral address",
					},
					&cli.StringSliceFlag{
						Name:  "allow",
						Usage: "client public key (nodekey:...) allowed to connect; repeatable or comma separated",
					},
					&cli.StringFlag{
						Name:  "allow-file",
						Usage: "file of allowed client public keys, one per line, # comments (default: $CONFIG/tailcat/allowed-ircs, if it exists)",
					},
					&cli.IntFlag{
						Name:  "history",
						Value: 1000,
						Usage: "messages kept in memory per channel",
					},
					&cli.BoolFlag{
						Name:  "tui",
						Usage: "run a chat session in the foreground as the host",
					},
					&cli.StringFlag{
						Name:  "channel",
						Value: "#general",
						Usage: "channel the --tui session opens",
					},
				},
				Action: serve,
			},
			{
				Name:      "connect",
				Usage:     "forward a local port to the server for a regular IRC client",
				ArgsUsage: "ADDR",
				Flags: []cli.Flag{
					clientKey,
					&cli.StringFlag{Name: "listen", Value: "127.0.0.1:6667", Usage: "local address to listen on: host:port, or unix:PATH for a unix socket"},
				},
				Action: connect,
			},
			{
				Name:      "send",
				Usage:     "send a message to a channel; reads stdin when MESSAGE is omitted",
				ArgsUsage: "ADDR#CHANNEL [MESSAGE]",
				Flags:     []cli.Flag{clientKey},
				Action:    send,
			},
			{
				Name:      "tui",
				Usage:     "interactive chat session; type /join #chan to switch channels",
				ArgsUsage: "ADDR[#CHANNEL]",
				Flags:     []cli.Flag{clientKey, namesFlag},
				Action:    runTUI,
			},
			{
				Name:      "read",
				Usage:     "print a channel's history from the server's memory",
				ArgsUsage: "ADDR#CHANNEL",
				Flags: []cli.Flag{
					clientKey,
					namesFlag,
					&cli.IntFlag{Name: "limit", Aliases: []string{"n"}, Usage: "most recent messages to print; 0 for the whole buffer"},
				},
				Action: read,
			},
		},
	}
}

func logf(cmd *cli.Command) logger.Logf {
	if cmd.Bool("verbose") {
		return log.Printf
	}
	return logger.Discard
}

func serve(ctx context.Context, cmd *cli.Command) error {
	var allow tckeys.AllowList
	allowed := 0
	for _, v := range cmd.StringSlice("allow") {
		n, err := allow.Parse(v)
		if err != nil {
			return err
		}
		allowed += n
	}
	path := cmd.String("allow-file")
	if path == "" {
		defaultPath, err := tckeys.DefaultAllowFile()
		if err != nil {
			return err
		}
		if _, err := os.Stat(defaultPath); err == nil {
			path = defaultPath
		}
	}
	if path != "" {
		fmt.Fprintf(os.Stderr, "reading allowed keys from %s\n", path)
		n, err := allow.LoadAllowFile(path, false)
		if err != nil {
			return err
		}
		allowed += n
	}
	if allowed == 0 {
		return fmt.Errorf("no allowed client keys; add nodekey:... lines to $CONFIG/tailcat/allowed-ircs, or pass --allow or --allow-file")
	}

	server := &tailcat.Server{
		Logf:        logf(cmd),
		AllowClient: allow.Keys.Contains,
	}
	saved, err := tckeys.LoadServerKey(cmd.String("key"))
	if err != nil {
		return err
	}
	if saved != nil {
		server.Key = saved.Private
		server.PresharedKey = saved.Public.PresharedKey
		server.DisablePresharedKey = saved.Public.PresharedKey.IsZero()
		if len(saved.Public.Region) > 0 {
			server.Region = saved.Public.Region[0]
		} else if saved.Public.RegionID > 0 {
			server.RegionID = saved.Public.RegionID
		}
	}
	listener, err := server.Listen(ctx, "tcp", fmt.Sprintf(":%d", IRCPort))
	if err != nil {
		return fmt.Errorf("start tailcat: %w", err)
	}
	defer server.Close()
	addr := server.TailcatAddr()

	if !cmd.Bool("tui") {
		fmt.Fprintf(os.Stderr, "tailirc serving %d allowed keys, messages kept in memory only\n", allowed)
		fmt.Fprintf(os.Stderr, "address: %s\n", addr)
		fmt.Fprintf(os.Stderr, "example: tailirc tui '%s#general'\n", addr)
		irc := ircd.New(ircd.Config{HistoryLimit: int(cmd.Int("history"))})
		return irc.Serve(ctx, listener, peerNick(server, &allow))
	}

	ui := tui.New(tui.Config{
		Channel: normalizeChannel(cmd.String("channel")),
		Info:    fmt.Sprintf("hosting for %d allowed keys. Alt-Shift-C copies the address to share", allowed),
		Addr:    string(addr),
	})
	irc := ircd.New(ircd.Config{HistoryLimit: int(cmd.Int("history")), Logf: ui.Logf})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- irc.Serve(ctx, listener, peerNick(server, &allow)) }()

	info, err := tailcat.ParseAddr(addr)
	if err != nil {
		return err
	}
	hostNick := tckeys.Nick(info.ServerPublic.NodePublic)
	err = ui.Run(ctx, func(context.Context) (net.Conn, error) {
		hostSide, uiSide := net.Pipe()
		go irc.ServeConn(hostSide, hostNick)
		return uiSide, nil
	})
	cancel()
	if err != nil {
		return err
	}
	return <-serveErr
}

func peerNick(server *tailcat.Server, allow *tckeys.AllowList) ircd.Identify {
	return func(c net.Conn) (string, bool) {
		k, ok := server.PeerKey(c.RemoteAddr())
		if !ok {
			return "", false
		}
		return allow.Nick(k), true
	}
}

func normalizeChannel(name string) string {
	if name != "" && !strings.HasPrefix(name, "#") {
		return "#" + name
	}
	return name
}

func dialServer(ctx context.Context, cmd *cli.Command, addr string) (net.Conn, *tailcat.Client, error) {
	key, err := tckeys.LoadClientKey(cmd.String("key"))
	if err != nil {
		return nil, nil, err
	}
	client := &tailcat.Client{Server: tailcat.Addr(addr), Key: key, Logf: logf(cmd)}
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := client.DialTCPPort(dialCtx, IRCPort)
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("connect (is your key %s on the server's allow list?): %w", key.Public(), err)
	}
	return conn, client, nil
}

// splitTarget splits "ADDR#CHANNEL" into the tailcat address and "#CHANNEL".
// Tailcat addresses are base64url, so they never contain '#'.
func splitTarget(arg string) (addr, channel string, err error) {
	addr, name, ok := strings.Cut(arg, "#")
	if !ok || addr == "" || name == "" {
		return "", "", fmt.Errorf("expected ADDR#CHANNEL, got %q", arg)
	}
	return addr, "#" + name, nil
}

func registeredClient(ctx context.Context, cmd *cli.Command) (*ircclient.Client, string, func(), error) {
	if cmd.Args().Len() < 1 {
		return nil, "", nil, fmt.Errorf("missing ADDR#CHANNEL")
	}
	addr, channel, err := splitTarget(cmd.Args().First())
	if err != nil {
		return nil, "", nil, err
	}
	conn, tc, err := dialServer(ctx, cmd, addr)
	if err != nil {
		return nil, "", nil, err
	}
	cleanup := func() {
		conn.Close()
		tc.DrainTCP(context.Background())
		tc.Close()
	}
	irc, err := ircclient.Register(conn)
	if err != nil {
		cleanup()
		return nil, "", nil, err
	}
	return irc, channel, cleanup, nil
}

func send(ctx context.Context, cmd *cli.Command) error {
	var text string
	if cmd.Args().Len() > 1 {
		text = strings.Join(cmd.Args().Tail(), " ")
	} else {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		text = string(data)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("empty message")
	}
	irc, channel, cleanup, err := registeredClient(ctx, cmd)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := irc.Send(channel, text); err != nil {
		return err
	}
	return irc.Quit()
}

func read(ctx context.Context, cmd *cli.Command) error {
	names, err := loadNames(cmd)
	if err != nil {
		return err
	}
	irc, channel, cleanup, err := registeredClient(ctx, cmd)
	if err != nil {
		return err
	}
	defer cleanup()
	messages, err := irc.History(channel, int(cmd.Int("limit")))
	if err != nil {
		return err
	}
	for _, m := range messages {
		fmt.Printf("%s <%s> %s\n", m.Time.Local().Format("2006-01-02 15:04:05"), names.Display(m.Nick), m.Text)
	}
	return irc.Quit()
}

func connect(ctx context.Context, cmd *cli.Command) error {
	addr := cmd.Args().First()
	if addr == "" {
		return fmt.Errorf("missing ADDR")
	}
	addr, _, _ = strings.Cut(addr, "#")
	key, err := tckeys.LoadClientKey(cmd.String("key"))
	if err != nil {
		return err
	}
	client := &tailcat.Client{Server: tailcat.Addr(addr), Key: key, Logf: logf(cmd)}
	defer client.Close()
	if _, err := client.Ping(ctx); err != nil {
		return fmt.Errorf("reach server (is your key %s on its allow list?): %w", key.Public(), err)
	}
	listener, err := listenLocal(cmd.String("listen"))
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	fmt.Fprintf(os.Stderr, "connected with key %s; point your IRC client at %s (no TLS, no password)\n",
		key.Public().ShortString(), listener.Addr())
	for {
		local, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go func() {
			remote, err := client.DialTCPPort(ctx, IRCPort)
			if err != nil {
				log.Printf("dial server: %v", err)
				local.Close()
				return
			}
			tailcat.ProxyConns(local, remote)
		}()
	}
}
