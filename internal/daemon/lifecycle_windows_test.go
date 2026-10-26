//go:build windows

package daemon

import (
	"errors"
	"testing"
)

func TestEndpointIsStableNamedPipe(t *testing.T) {
	home := t.TempDir()
	first := Endpoint(home)
	second := Endpoint(home)
	if first.Network != "npipe" || first.Address != second.Address {
		t.Fatalf("endpoints = %+v %+v", first, second)
	}
	if first.Address == Endpoint(t.TempDir()).Address {
		t.Fatal("different homes share an endpoint")
	}
}

func TestLifetimeLockExcludesSecondOwner(t *testing.T) {
	home := t.TempDir()
	first, err := acquireLifetimeLock(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLifetimeLock(home); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second lock = %v, want ErrAlreadyRunning", err)
	}
	first.release()
	again, err := acquireLifetimeLock(home)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again.release()
}
