package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func testRuntimeArchive(t *testing.T) []byte {
	t.Helper()
	return testRuntimeArchiveWith(t, func(tw *tar.Writer) {
		payload := []byte("#!/bin/sh\necho php\n")
		if err := tw.WriteHeader(&tar.Header{
			Name: "php-8.4.0/bin/php", Mode: 0o755, Size: int64(len(payload)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(payload); err != nil {
			t.Fatal(err)
		}
	})
}

func testRuntimeArchiveWith(t *testing.T, write func(*tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write(tw)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDownloadAndExtractArchiveRequiresAndVerifiesChecksum(t *testing.T) {
	archive := testRuntimeArchive(t)
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "runtime")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := downloadAndExtractArchive(context.Background(), server.URL+"/php.tar.gz", digest, dest); err != nil {
		t.Fatalf("downloadAndExtractArchive = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "bin", "php"))
	if err != nil || string(data) != "#!/bin/sh\necho php\n" {
		t.Fatalf("extracted php = %q, %v", data, err)
	}
}

func TestDownloadAndExtractArchiveFailsClosed(t *testing.T) {
	archive := testRuntimeArchive(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	for _, tc := range []struct {
		name string
		hash string
	}{
		{name: "missing hash", hash: ""},
		{name: "short hash", hash: "abcd"},
		{name: "wrong hash", hash: string(bytes.Repeat([]byte("0"), 64))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "runtime")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := downloadAndExtractArchive(context.Background(), server.URL+"/php.tar.gz", tc.hash, dest); err == nil {
				t.Fatal("downloadAndExtractArchive accepted an unverified archive")
			}
			entries, err := os.ReadDir(dest)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("failed download left %d entries in destination", len(entries))
			}
		})
	}
}

func TestDownloadAndExtractArchiveRejectsUnsafeMembers(t *testing.T) {
	cases := []struct {
		name  string
		write func(*tar.Writer)
	}{
		{
			name: "parent traversal",
			write: func(tw *tar.Writer) {
				payload := []byte("x")
				_ = tw.WriteHeader(&tar.Header{Name: "../../evil", Mode: 0o644, Size: int64(len(payload)), Typeflag: tar.TypeReg})
				_, _ = tw.Write(payload)
			},
		},
		{
			name: "absolute path",
			write: func(tw *tar.Writer) {
				payload := []byte("x")
				_ = tw.WriteHeader(&tar.Header{Name: "/etc/passwd", Mode: 0o644, Size: int64(len(payload)), Typeflag: tar.TypeReg})
				_, _ = tw.Write(payload)
			},
		},
		{
			name: "escaping symlink",
			write: func(tw *tar.Writer) {
				_ = tw.WriteHeader(&tar.Header{Name: "php-8.4.0/bin/php", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "../../../etc/passwd"})
			},
		},
		{
			name: "absolute symlink",
			write: func(tw *tar.Writer) {
				_ = tw.WriteHeader(&tar.Header{Name: "php-8.4.0/bin/php", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"})
			},
		},
		{
			name: "hardlink",
			write: func(tw *tar.Writer) {
				_ = tw.WriteHeader(&tar.Header{Name: "php-8.4.0/bin/php", Typeflag: tar.TypeLink, Linkname: "php-8.4.0/bin/sh"})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			archive := testRuntimeArchiveWith(t, tc.write)
			sum := sha256.Sum256(archive)
			digest := hex.EncodeToString(sum[:])
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(archive)
			}))
			defer server.Close()

			dest := filepath.Join(t.TempDir(), "runtime")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := downloadAndExtractArchive(context.Background(), server.URL+"/php.tar.gz", digest, dest); err == nil {
				t.Fatal("downloadAndExtractArchive accepted an unsafe archive")
			}
			entries, err := os.ReadDir(dest)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("unsafe archive left %d entries in the destination", len(entries))
			}
		})
	}
}

func TestValidateRuntimeArchiveAllowsContainedRelativeSymlink(t *testing.T) {
	archive := testRuntimeArchiveWith(t, func(tw *tar.Writer) {
		payload := []byte("#!/bin/sh\n")
		if err := tw.WriteHeader(&tar.Header{Name: "node-v24/bin/node", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: "node-v24/bin/npm", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "../lib/node_modules/npm/bin/npm-cli.js"}); err != nil {
			t.Fatal(err)
		}
	})
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRuntimeArchive(path); err != nil {
		t.Fatalf("validateRuntimeArchive rejected a contained symlink: %v", err)
	}
}
