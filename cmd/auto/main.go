package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/viveksbh/autoscope/internal/cli"
	"github.com/viveksbh/autoscope/internal/exitcode"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := cli.NewRootCmd().ExecuteContext(ctx)
	if err == nil {
		return
	}

	// SIGINT path: ctx error preempts other codes.
	if errors.Is(err, context.Canceled) && ctx.Err() == context.Canceled {
		os.Exit(exitcode.Sigint)
	}

	fmt.Fprintln(os.Stderr, err)
	os.Exit(exitcode.CodeOf(err))
}
