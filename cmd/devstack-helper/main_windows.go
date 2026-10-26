//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
)

type windowsOptions struct {
	allowSID string
	pipe     string
}

func parseWindowsOptions(args []string) (windowsOptions, error) {
	opts := windowsOptions{pipe: helper.DefaultWindowsPipe}
	flags := flag.NewFlagSet("devstack-helper", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.allowSID, "allow-sid", "", "installing user SID allowed to use the helper")
	flags.StringVar(&opts.pipe, "pipe", opts.pipe, "Windows named-pipe endpoint")
	if err := flags.Parse(args); err != nil {
		return windowsOptions{}, err
	}
	if flags.NArg() != 0 {
		return windowsOptions{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if opts.allowSID == "" {
		return windowsOptions{}, errors.New("--allow-sid is required")
	}
	if _, err := helper.NewServer(helper.Config{AllowSID: opts.allowSID, PipeName: opts.pipe}); err != nil {
		return windowsOptions{}, err
	}
	return opts, nil
}

func main() {
	args := os.Args[1:]
	if isWindowsServiceCommand(args) {
		if err := serviceCommand(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "devstack-helper:", err)
			os.Exit(1)
		}
		return
	}
	opts, err := parseWindowsOptions(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "devstack-helper:", err)
		os.Exit(2)
	}
	server, err := helper.NewServer(helper.Config{
		AllowSID: opts.allowSID,
		PipeName: opts.pipe,
		Logger:   slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "devstack-helper:", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := server.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "devstack-helper:", err)
		os.Exit(1)
	}
	<-ctx.Done()
	if err := server.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "devstack-helper:", err)
		os.Exit(1)
	}
}
