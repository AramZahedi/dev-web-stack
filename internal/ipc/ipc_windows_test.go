//go:build windows

package ipc

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func testPipe() Endpoint {
	return NamedPipeEndpoint(fmt.Sprintf("devstack-ipc-test-%d-%d", os.Getpid(), time.Now().UnixNano()))
}

func TestNamedPipeListenDialAndPeerSID(t *testing.T) {
	endpoint := testPipe()
	ln, err := Listen(endpoint)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	server := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			server <- err
			return
		}
		defer conn.Close()
		peer, err := PeerIdentityOf(conn)
		if err != nil {
			server <- err
			return
		}
		if !strings.HasPrefix(peer.SID, "S-1-") {
			server <- fmt.Errorf("peer SID = %q", peer.SID)
			return
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			server <- err
			return
		}
		_, err = conn.Write(buf)
		server <- err
	}()

	conn, err := Dial(context.Background(), endpoint, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q", buf)
	}
	if err := <-server; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func TestPipeSDDL(t *testing.T) {
	sddl, err := PipeSDDL("S-1-5-21-1111111111-2222222222-3333333333-1001", true)
	if err != nil {
		t.Fatalf("PipeSDDL: %v", err)
	}
	for _, want := range []string{"D:P", "SY", "BA", "S-1-5-21-1111111111-2222222222-3333333333-1001"} {
		if !strings.Contains(sddl, want) {
			t.Fatalf("SDDL %q does not contain %q", sddl, want)
		}
	}
	if _, err := PipeSDDL("", false); err == nil {
		t.Fatal("empty SID accepted")
	}
	if _, err := PipeSDDL("not-a-sid", false); err == nil {
		t.Fatal("invalid SID accepted")
	}
	noAdmins, err := PipeSDDL("S-1-5-21-1111111111-2222222222-3333333333-1001", false)
	if err != nil {
		t.Fatalf("PipeSDDL: %v", err)
	}
	if strings.Contains(noAdmins, ";;;BA)") {
		t.Fatalf("administrators grant present when disabled: %q", noAdmins)
	}
}

func TestNamedPipeListenerRejectsForeignSID(t *testing.T) {
	endpoint := testPipe()
	ln, err := Listen(endpoint, WithAllowSID("S-1-5-18"))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	if _, err := Dial(context.Background(), endpoint, time.Second); err == nil {
		// The pipe ACL deliberately does not grant the current user, so the
		// dial itself must fail. Either failure path proves the boundary.
		t.Fatal("dial unexpectedly succeeded through a SYSTEM-only pipe ACL")
	}
}

func TestPeerIdentityCurrentProcess(t *testing.T) {
	sid, err := CurrentSID()
	if err != nil {
		t.Fatalf("CurrentSID: %v", err)
	}
	if !strings.HasPrefix(sid, "S-1-") {
		t.Fatalf("CurrentSID = %q", sid)
	}
}
