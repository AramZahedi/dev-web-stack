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
	configPath     string
	allowSID       string
	pipe           string
	proxyHTTPPort  int
	proxyHTTPSPort int
}

func parseWindowsOptions(args []string) (windowsOptions, error) {
	configPath, err := configPathFromWindowsArgs(args)
	if err != nil {
		return windowsOptions{}, err
	}
	opts := windowsOptions{
		configPath: configPath, pipe: helper.DefaultWindowsPipe,
		proxyHTTPPort: helper.DefaultWindowsProxyHTTP, proxyHTTPSPort: helper.DefaultWindowsProxyHTTPS,
	}
	fileConfig, err := loadWindowsFileConfig(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return windowsOptions{}, err
	}
	fileConfig.apply(&opts)
	flags := flag.NewFlagSet("devstack-helper", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.configPath, "config", opts.configPath, "Windows helper configuration file")
	flags.StringVar(&opts.allowSID, "allow-sid", opts.allowSID, "installing user SID allowed to use the helper")
	flags.StringVar(&opts.pipe, "pipe", opts.pipe, "Windows named-pipe endpoint")
	flags.IntVar(&opts.proxyHTTPPort, "proxy-http", opts.proxyHTTPPort, "Windows daemon HTTP proxy port")
	flags.IntVar(&opts.proxyHTTPSPort, "proxy-https", opts.proxyHTTPSPort, "Windows daemon HTTPS proxy port")
	if err := flags.Parse(args); err != nil {
		return windowsOptions{}, err
	}
	if flags.NArg() != 0 {
		return windowsOptions{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if opts.allowSID == "" {
		return windowsOptions{}, errors.New("--allow-sid is required")
	}
	if _, err := helper.NewServer(helper.Config{AllowSID: opts.allowSID, PipeName: opts.pipe, ConfigPath: opts.configPath,
		ProxyHTTPPort: opts.proxyHTTPPort, ProxyHTTPSPort: opts.proxyHTTPSPort}); err != nil {
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
		AllowSID: opts.allowSID, PipeName: opts.pipe, ConfigPath: opts.configPath,
		ProxyHTTPPort: opts.proxyHTTPPort, ProxyHTTPSPort: opts.proxyHTTPSPort,
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
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
