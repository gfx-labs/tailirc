package cli

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/gfx-labs/tailirc/pkg/tui"
)

func runTUI(ctx context.Context, cmd *cli.Command) error {
	arg := cmd.Args().First()
	if arg == "" {
		return fmt.Errorf("missing ADDR[#CHANNEL]")
	}
	addr, channel, _ := strings.Cut(arg, "#")
	ui := tui.New(tui.Config{Channel: normalizeChannel(channel)})
	return ui.Run(ctx, func(ctx context.Context) (net.Conn, error) {
		conn, tc, err := dialServer(ctx, cmd, addr)
		if err != nil {
			return nil, err
		}
		return &closeBoth{Conn: conn, close: tc.Close}, nil
	})
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
