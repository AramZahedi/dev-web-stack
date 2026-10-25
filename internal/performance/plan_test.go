package performance

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestApplyPlanPathsBackupsAndReset(t *testing.T) {
	root := t.TempDir()
	v := mustCompute(t, testResources(), Balanced, "a", "b")
	at := time.Date(2026, 10, 25, 12, 34, 56, 0, time.UTC)
	prior := PriorHost{
		Sysctl: PriorFile{Existed: true, Content: []byte("fs.file-max = 1000\n"), Mode: 0o644},
		Limits: PriorFile{Existed: false},
	}

	apply, reset, err := BuildPlans(root, v, at, prior)
	if err != nil {
		t.Fatalf("BuildPlans: %v", err)
	}

	wantBackupDir := filepath.Join(root, "backups", "performance", "20261025T123456.000000000Z")
	if apply.BackupDir != wantBackupDir || reset.BackupDir != wantBackupDir {
		t.Fatalf("backup dir = %q/%q, want %q", apply.BackupDir, reset.BackupDir, wantBackupDir)
	}

	wantFiles := map[string]bool{
		filepath.Join(root, "services", "nginx", "config", "conf.d", "performance", "main.conf"):   true,
		filepath.Join(root, "services", "nginx", "config", "conf.d", "performance", "events.conf"): true,
		filepath.Join(root, "services", "nginx", "config", "conf.d", "performance", "http.conf"):   true,
		filepath.Join(root, "services", "apache", "config", "conf.d", "performance.conf"):          true,
		filepath.Join(root, "services", "php", "performance", "a.conf"):                            true,
		filepath.Join(root, "services", "php", "performance", "b.conf"):                            true,
	}
	if len(apply.Files) != len(wantFiles) {
		t.Fatalf("apply files = %d, want %d: %+v", len(apply.Files), len(wantFiles), apply.Files)
	}
	for _, file := range apply.Files {
		if !wantFiles[file.Path] {
			t.Fatalf("unexpected apply file path %q", file.Path)
		}
		if file.Mode != 0o644 {
			t.Fatalf("%s mode = %o, want 0644", file.Path, file.Mode)
		}
		if len(file.Content) == 0 {
			t.Fatalf("%s has no content", file.Path)
		}
	}
	if len(apply.HostFiles) != 2 {
		t.Fatalf("host files = %d, want 2", len(apply.HostFiles))
	}
	if apply.HostFiles[0].Path != SysctlTarget || apply.HostFiles[1].Path != LimitsTarget {
		t.Fatalf("host targets = %q/%q", apply.HostFiles[0].Path, apply.HostFiles[1].Path)
	}
	if len(apply.Backups) != 1 {
		t.Fatalf("backups = %+v, want only the existing sysctl file", apply.Backups)
	}
	backup := apply.Backups[0]
	if backup.Source != SysctlTarget {
		t.Fatalf("backup source = %q, want %q", backup.Source, SysctlTarget)
	}
	wantBackupPath := filepath.Join(wantBackupDir, "etc", "sysctl.d", "99-devstack-performance.conf")
	if backup.Path != wantBackupPath {
		t.Fatalf("backup path = %q, want %q", backup.Path, wantBackupPath)
	}
	if !bytes.Equal(backup.Content, prior.Sysctl.Content) {
		t.Fatalf("backup content = %q, want %q", backup.Content, prior.Sysctl.Content)
	}
	if backup.Mode != 0o644 {
		t.Fatalf("backup mode = %o", backup.Mode)
	}

	if len(reset.Files) != len(wantFiles) {
		t.Fatalf("reset files = %d, want %d", len(reset.Files), len(wantFiles))
	}
	for _, file := range reset.Files {
		text := string(file.Content)
		if strings.Contains(text, "worker_processes") || strings.Contains(text, "pm.max_children") ||
			strings.Contains(text, "fs.file-max") || strings.Contains(text, "ListenBacklog") {
			t.Fatalf("reset file %s still contains tuning:\n%s", file.Path, text)
		}
		if !strings.Contains(text, "reset") {
			t.Fatalf("reset file %s missing reset marker:\n%s", file.Path, text)
		}
	}
	if len(reset.RemoveHost) != 1 || reset.RemoveHost[0] != LimitsTarget {
		t.Fatalf("RemoveHost = %v, want [%s]", reset.RemoveHost, LimitsTarget)
	}
	if len(reset.RestoreHost) != 1 || reset.RestoreHost[0].Path != SysctlTarget {
		t.Fatalf("RestoreHost = %+v, want sysctl restore", reset.RestoreHost)
	}
	if !bytes.Equal(reset.RestoreHost[0].Content, prior.Sysctl.Content) {
		t.Fatalf("restore content = %q, want %q", reset.RestoreHost[0].Content, prior.Sysctl.Content)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("planning wrote files under root: %v", entries)
	}
}

func TestPlansAreDeterministic(t *testing.T) {
	root := t.TempDir()
	v := mustCompute(t, highResources(), Throughput, "b", "a")
	at := time.Date(2026, 10, 25, 1, 2, 3, 0, time.FixedZone("x", 3600))
	prior := PriorHost{
		Sysctl: PriorFile{Existed: true, Content: []byte("a = b\n"), Mode: 0o600},
		Limits: PriorFile{Existed: true, Content: []byte("c = d\n"), Mode: 0o640},
	}
	firstApply, firstReset, err := BuildPlans(root, v, at, prior)
	if err != nil {
		t.Fatal(err)
	}
	secondApply, secondReset, err := BuildPlans(root, v, at, prior)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstApply, secondApply) {
		t.Fatal("apply plan is not deterministic")
	}
	if !reflect.DeepEqual(firstReset, secondReset) {
		t.Fatal("reset plan is not deterministic")
	}
	if !strings.Contains(firstApply.BackupDir, "20261025T000203.000000000Z") {
		t.Fatalf("backup dir = %q, want UTC timestamp", firstApply.BackupDir)
	}
	if len(firstApply.Backups) != 2 {
		t.Fatalf("backups = %d, want 2", len(firstApply.Backups))
	}
	if firstApply.Backups[0].Mode != 0o600 || firstApply.Backups[1].Mode != 0o640 {
		t.Fatalf("backup modes = %o/%o", firstApply.Backups[0].Mode, firstApply.Backups[1].Mode)
	}
}

func TestPlanValidation(t *testing.T) {
	v := mustCompute(t, testResources(), Balanced, "a")
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	if _, _, err := BuildPlans("relative", v, at, PriorHost{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative root error = %v, want ErrInvalid", err)
	}
	if _, _, err := BuildPlans("", v, at, PriorHost{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty root error = %v, want ErrInvalid", err)
	}
	root := t.TempDir()
	if _, _, err := BuildPlans(root, v, time.Time{}, PriorHost{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero timestamp error = %v, want ErrInvalid", err)
	}
	bad := PriorHost{Sysctl: PriorFile{Existed: false, Content: []byte("stale")}}
	if _, _, err := BuildPlans(root, v, at, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("absent prior file with content error = %v, want ErrInvalid", err)
	}
	if _, _, err := BuildPlans(root, Values{}, at, PriorHost{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid values error = %v, want ErrInvalid", err)
	}
}

func TestResetWithoutPriorFilesRemovesHostTuningOnly(t *testing.T) {
	root := t.TempDir()
	v := mustCompute(t, lowResources(), Balanced, "a")
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	_, reset, err := BuildPlans(root, v, at, PriorHost{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{SysctlTarget, LimitsTarget}
	if !reflect.DeepEqual(reset.RemoveHost, want) {
		t.Fatalf("RemoveHost = %v, want %v", reset.RemoveHost, want)
	}
	if len(reset.RestoreHost) != 0 {
		t.Fatalf("RestoreHost = %+v, want none", reset.RestoreHost)
	}
}

func TestApplyPlanCopiesPriorContent(t *testing.T) {
	root := t.TempDir()
	v := mustCompute(t, testResources(), Balanced)
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	content := []byte("original\n")
	prior := PriorHost{Sysctl: PriorFile{Existed: true, Content: content}}
	apply, _, err := BuildPlans(root, v, at, prior)
	if err != nil {
		t.Fatal(err)
	}
	content[0] = 'X'
	if apply.Backups[0].Content[0] == 'X' {
		t.Fatal("backup content aliases caller memory")
	}
}

func TestPlanWrappers(t *testing.T) {
	root := t.TempDir()
	v := mustCompute(t, testResources(), Balanced, "a")
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	apply, err := BuildApplyPlan(root, v, at, PriorHost{})
	if err != nil {
		t.Fatalf("BuildApplyPlan: %v", err)
	}
	if len(apply.Files) == 0 || len(apply.HostFiles) != 2 {
		t.Fatalf("apply plan = %+v", apply)
	}
	reset, err := BuildResetPlan(root, v, at, PriorHost{})
	if err != nil {
		t.Fatalf("BuildResetPlan: %v", err)
	}
	if len(reset.Files) == 0 || len(reset.RemoveHost) != 2 {
		t.Fatalf("reset plan = %+v", reset)
	}
}

func TestPlansMarkStalePHPFragments(t *testing.T) {
	root := t.TempDir()
	v := mustCompute(t, testResources(), Balanced, "a", "b")
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	perfDir := filepath.Join(root, "services", "php", "performance")
	existing := []string{
		filepath.Join(perfDir, "a.conf"),
		filepath.Join(perfDir, "b.conf"),
		filepath.Join(perfDir, "stale.conf"),
	}
	apply, reset, err := BuildPlans(root, v, at, PriorHost{}, existing...)
	if err != nil {
		t.Fatalf("BuildPlans: %v", err)
	}
	want := []string{filepath.Join(perfDir, "stale.conf")}
	if !reflect.DeepEqual(apply.Stale, want) {
		t.Fatalf("apply stale = %v, want %v", apply.Stale, want)
	}
	if !reflect.DeepEqual(reset.Stale, want) {
		t.Fatalf("reset stale = %v, want %v", reset.Stale, want)
	}
}

func TestPlansRejectExistingFragmentOutsidePerformanceDir(t *testing.T) {
	root := t.TempDir()
	v := mustCompute(t, testResources(), Balanced, "a")
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	outside := filepath.Join(root, "services", "php", "stale.conf")
	if _, _, err := BuildPlans(root, v, at, PriorHost{}, outside); !errors.Is(err, ErrInvalid) {
		t.Fatalf("BuildPlans(outside fragment) error = %v, want ErrInvalid", err)
	}
	if _, _, err := BuildPlans(root, v, at, PriorHost{}, "relative.conf"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("BuildPlans(relative fragment) error = %v, want ErrInvalid", err)
	}
}
