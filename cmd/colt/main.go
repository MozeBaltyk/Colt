package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/MozeBaltyk/Colt/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.New().Root().ExecuteContext(ctx); err != nil {
		if !app.ErrorReported(err) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
