package agent

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
)

func TestIngressUsesFixed80To8080And443To8443Targets(t *testing.T) {
	if target, err := ingressTargetPort(80); err != nil || target != 8080 {
		t.Fatalf("HTTP target = %d, %v", target, err)
	}
	if target, err := ingressTargetPort(443); err != nil || target != 8443 {
		t.Fatalf("HTTPS target = %d, %v", target, err)
	}
	if _, err := ingressTargetPort(444); err == nil {
		t.Fatal("ingress accepted a caller-selected listener port")
	}
}

func TestIngressRestartsOnlyWhenWindowsGatewayChanges(t *testing.T) {
	gateway := net.ParseIP("172.20.0.1")
	var requested []string
	ingress := NewIngress()
	ingress.discoverGateway = func(context.Context) (net.IP, error) { return append(net.IP(nil), gateway...), nil }
	ingress.listen = func(network, address string) (net.Listener, error) {
		requested = append(requested, address)
		return net.Listen("tcp", "127.0.0.1:0")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ingress.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 2 || requested[0] != "127.0.0.1:80" || requested[1] != "127.0.0.1:443" {
		t.Fatalf("fixed ingress listen addresses = %#v", requested)
	}
	if err := ingress.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 2 {
		t.Fatalf("unchanged gateway restarted listeners: %#v", requested)
	}
	gateway = net.ParseIP("172.21.0.1")
	if err := ingress.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(requested) != 4 || ingress.Gateway() != "172.21.0.1" {
		t.Fatalf("gateway refresh listeners=%#v gateway=%q", requested, ingress.Gateway())
	}
	if err := ingress.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestIngressRejectsNonPrivateHostGateway(t *testing.T) {
	ingress := NewIngress()
	ingress.discoverGateway = func(context.Context) (net.IP, error) { return net.ParseIP("8.8.8.8"), nil }
	if err := ingress.Start(context.Background()); err == nil {
		t.Fatal("ingress accepted a non-private gateway")
	}
}

func TestIngressParsesOnlyPrivateGatewayFromDefaultRoute(t *testing.T) {
	got, err := parseWindowsGateway([]byte("default via 172.28.96.1 dev eth0 proto kernel\n192.168.0.0/16 dev eth0\n"))
	if err != nil || got.String() != "172.28.96.1" {
		t.Fatalf("parsed gateway = %v, %v", got, err)
	}
	if _, err := parseWindowsGateway([]byte("default dev eth0\n")); err == nil {
		t.Fatal("default route without a gateway was accepted")
	}
}

func TestIngressRelaysOpaqueTCPToOnlyFixedHostGatewayPort(t *testing.T) {
	gateway := net.ParseIP("172.20.0.1")
	listeners := make(map[string]net.Listener)
	called := make(chan string, 1)
	ingress := NewIngress()
	ingress.discoverGateway = func(context.Context) (net.IP, error) { return gateway, nil }
	ingress.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			listeners[address] = listener
		}
		return listener, err
	}
	ingress.dial = func(_ context.Context, network, address string) (net.Conn, error) {
		called <- address
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			request := make([]byte, 4)
			_, _ = io.ReadFull(server, request)
			_, _ = server.Write([]byte("pong"))
		}()
		return client, nil
	}
	if err := ingress.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer ingress.Stop()
	listener := listeners["127.0.0.1:80"]
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if string(response) != "pong" {
		t.Fatalf("raw relay response = %q", response)
	}
	if got := <-called; got != "172.20.0.1:8080" {
		t.Fatalf("relay dialed %q, want fixed gateway HTTP target", got)
	}
}
