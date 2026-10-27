//go:build windows

package helper

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/forwarder"
)

type windowsForwarders interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type windowsForwarderFactory func(httpListen, httpsListen, httpTarget, httpsTarget string) (windowsForwarders, error)

func windowsForwarderAddresses(httpTarget, httpsTarget string) (httpListen, httpsListen, fixedHTTP, fixedHTTPS string, err error) {
	if err := validateWindowsDaemonTarget(httpTarget, DefaultWindowsProxyHTTP); err != nil {
		return "", "", "", "", fmt.Errorf("HTTP target: %w", err)
	}
	if err := validateWindowsDaemonTarget(httpsTarget, DefaultWindowsProxyHTTPS); err != nil {
		return "", "", "", "", fmt.Errorf("HTTPS target: %w", err)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultWindowsHTTPPort)),
		net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultWindowsHTTPSPort)),
		net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultWindowsProxyHTTP)),
		net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultWindowsProxyHTTPS)), nil
}

func validateWindowsDaemonTarget(address string, wantPort int) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port != wantPort {
		return fmt.Errorf("target port must be %d", wantPort)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || !strings.EqualFold(host, "127.0.0.1") {
		return errors.New("target must be the Windows daemon's IPv4 loopback address")
	}
	return nil
}

func newWindowsForwarders(httpListen, httpsListen, httpTarget, httpsTarget string) (windowsForwarders, error) {
	wantHTTP, wantHTTPS, fixedHTTP, fixedHTTPS, err := windowsForwarderAddresses(httpTarget, httpsTarget)
	if err != nil {
		return nil, err
	}
	if httpListen != wantHTTP || httpsListen != wantHTTPS || httpTarget != fixedHTTP || httpsTarget != fixedHTTPS {
		return nil, errors.New("helper: Windows public forwarder addresses are fixed")
	}
	return forwarder.New(httpListen, httpsListen, httpTarget, httpsTarget, nil), nil
}
