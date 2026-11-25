package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/tailscale/tailcat"
	"github.com/urfave/cli/v3"

	"github.com/gfx-labs/tailirc/pkg/aliases"
)

func aliasAdd(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 2 {
		return fmt.Errorf("usage: tailirc alias add [--ephemeral] NAME ADDR")
	}
	name, addr := cmd.Args().Get(0), cmd.Args().Get(1)
	addr, _, _ = strings.Cut(addr, "#")
	if _, err := tailcat.ParseAddr(tailcat.Addr(addr)); err != nil {
		return fmt.Errorf("not a tailcat address: %w", err)
	}
	ephemeral := cmd.Bool("ephemeral")
	if err := aliases.Set(name, addr, ephemeral); err != nil {
		return err
	}
	kind := "saved"
	if ephemeral {
		kind = "ephemeral"
	}
	fmt.Fprintf(os.Stderr, "%s alias %q added; use: tailirc read '%s#general'\n", kind, name, name)
	return nil
}

func aliasList(ctx context.Context, cmd *cli.Command) error {
	list, err := aliases.List()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, a := range list {
		kind := "saved"
		if a.Ephemeral {
			kind = "ephemeral"
		}
		fmt.Fprintf(w, "%s\t%s\t%.16s...\n", a.Name, kind, a.Addr)
	}
	return w.Flush()
}

func aliasRemove(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return fmt.Errorf("usage: tailirc alias rm NAME")
	}
	removed, err := aliases.Remove(cmd.Args().First())
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no alias %q", cmd.Args().First())
	}
	return nil
}
