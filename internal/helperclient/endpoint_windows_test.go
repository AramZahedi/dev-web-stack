//go:build windows

package helperclient

import "testing"

func TestDialNamedPipeEndpoint(t *testing.T) {
	c, err := Dial(`\\.\pipe\devstack-helper-test`)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
}

func TestDialRejectsInvalidEndpoint(t *testing.T) {
	if _, err := Dial("relative-path"); err == nil {
		t.Fatal("Dial accepted a relative helper endpoint")
	}
}
