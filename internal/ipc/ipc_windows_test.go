//go:build windows

package ipc

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
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
		fdConn, ok := conn.(interface{ Fd() uintptr })
		if !ok {
			server <- fmt.Errorf("conn has no Fd")
			return
		}
		impersonatedSID, err := peerIdentityFromImpersonation(windows.Handle(fdConn.Fd()))
		if err != nil {
			server <- fmt.Errorf("peerIdentityFromImpersonation failed: %w", err)
			return
		}
		if impersonatedSID != peer.SID {
			server <- fmt.Errorf("impersonated SID %q != peer SID %q", impersonatedSID, peer.SID)
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

func TestNamedPipePeerIdentityLevels(t *testing.T) {
	currentSID, err := CurrentSID()
	if err != nil {
		t.Fatalf("CurrentSID: %v", err)
	}

	t.Run("identification level resolves user SID via OpenAsSelf", func(t *testing.T) {
		endpoint := testPipe()
		ln, err := Listen(endpoint)
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		defer ln.Close()

		peerCh := make(chan PeerIdentity, 1)
		errCh := make(chan error, 1)
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				errCh <- err
				return
			}
			defer conn.Close()
			peer, err := PeerIdentityOf(conn)
			if err != nil {
				errCh <- err
				return
			}
			peerCh <- peer
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, err := dialWithImpLevel(ctx, endpoint, winio.PipeImpLevelIdentification)
		if err != nil {
			t.Fatalf("dial identification: %v", err)
		}
		defer conn.Close()

		select {
		case peer := <-peerCh:
			if peer.SID != currentSID {
				t.Fatalf("peer SID %q != current SID %q", peer.SID, currentSID)
			}
		case err := <-errCh:
			t.Fatalf("PeerIdentityOf with identification level failed: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for accept")
		}
	})

	t.Run("anonymous level fails closed", func(t *testing.T) {
		endpoint := testPipe()
		ln, err := Listen(endpoint)
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		defer ln.Close()

		errCh := make(chan error, 1)
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				errCh <- err
				return
			}
			defer conn.Close()
			_, err = PeerIdentityOf(conn)
			errCh <- err
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, err := dialWithImpLevel(ctx, endpoint, winio.PipeImpLevelAnonymous)
		if err != nil {
			t.Fatalf("dial anonymous: %v", err)
		}
		defer conn.Close()

		select {
		case err := <-errCh:
			if err == nil {
				t.Fatal("PeerIdentityOf unexpectedly succeeded for anonymous pipe client")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for accept")
		}
	})
}

func dialWithImpLevel(ctx context.Context, endpoint Endpoint, impLevel winio.PipeImpLevel) (net.Conn, error) {
	return winio.DialPipeAccessImpLevel(ctx, endpoint.Address,
		uint32(windows.GENERIC_READ|windows.GENERIC_WRITE),
		impLevel)
}
