package wsl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	out   []byte
	err   error
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	return f.out, f.err
}

func TestParseDistroList(t *testing.T) {
	raw := []byte("\xff\xfeU\x00b\x00u\x00n\x00t\x00u\x00\r\x00\n\x00D\x00e\x00b\x00i\x00a\x00n\x00\r\x00\n\x00")
	got := ParseDistroList(raw)
	if len(got) != 2 || got[0] != "Ubuntu" || got[1] != "Debian" {
		t.Fatalf("distros = %#v", got)
	}
	if got := ParseDistroList([]byte("devstack\r\n\r\n")); len(got) != 1 || got[0] != "devstack" {
		t.Fatalf("utf8 distros = %#v", got)
	}
}

func TestDistroPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
		err  bool
	}{
		{`C:\Users\Aram\app`, "/mnt/c/Users/Aram/app", false},
		{`c:/Users/Aram/app`, "/mnt/c/Users/Aram/app", false},
		{`\\wsl.localhost\Ubuntu\home\aram\app`, "/home/aram/app", false},
		{`//wsl.localhost/Ubuntu/home/aram/app`, "/home/aram/app", false},
		{`\\wsl$\Ubuntu\home\aram\app`, "/home/aram/app", false},
		{`//wsl$/Ubuntu/home/aram/app`, "/home/aram/app", false},
		{`\\server\share\app`, "", true},
		{`app`, "", true},
	}
	for _, tt := range tests {
		got, err := DistroPath(tt.in)
		if tt.err {
			if err == nil {
				t.Fatalf("DistroPath(%q) = %q, want error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Fatalf("DistroPath(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestValidateWSLPathUsesRunner(t *testing.T) {
	runner := &fakeRunner{out: []byte("/home/aram/app\n")}
	got, err := ValidateWSLPath(context.Background(), runner, "Ubuntu", "/home/aram/app")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/home/aram/app" || !got.Exists {
		t.Fatalf("validated = %+v", got)
	}
	if len(runner.calls) != 1 || runner.calls[0][0] != "-d" || runner.calls[0][1] != "Ubuntu" {
		t.Fatalf("runner calls = %#v", runner.calls)
	}
}

func TestValidateWSLPathRejectsRunnerFailure(t *testing.T) {
	runner := &fakeRunner{err: errors.New("exit 5")}
	if _, err := ValidateWSLPath(context.Background(), runner, "Ubuntu", "/home/a"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("error = %v, want ErrInvalidPath", err)
	}
}

func TestProbeLocalhostForwarding(t *testing.T) {
	runner := &fakeRunner{out: []byte("ok\n")}
	if err := ProbeLocalhostForwarding(context.Background(), runner, "Ubuntu", 8080); err != nil {
		t.Fatal(err)
	}
	runner.err = errors.New("refused")
	if err := ProbeLocalhostForwarding(context.Background(), runner, "Ubuntu", 8080); !errors.Is(err, ErrLocalhostForwarding) {
		t.Fatalf("error = %v, want ErrLocalhostForwarding", err)
	}
}

func TestCopySourceLocal(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(filepath.Join(src, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "public", "index.php"), []byte("<?php echo 'ok';"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(base, "dst")
	result, err := CopySource(context.Background(), src, dst, "local", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Bytes == 0 || len(result.SHA256) != 64 {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(dst, "public", "index.php"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "<?php echo 'ok';" {
		t.Fatalf("copied data = %q", data)
	}
}

func TestCopySourceRejectsSymlinkAndNonEmptyDestination(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(src, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := CopySource(context.Background(), src, filepath.Join(base, "dst"), "local", false); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("symlink error = %v, want ErrInvalidPath", err)
	}

	plain := filepath.Join(base, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "a"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(base, "nonempty")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "b"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CopySource(context.Background(), plain, dst, "local", false); !errors.Is(err, ErrDestinationNotEmpty) {
		t.Fatalf("nonempty destination error = %v", err)
	}
}

func TestCopySourceRejectsOverlappingTrees(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(filepath.Join(src, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "public", "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{
		filepath.Join(src, "nested"),
		src,
		base,
	} {
		if _, err := CopySource(context.Background(), src, destination, "local", false); !errors.Is(err, ErrOverlappingCopy) {
			t.Fatalf("CopySource(%q) error = %v, want ErrOverlappingCopy", destination, err)
		}
	}
	if _, err := os.Stat(filepath.Join(src, "public", "index.php")); err != nil {
		t.Fatalf("source was modified by a rejected overlapping copy: %v", err)
	}
}

func TestCopySourceRefusesAnInProgressDestination(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(base, "dst")
	lockPath := filepath.Join(base, "."+filepath.Base(dst)+".copy.lock")
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CopySource(context.Background(), src, dst, "local", false); !errors.Is(err, ErrDestinationInUse) {
		t.Fatalf("CopySource error = %v, want ErrDestinationInUse", err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused copy created the destination")
	}
}

func TestCopySourceManifestIsStableAndVerifiesContent(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(filepath.Join(src, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a", "one.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "two.txt"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := CopySource(context.Background(), src, filepath.Join(base, "dst-one"), "local", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CopySource(context.Background(), src, filepath.Join(base, "dst-two"), "local", false)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatalf("manifest is not deterministic: %s != %s", first.SHA256, second.SHA256)
	}
	if first.Files != 2 || first.Bytes != 6 {
		t.Fatalf("copy result = %+v", first)
	}
	info, err := os.Stat(filepath.Join(base, "dst-one", "two.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestExecRunnerRejectsEmptyArgs(t *testing.T) {
	if _, err := (ExecRunner{}).Run(context.Background()); err == nil {
		t.Fatal("Run accepted no args")
	}
}
