//go:build windows

package performance

import (
	"runtime"
	"testing"
)

func TestDefaultNofileLimitOnWindows(t *testing.T) {
	soft, hard, err := defaultNofileLimit()
	if err != nil {
		t.Fatalf("defaultNofileLimit error = %v, want nil", err)
	}
	if soft == 0 || hard == 0 {
		t.Fatalf("defaultNofileLimit returned zero limits: soft=%d, hard=%d", soft, hard)
	}
	if soft > hard {
		t.Fatalf("soft limit %d > hard limit %d", soft, hard)
	}
}

func TestCollectWindowsMeasuresHost(t *testing.T) {
	res, err := collectWindows()
	if err != nil {
		t.Fatalf("collectWindows() failed: %v", err)
	}

	if res.CPUCount <= 0 {
		t.Errorf("res.CPUCount = %d, want > 0", res.CPUCount)
	}
	if res.MemoryTotalBytes == 0 {
		t.Errorf("res.MemoryTotalBytes = 0, want > 0")
	}
	if res.MemoryAvailableBytes == 0 {
		t.Errorf("res.MemoryAvailableBytes = 0, want > 0")
	}
	if res.MemoryAvailableBytes > res.MemoryTotalBytes {
		t.Errorf("available memory %d > total memory %d", res.MemoryAvailableBytes, res.MemoryTotalBytes)
	}
	if res.PageSizeBytes == 0 {
		t.Errorf("res.PageSizeBytes = 0, want > 0")
	}
	if res.NoFileSoft == 0 || res.NoFileHard == 0 {
		t.Errorf("nofile limits soft=%d, hard=%d, want > 0", res.NoFileSoft, res.NoFileHard)
	}
	if res.Hostname == "" {
		t.Errorf("res.Hostname is empty")
	}
	if res.Arch != runtime.GOARCH {
		t.Errorf("res.Arch = %q, want %q", res.Arch, runtime.GOARCH)
	}
	if res.User == "" {
		t.Errorf("res.User is empty")
	}

	if err := res.Validate(); err != nil {
		t.Fatalf("res.Validate() failed: %v", err)
	}
}

func TestCollectOnWindows(t *testing.T) {
	res, err := Collect()
	if err != nil {
		t.Fatalf("Collect() failed on Windows: %v", err)
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("res.Validate() failed: %v", err)
	}
}
