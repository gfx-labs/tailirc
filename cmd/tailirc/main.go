package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gfx-labs/tailirc/pkg/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "tailirc:", err)
		os.Exit(1)
	}
}
