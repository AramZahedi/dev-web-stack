// Command devstack-helper is the narrowly privileged helper service. It owns
// the managed hosts block, the local certificate authority, trust stores, and
// the privileged 80/443 forwarders.
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
	"path/filepath"
	"strings"
	"syscall"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
)

const (
	defaultSocket   = "/run/devstack-helper/helper.sock"
	defaultStateDir = "/var/lib/devstack-helper"
	defaultHosts    = "/etc/hosts"

	defaultHTTPPort       = 80
	defaultHTTPSPort      = 443
	defaultProxyHTTPPort  = 8080
	defaultProxyHTTPSPort = 8443
)

// options holds the parsed helper configuration. Values start at the built-in
// defaults, are optionally replaced by --config, and are finally overridden by
// explicit command-line flags.
type options struct {
	configPath     string
	socket         string
	stateDir       string
	hosts          string
	trustDir       string
	allowUID       int
	httpPort       int
	httpsPort      int
	proxyHTTPPort  int
	proxyHTTPSPort int
	logLevel       string
}

func (o options) caDir() string {
	return filepath.Join(o.stateDir, "pki")
}

// parseOptions parses the helper's configuration file and flags. Defaults
// target the production install; tests and unprivileged runs override every
// path and port. The file is loaded before the flags so explicit flags win.
func parseOptions(args []string) (options, error) {
	opts := options{
		socket:         defaultSocket,
		stateDir:       defaultStateDir,
		hosts:          defaultHosts,
		allowUID:       os.Getuid(),
		httpPort:       defaultHTTPPort,
		httpsPort:      defaultHTTPSPort,
		proxyHTTPPort:  defaultProxyHTTPPort,
		proxyHTTPSPort: defaultProxyHTTPSPort,
		logLevel:       "info",
	}

	configPath, err := findConfigPath(args)
	if err != nil {
		return options{}, err
	}
	if configPath != "" {
		fileCfg, err := loadFileConfig(configPath)
		if err != nil {
			return options{}, err
		}
		fileCfg.apply(&opts)
		opts.configPath = configPath
	}

	flags := flag.NewFlagSet("devstack-helper", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.configPath, "config", opts.configPath, "YAML configuration file")
	flags.StringVar(&opts.socket, "socket", opts.socket, "Unix socket path")
	flags.StringVar(&opts.stateDir, "state-dir", opts.stateDir, "helper state directory")
	flags.StringVar(&opts.hosts, "hosts", opts.hosts, "hosts file to manage")
	flags.StringVar(&opts.trustDir, "trust-dir", opts.trustDir, "system CA anchor directory (empty detects)")
	flags.IntVar(&opts.allowUID, "allow-uid", opts.allowUID, "only UID permitted to call the helper")
	flags.IntVar(&opts.httpPort, "http-port", opts.httpPort, "privileged HTTP listen port")
	flags.IntVar(&opts.httpsPort, "https-port", opts.httpsPort, "privileged HTTPS listen port")
	flags.IntVar(&opts.proxyHTTPPort, "proxy-http", opts.proxyHTTPPort, "daemon HTTP proxy port")
	flags.IntVar(&opts.proxyHTTPSPort, "proxy-https", opts.proxyHTTPSPort, "daemon HTTPS proxy port")
	flags.StringVar(&opts.logLevel, "log-level", opts.logLevel, "debug, info, warn, or error")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(opts.socket) == "" {
		return options{}, errors.New("--socket is required")
	}
	if strings.TrimSpace(opts.stateDir) == "" {
		return options{}, errors.New("--state-dir is required")
	}
	if opts.allowUID < 0 {
		return options{}, fmt.Errorf("--allow-uid %d must not be negative", opts.allowUID)
	}
	return opts, nil
}

// newHelperServer builds the server from parsed flags. It is shared with the
// startup test so temp-dir/high-port behavior is exercised directly.
func newHelperServer(opts options, logger *slog.Logger) (*helper.Server, error) {
	return helper.NewServer(helper.Config{
		SocketPath:       opts.socket,
		HostsPath:        opts.hosts,
		AllowUID:         opts.allowUID,
		CADir:            opts.caDir(),
		TrustDir:         opts.trustDir,
		ForwardHTTPPort:  opts.httpPort,
		ForwardHTTPSPort: opts.httpsPort,
		ProxyHTTPPort:    opts.proxyHTTPPort,
		ProxyHTTPSPort:   opts.proxyHTTPSPort,
		Logger:           logger,
	})
}

func main() {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "devstack-helper:", err)
		os.Exit(2)
	}

	logger, err := newLogger(opts.logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, "devstack-helper:", err)
		os.Exit(2)
	}

	server, err := newHelperServer(opts, logger)
	if err != nil {
		fmt.Fprintln(os.Stderr, "devstack-helper:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "devstack-helper:", err)
		os.Exit(1)
	}
	logger.Info("helper listening", "socket", opts.socket, "state_dir", opts.stateDir)

	<-ctx.Done()
	if err := server.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "devstack-helper:", err)
		os.Exit(1)
	}
}

func newLogger(level string) (*slog.Logger, error) {
	var parsed slog.Level
	value := strings.TrimSpace(level)
	if value != "" {
		if err := parsed.UnmarshalText([]byte(value)); err != nil {
			return nil, fmt.Errorf("invalid log level %q", level)
		}
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parsed})), nil
}
