package cli

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/gfx-labs/tailirc/pkg/tckeys"
	"github.com/gfx-labs/tailirc/pkg/tui"
)

func runTUI(ctx context.Context, cmd *cli.Command) error {
	arg := cmd.Args().First()
	if arg == "" {
		return fmt.Errorf("missing ADDR[#CHANNEL]")
	}
	addr, channel, _ := strings.Cut(arg, "#")
	names, err := loadNames(cmd)
	if err != nil {
		return err
	}
	ui := tui.New(tui.Config{Channel: normalizeChannel(channel), Addr: addr, DisplayNick: names.Display})
	return ui.Run(ctx, func(ctx context.Context) (net.Conn, error) {
		conn, tc, err := dialServer(ctx, cmd, addr)
		if err != nil {
			return nil, err
		}
		return &closeBoth{Conn: conn, close: tc.Close}, nil
	})
}

// loadNames reads --names, or the default allowlist file if it exists, for
// showing nicks on the client side.
func loadNames(cmd *cli.Command) (*tckeys.AllowList, error) {
	var names tckeys.AllowList
	if path := cmd.String("names"); path != "" {
		_, err := names.LoadAllowFile(path, false)
		return &names, err
	}
	path, err := tckeys.DefaultAllowFile()
	if err != nil {
		return nil, err
	}
	_, err = names.LoadAllowFile(path, true)
	return &names, err
}

// closeBoth closes the tailcat client along with its connection.
type closeBoth struct {
	net.Conn
	close func() error
}

func (c *closeBoth) Close() error {
	err := c.Conn.Close()
	c.close()
	return err
}
