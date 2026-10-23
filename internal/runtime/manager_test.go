package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

var _ backend.RuntimeProvider = (*Manager)(nil)

const (
	testVersion = "24.99.0"
	testID      = "node@" + testVersion
	testBuild   = "linux-x64"
	testArchive = "node-v24.99.0-linux-x64.tar.gz"
)

func TestInstallVerifiesChecksumAndExtractsSafely(t *testing.T) {
	archive := fixtureData(t, "valid.tar.gz")
	srv := newFixtureServer(t, archive)
	entry := entryFor(t, archive, srv.URL+"/"+testArchive)
	m, st, home, _ := newManagerFor(t, entry, srv.Client())

	build, err := m.Install(context.Background(), testID)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	wantPath := filepath.Join(home, "runtimes", "node", testVersion+"-"+testBuild)
	if build.Path != wantPath {
		t.Errorf("build.Path = %q, want %q", build.Path, wantPath)
	}
	if build.ID != testID || build.Kind != "node" || build.Version != testVersion {
		t.Errorf("unexpected build identity: %+v", build)
	}
	if build.Backend != model.BackendLocal {
		t.Errorf("build.Backend = %q, want local", build.Backend)
	}
	if build.Checksum != entry.SHA256 {
		t.Errorf("build.Checksum = %q, want %q", build.Checksum, entry.SHA256)
	}
	if build.InstalledAt.IsZero() {
		t.Error("build.InstalledAt is zero")
	}

	info, err := os.Stat(filepath.Join(build.Path, "bin", "node"))
	if err != nil {
		t.Fatalf("stat installed bin/node: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("bin/node mode = %04o, want executable", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(build.Path, manifestName)); err != nil {
		t.Fatalf("stat manifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(build.Path, "node-v"+testVersion+"-linux-x64")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("wrapper directory was not stripped: %v", err)
	}

	builds, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(builds) != 1 || builds[0].ID != testID {
		t.Fatalf("List = %+v, want one %s", builds, testID)
	}
	got, err := st.GetRuntime(context.Background(), testID)
	if err != nil {
		t.Fatalf("GetRuntime: %v", err)
	}
	if got.Path != build.Path {
		t.Errorf("stored path = %q, want %q", got.Path, build.Path)
	}
	if err := m.Verify(context.Background(), testID); err != nil {
		t.Fatalf("Verify after install: %v", err)
	}

	assertNoPartialOrTempFiles(t, home)
}

func TestChecksumMismatchLeavesNoRuntime(t *testing.T) {
	archive := fixtureData(t, "valid.tar.gz")
	corrupt := append([]byte(nil), archive...)
	corrupt[len(corrupt)-1] ^= 0xff
	srv := newFixtureServer(t, corrupt)
	entry := entryFor(t, archive, srv.URL+"/"+testArchive)
	m, _, home, _ := newManagerFor(t, entry, srv.Client())

	_, err := m.Install(context.Background(), testID)
	if err == nil {
		t.Fatal("Install accepted a tarball with the wrong checksum")
	}
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Install error = %v, want ErrChecksumMismatch", err)
	}

	builds, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(builds) != 0 {
		t.Fatalf("runtime registered after checksum failure: %+v", builds)
	}
	if _, err := os.Stat(filepath.Join(home, "runtimes", "node", testVersion+"-"+testBuild)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("target directory exists after checksum failure: %v", err)
	}
	assertNoPartialOrTempFiles(t, home)
}

func TestExtractionLimits(t *testing.T) {
	tests := []struct {
		name   string
		limits archiveLimits
	}{
		{
			name:   "entry count",
			limits: archiveLimits{maxEntries: 2, maxBytes: defaultArchiveLimits.maxBytes},
		},
		{
			name:   "expanded bytes",
			limits: archiveLimits{maxEntries: defaultArchiveLimits.maxEntries, maxBytes: 8},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, _, home, _ := newManagerFor(t, offlineEntry(t), nil)
			m.limits = test.limits

			_, err := m.InstallFile(context.Background(), testID, filepath.Join("testdata", "valid.tar.gz"))
			if err == nil || !errors.Is(err, ErrUnsafeArchive) {
				t.Fatalf("InstallFile error = %v, want ErrUnsafeArchive", err)
			}
			if builds, _ := m.List(context.Background()); len(builds) != 0 {
				t.Fatalf("runtime registered after limit failure: %+v", builds)
			}
			if _, err := os.Stat(filepath.Join(home, "runtimes", "node", testVersion+"-"+testBuild)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("target directory exists after limit failure: %v", err)
			}
			assertNoPartialOrTempFiles(t, home)
		})
	}
}

func TestExtractionRejectsTraversalAndSymlink(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
	}{
		{name: "parent traversal", fixture: "traversal.tar.gz"},
		{name: "absolute path", fixture: "absolute.tar.gz"},
		{name: "absolute symlink", fixture: "symlink.tar.gz"},
		{name: "escaping symlink", fixture: "symlink-escape.tar.gz"},
		{name: "ancestor symlink", fixture: "symlink-ancestor.tar.gz"},
		{name: "symlink chain escapes", fixture: "symlink-chain.tar.gz"},
		{name: "write through symlink chain", fixture: "symlink-chain-write.tar.gz"},
		{name: "wrapper-relative symlink escapes after strip", fixture: "wrapped-symlink-escape.tar.gz"},
		{name: "hardlink", fixture: "hardlink.tar.gz"},
		{name: "device", fixture: "device.tar.gz"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := fixtureData(t, test.fixture)
			srv := newFixtureServer(t, archive)
			entry := entryFor(t, archive, srv.URL+"/"+testArchive)
			m, _, home, _ := newManagerFor(t, entry, srv.Client())

			_, err := m.Install(context.Background(), testID)
			if err == nil {
				t.Fatal("Install accepted an unsafe archive")
			}
			if !errors.Is(err, ErrUnsafeArchive) {
				t.Fatalf("Install error = %v, want ErrUnsafeArchive", err)
			}
			builds, listErr := m.List(context.Background())
			if listErr != nil {
				t.Fatalf("List: %v", listErr)
			}
			if len(builds) != 0 {
				t.Fatalf("runtime registered after unsafe archive: %+v", builds)
			}
			if matches := filesNamed(t, home, "escape.txt"); len(matches) != 0 {
				t.Fatalf("traversal wrote outside the extraction root: %v", matches)
			}
			if matches := filesNamed(t, home, "devstack-absolute-escape.txt"); len(matches) != 0 {
				t.Fatalf("absolute path was written: %v", matches)
			}
			assertNoPartialOrTempFiles(t, home)
		})
	}
}

func TestExtractionRejectsSymlinkChainWithoutWritingOutside(t *testing.T) {
	for _, fixture := range []string{"symlink-chain.tar.gz", "symlink-chain-write.tar.gz"} {
		t.Run(fixture, func(t *testing.T) {
			archive := fixtureData(t, fixture)
			entry := entryFor(t, archive, "https://nodejs.org/dist/v"+testVersion+"/"+testArchive)
			m, _, home, _ := newManagerFor(t, entry, nil)

			_, err := m.InstallFile(context.Background(), testID, filepath.Join("testdata", fixture))
			if err == nil || !errors.Is(err, ErrUnsafeArchive) {
				t.Fatalf("InstallFile error = %v, want ErrUnsafeArchive", err)
			}
			wantErr := "traverses symlink"
			if fixture == "symlink-chain.tar.gz" {
				// The chain-only fixture has no file entry under the link, so
				// this failure must come from the post-extraction chain check.
				wantErr = "target escapes"
			}
			if !strings.Contains(err.Error(), wantErr) {
				t.Fatalf("InstallFile error = %q, want it to contain %q", err, wantErr)
			}
			if builds, _ := m.List(context.Background()); len(builds) != 0 {
				t.Fatalf("runtime registered after symlink chain: %+v", builds)
			}
			if _, err := os.Stat(filepath.Join(home, "runtimes", "node", testVersion+"-"+testBuild)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("target directory exists after symlink chain: %v", err)
			}
			pwned := filepath.Join(filepath.Dir(home), "outside", "pwned.txt")
			if _, err := os.Stat(pwned); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("extraction wrote through the symlink chain: %v", err)
			}
			assertNoPartialOrTempFiles(t, home)
		})
	}
}

func TestInstallAcceptsSafeRelativeSymlink(t *testing.T) {
	archive := fixtureData(t, "symlink-safe.tar.gz")
	entry := entryFor(t, archive, "https://nodejs.org/dist/v"+testVersion+"/"+testArchive)
	m, _, _, _ := newManagerFor(t, entry, nil)

	build, err := m.InstallFile(context.Background(), testID, filepath.Join("testdata", "symlink-safe.tar.gz"))
	if err != nil {
		t.Fatalf("InstallFile: %v", err)
	}
	link := filepath.Join(build.Path, "bin", "npm")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat bin/npm: %v", err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("bin/npm mode = %v, want symlink", info.Mode())
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink bin/npm: %v", err)
	}
	if target != "../lib/npm.js" {
		t.Fatalf("bin/npm target = %q, want ../lib/npm.js", target)
	}
	if err := m.Verify(context.Background(), testID); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// Replacing the managed link with one that points outside must fail
	// verification.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	err = m.Verify(context.Background(), testID)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "symlink") {
		t.Fatalf("Verify after symlink tampering = %v, want symlink error", err)
	}
}

func TestWrapperDirectoryStripsSymlinkTargets(t *testing.T) {
	archive := fixtureData(t, "wrapped-symlink.tar.gz")
	entry := entryFor(t, archive, "https://nodejs.org/dist/v"+testVersion+"/"+testArchive)
	m, _, _, _ := newManagerFor(t, entry, nil)

	build, err := m.InstallFile(context.Background(), testID, filepath.Join("testdata", "wrapped-symlink.tar.gz"))
	if err != nil {
		t.Fatalf("InstallFile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(build.Path, "bin", "node")); err != nil {
		t.Fatalf("stat bin/node: %v", err)
	}
	if _, err := os.Stat(filepath.Join(build.Path, "node-v"+testVersion+"-linux-x64")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("wrapper directory survived: %v", err)
	}
	target, err := os.Readlink(filepath.Join(build.Path, "bin", "npm"))
	if err != nil {
		t.Fatalf("readlink bin/npm: %v", err)
	}
	if target != "../lib/npm.js" {
		t.Fatalf("bin/npm target = %q, want ../lib/npm.js", target)
	}
	if err := m.Verify(context.Background(), testID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestWrapperDirectoryIsStrippedOnlyForWrappers(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
	}{
		{name: "standard roots kept", fixture: "valid.tar.gz"},
		{name: "single standard root kept", fixture: "binonly.tar.gz"},
		{name: "node wrapper stripped", fixture: "wrapped.tar.gz"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := fixtureData(t, test.fixture)
			entry := entryFor(t, archive, "https://nodejs.org/dist/v"+testVersion+"/"+testArchive)
			m, _, _, _ := newManagerFor(t, entry, nil)

			build, err := m.InstallFile(context.Background(), testID, filepath.Join("testdata", test.fixture))
			if err != nil {
				t.Fatalf("InstallFile: %v", err)
			}
			if _, err := os.Stat(filepath.Join(build.Path, "bin", "node")); err != nil {
				t.Fatalf("stat bin/node: %v", err)
			}
			wrapper := filepath.Join(build.Path, "node-v"+testVersion+"-linux-x64")
			if _, statErr := os.Stat(wrapper); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("wrapper directory survived extraction: %v", statErr)
			}

			var manifest manifest
			if err := readJSONFile(filepath.Join(build.Path, manifestName), &manifest); err != nil {
				t.Fatalf("read manifest: %v", err)
			}
			for _, file := range manifest.Files {
				if strings.HasPrefix(file.Path, "node-v") {
					t.Errorf("manifest contains wrapper path %q", file.Path)
				}
			}
		})
	}
}

func TestInstallFileOfflineAndIdempotent(t *testing.T) {
	archive := fixtureData(t, "valid.tar.gz")
	entry := entryFor(t, archive, "https://nodejs.org/dist/v"+testVersion+"/"+testArchive)
	m, _, _, _ := newManagerFor(t, entry, nil)
	source := filepath.Join("testdata", "valid.tar.gz")

	build, err := m.InstallFile(context.Background(), testID, source)
	if err != nil {
		t.Fatalf("InstallFile: %v", err)
	}
	if err := m.Verify(context.Background(), testID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	builds, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(builds) != 1 {
		t.Fatalf("List returned %d builds, want 1", len(builds))
	}

	// A second offline install of the same id is a no-op even when the source
	// file no longer exists.
	again, err := m.InstallFile(context.Background(), testID, filepath.Join(t.TempDir(), "missing.tar.gz"))
	if err != nil {
		t.Fatalf("second InstallFile: %v", err)
	}
	if again.Path != build.Path || again.Checksum != build.Checksum {
		t.Fatalf("idempotent install changed the build: %+v", again)
	}
}

func TestInstallFileChecksumMismatchLeavesNoRuntime(t *testing.T) {
	archive := fixtureData(t, "valid.tar.gz")
	entry := entryFor(t, archive, "https://nodejs.org/dist/v"+testVersion+"/"+testArchive)
	m, _, home, _ := newManagerFor(t, entry, nil)

	corrupt := append([]byte(nil), archive...)
	corrupt[len(corrupt)-1] ^= 0xff
	source := filepath.Join(t.TempDir(), testArchive)
	if err := os.WriteFile(source, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := m.InstallFile(context.Background(), testID, source); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("InstallFile error = %v, want ErrChecksumMismatch", err)
	}
	if builds, _ := m.List(context.Background()); len(builds) != 0 {
		t.Fatalf("runtime registered after checksum failure: %+v", builds)
	}
	assertNoPartialOrTempFiles(t, home)
}

func TestVerifyDetectsTampering(t *testing.T) {
	tests := []struct {
		name    string
		tamper  func(t *testing.T, build model.RuntimeBuild)
		wantErr string
	}{
		{
			name: "content tampered",
			tamper: func(t *testing.T, build model.RuntimeBuild) {
				path := filepath.Join(build.Path, "bin", "node")
				if err := os.WriteFile(path, []byte("#!/bin/sh\necho tampered\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "checksum",
		},
		{
			name: "file missing",
			tamper: func(t *testing.T, build model.RuntimeBuild) {
				if err := os.Remove(filepath.Join(build.Path, "lib", "readme.txt")); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "missing",
		},
		{
			name: "extra file",
			tamper: func(t *testing.T, build model.RuntimeBuild) {
				if err := os.WriteFile(filepath.Join(build.Path, "extra.txt"), []byte("extra"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "unexpected",
		},
		{
			name: "mode changed",
			tamper: func(t *testing.T, build model.RuntimeBuild) {
				if err := os.Chmod(filepath.Join(build.Path, "bin", "node"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "mode",
		},
		{
			name: "file replaced by symlink",
			tamper: func(t *testing.T, build model.RuntimeBuild) {
				path := filepath.Join(build.Path, "lib", "readme.txt")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/etc/passwd", path); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "symlink",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, _, _, _ := installValidOffline(t)
			builds, err := m.List(context.Background())
			if err != nil || len(builds) != 1 {
				t.Fatalf("List = %+v, %v; want one build", builds, err)
			}
			if err := m.Verify(context.Background(), testID); err != nil {
				t.Fatalf("Verify before tampering: %v", err)
			}
			test.tamper(t, builds[0])
			err = m.Verify(context.Background(), testID)
			if err == nil {
				t.Fatal("Verify accepted a tampered runtime")
			}
			if !strings.Contains(strings.ToLower(err.Error()), test.wantErr) {
				t.Fatalf("Verify error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestVerifyRejectsSymlinkChainEscape(t *testing.T) {
	m, _, _, _ := installValidOffline(t)
	builds, err := m.List(context.Background())
	if err != nil || len(builds) != 1 {
		t.Fatalf("List = %+v, %v; want one build", builds, err)
	}
	build := builds[0]

	// Build the same two-link chain used by the extraction fixture directly in
	// the installed tree, then record it in the manifest so Verify must apply
	// the same resolution rather than only checking lexical paths.
	chainDir := filepath.Join(build.Path, "d1", "d2", "a")
	if err := os.MkdirAll(chainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../other", filepath.Join(chainDir, "x")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("x/../../../../outside", filepath.Join(chainDir, "y")); err != nil {
		t.Fatal(err)
	}

	var document manifest
	if err := readJSONFile(filepath.Join(build.Path, manifestName), &document); err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	document.Links = append(document.Links,
		manifestLink{Path: "d1/d2/a/x", Target: "../../../other"},
		manifestLink{Path: "d1/d2/a/y", Target: "x/../../../../outside"},
	)
	if err := writeManifest(build.Path, document); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	err = m.Verify(context.Background(), testID)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "target escapes") {
		t.Fatalf("Verify error = %v, want symlink chain escape rejection", err)
	}
}

func TestVerifyDetectsMissingManifest(t *testing.T) {
	m, _, _, _ := installValidOffline(t)
	builds, err := m.List(context.Background())
	if err != nil || len(builds) != 1 {
		t.Fatalf("List: %v", err)
	}
	if err := os.Remove(filepath.Join(builds[0].Path, manifestName)); err != nil {
		t.Fatal(err)
	}
	if err := m.Verify(context.Background(), testID); err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("Verify error = %v, want missing manifest", err)
	}
}

func TestRemoveContainmentAndIdempotency(t *testing.T) {
	t.Run("removes managed build and is idempotent", func(t *testing.T) {
		m, _, _, _ := installValidOffline(t)
		builds, _ := m.List(context.Background())
		build := builds[0]

		if err := m.Remove(context.Background(), testID); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, err := os.Stat(build.Path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("runtime path still exists: %v", err)
		}
		if _, err := m.stores.GetRuntime(context.Background(), testID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetRuntime after Remove = %v, want ErrNotFound", err)
		}
		if err := m.Remove(context.Background(), testID); err != nil {
			t.Fatalf("second Remove = %v, want nil", err)
		}
	})

	t.Run("removes row when files are already absent", func(t *testing.T) {
		entry := offlineEntry(t)
		m, st, home, _ := newManagerFor(t, entry, nil)
		path := filepath.Join(home, "runtimes", "node", testVersion+"-"+testBuild)
		upsertRuntime(t, st, testID, path)

		if err := m.Remove(context.Background(), testID); err != nil {
			t.Fatalf("Remove with absent files = %v, want nil", err)
		}
		if _, err := st.GetRuntime(context.Background(), testID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("row still exists after Remove: %v", err)
		}
	})

	t.Run("refuses kind directory", func(t *testing.T) {
		entry := offlineEntry(t)
		m, st, home, _ := newManagerFor(t, entry, nil)
		kindDir := filepath.Join(home, "runtimes", "node")
		if err := os.MkdirAll(kindDir, 0o755); err != nil {
			t.Fatal(err)
		}
		victim := filepath.Join(kindDir, "unrelated-build")
		if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		upsertRuntime(t, st, "node@kind-dir", kindDir)

		if err := m.Remove(context.Background(), "node@kind-dir"); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("Remove error = %v, want ErrUnsafePath", err)
		}
		if _, err := os.Stat(victim); err != nil {
			t.Fatalf("Remove deleted an unrelated managed path: %v", err)
		}
		if _, err := st.GetRuntime(context.Background(), "node@kind-dir"); err != nil {
			t.Fatalf("row was deleted for an invalid path: %v", err)
		}
	})

	t.Run("refuses path for another version", func(t *testing.T) {
		entry := offlineEntry(t)
		m, st, home, _ := newManagerFor(t, entry, nil)
		other := filepath.Join(home, "runtimes", "node", "24.98.0-"+testBuild)
		if err := os.MkdirAll(other, 0o755); err != nil {
			t.Fatal(err)
		}
		victim := filepath.Join(other, "keep.txt")
		if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		upsertRuntime(t, st, "node@other-version", other)

		if err := m.Remove(context.Background(), "node@other-version"); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("Remove error = %v, want ErrUnsafePath", err)
		}
		if _, err := os.Stat(victim); err != nil {
			t.Fatalf("Remove deleted another build's directory: %v", err)
		}
	})

	t.Run("refuses path outside root", func(t *testing.T) {
		entry := offlineEntry(t)
		m, st, home, _ := newManagerFor(t, entry, nil)
		outside := filepath.Join(home, "outside")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		victim := filepath.Join(outside, "keep.txt")
		if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		upsertRuntime(t, st, "node@outside", outside)

		err := m.Remove(context.Background(), "node@outside")
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("Remove error = %v, want ErrUnsafePath", err)
		}
		if _, err := os.Stat(victim); err != nil {
			t.Fatalf("Remove touched a path outside the runtime root: %v", err)
		}
		if _, err := st.GetRuntime(context.Background(), "node@outside"); err != nil {
			t.Fatalf("row was deleted for outside path: %v", err)
		}
	})

	t.Run("refuses runtime root itself", func(t *testing.T) {
		entry := offlineEntry(t)
		m, st, home, _ := newManagerFor(t, entry, nil)
		root := filepath.Join(home, "runtimes")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		upsertRuntime(t, st, "node@root", root)

		if err := m.Remove(context.Background(), "node@root"); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("Remove error = %v, want ErrUnsafePath", err)
		}
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("runtime root was removed: %v", err)
		}
	})

	t.Run("refuses symlinked parent", func(t *testing.T) {
		entry := offlineEntry(t)
		m, st, home, _ := newManagerFor(t, entry, nil)
		runtimes := filepath.Join(home, "runtimes")
		if err := os.MkdirAll(runtimes, 0o755); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(home, "outside-link")
		managed := filepath.Join(outside, testVersion+"-"+testBuild)
		if err := os.MkdirAll(managed, 0o755); err != nil {
			t.Fatal(err)
		}
		victim := filepath.Join(managed, "keep.txt")
		if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		kindDir := filepath.Join(runtimes, "node")
		if err := os.Symlink(outside, kindDir); err != nil {
			t.Fatal(err)
		}
		upsertRuntime(t, st, "node@linked", filepath.Join(kindDir, testVersion+"-"+testBuild))

		if err := m.Remove(context.Background(), "node@linked"); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("Remove error = %v, want ErrUnsafePath", err)
		}
		if _, err := os.Stat(victim); err != nil {
			t.Fatalf("Remove followed a symlinked parent: %v", err)
		}
	})
}

func TestConcurrentInstallsSameID(t *testing.T) {
	archive := fixtureData(t, "valid.tar.gz")
	srv := newFixtureServer(t, archive)
	entry := entryFor(t, archive, srv.URL+"/"+testArchive)
	m1, st, home, cat := newManagerFor(t, entry, srv.Client())
	m2 := newManager(st, home, srv.Client(), cat, nil)

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	builds := make([]model.RuntimeBuild, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := m1
			if i%2 == 1 {
				m = m2
			}
			builds[i], errs[i] = m.Install(context.Background(), testID)
		}(i)
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if builds[i].Path != builds[0].Path {
			t.Fatalf("worker %d path = %q, want %q", i, builds[i].Path, builds[0].Path)
		}
		if builds[i].Checksum != entry.SHA256 {
			t.Fatalf("worker %d checksum = %q", i, builds[i].Checksum)
		}
	}
	if got := srv.requests.Load(); got != 1 {
		t.Fatalf("downloads = %d, want 1", got)
	}
	if list, err := m1.List(context.Background()); err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, %v; want one row", list, err)
	}
	if err := m1.Verify(context.Background(), testID); err != nil {
		t.Fatalf("Verify after concurrent install: %v", err)
	}
	assertNoPartialOrTempFiles(t, home)
}

func TestInstallKeepsPHPRevisionsSeparate(t *testing.T) {
	stubPHPSmoke(t)
	archive := fixtureData(t, "valid.tar.gz")
	srv := newAnyFixtureServer(t, archive)
	cat := catalogFor(t,
		phpEntryFor(t, archive, srv.URL+"/php-8.4.4-r1-linux-x64.tar.gz", 1),
		phpEntryFor(t, archive, srv.URL+"/php-8.4.4-r2-linux-x64.tar.gz", 2))
	m, _, _, _ := newManagerWithCatalogFor(t, cat, srv.Client())

	b1, err := m.Install(context.Background(), "php@8.4.4-r1")
	if err != nil {
		t.Fatal(err)
	}
	b2, err := m.Install(context.Background(), "php@8.4.4-r2")
	if err != nil {
		t.Fatal(err)
	}
	if b1.Path == b2.Path {
		t.Fatalf("both revisions installed at %s", b1.Path)
	}
	if filepath.Base(b1.Path) != "8.4.4-r1-linux-x64" {
		t.Fatalf("path = %s", b1.Path)
	}
	if filepath.Base(b2.Path) != "8.4.4-r2-linux-x64" {
		t.Fatalf("path = %s", b2.Path)
	}
	if err := m.Verify(context.Background(), b1.ID); err != nil {
		t.Fatalf("Verify r1: %v", err)
	}
	if err := m.Verify(context.Background(), b2.ID); err != nil {
		t.Fatalf("Verify r2: %v", err)
	}
}

func TestEnsureBuildInstallsPinnedRevision(t *testing.T) {
	stubPHPSmoke(t)
	archive := fixtureData(t, "valid.tar.gz")
	srv := newAnyFixtureServer(t, archive)
	cat := catalogFor(t,
		phpEntryFor(t, archive, srv.URL+"/php-8.4.4-r1-linux-x64.tar.gz", 1),
		phpEntryFor(t, archive, srv.URL+"/php-8.4.4-r2-linux-x64.tar.gz", 2))
	m, _, _, _ := newManagerWithCatalogFor(t, cat, srv.Client())

	build, err := m.EnsureBuild(context.Background(), "php@8.4.4-r2")
	if err != nil {
		t.Fatalf("EnsureBuild: %v", err)
	}
	if build.ID != "php@8.4.4-r2" || filepath.Base(build.Path) != "8.4.4-r2-linux-x64" {
		t.Fatalf("EnsureBuild = %+v", build)
	}
	again, err := m.EnsureBuild(context.Background(), "php@8.4.4-r2")
	if err != nil {
		t.Fatalf("second EnsureBuild: %v", err)
	}
	if again.Path != build.Path {
		t.Fatalf("second EnsureBuild path = %q, want %q", again.Path, build.Path)
	}
	if got := srv.requests.Load(); got != 1 {
		t.Fatalf("downloads = %d, want 1", got)
	}
	if builds, err := m.List(context.Background()); err != nil || len(builds) != 1 {
		t.Fatalf("List = %+v, %v; want only the pinned revision", builds, err)
	}

	if _, err := m.EnsureBuild(context.Background(), "php@8.4.4-r3"); !errors.Is(err, ErrNoCatalogEntry) {
		t.Fatalf("EnsureBuild unknown revision = %v, want ErrNoCatalogEntry", err)
	}

	if err := os.RemoveAll(build.Path); err != nil {
		t.Fatal(err)
	}
	repaired, err := m.EnsureBuild(context.Background(), "php@8.4.4-r2")
	if err != nil {
		t.Fatalf("EnsureBuild after deletion: %v", err)
	}
	if repaired.Path != build.Path {
		t.Fatalf("repaired path = %q, want %q", repaired.Path, build.Path)
	}
	if got := srv.requests.Load(); got != 2 {
		t.Fatalf("downloads after repair = %d, want 2", got)
	}
}

func TestEnsureBuildPrefersIntactInstalledBuildOverCatalog(t *testing.T) {
	stubPHPSmoke(t)
	archive := fixtureData(t, "valid.tar.gz")
	srv := newAnyFixtureServer(t, archive)
	cat := catalogFor(t, phpEntryFor(t, archive, srv.URL+"/php-8.4.4-r1-linux-x64.tar.gz", 1))
	m, st, home, _ := newManagerWithCatalogFor(t, cat, srv.Client())

	build, err := m.Install(context.Background(), "php@8.4.4-r1")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	// The build is no longer in the catalog of a later release, but the
	// installed row must remain usable for rollback.
	withoutPHP := catalogFor(t, offlineEntry(t))
	m2 := NewManagerWithCatalog(st, home, srv.Client(), withoutPHP)
	retained, err := m2.EnsureBuild(context.Background(), build.ID)
	if err != nil {
		t.Fatalf("EnsureBuild without catalog entry: %v", err)
	}
	if retained.Path != build.Path || retained.Checksum != build.Checksum {
		t.Fatalf("retained build = %+v, want %+v", retained, build)
	}

	// A catalog load failure is irrelevant when the row is already intact.
	m3 := newManager(st, home, srv.Client(), catalog.Catalog{}, errors.New("catalog exploded"))
	retained, err = m3.EnsureBuild(context.Background(), build.ID)
	if err != nil {
		t.Fatalf("EnsureBuild with catalog load failure: %v", err)
	}
	if retained.Path != build.Path {
		t.Fatalf("retained path = %q, want %q", retained.Path, build.Path)
	}
	if got := srv.requests.Load(); got != 1 {
		t.Fatalf("downloads = %d, want 1 (no reinstall)", got)
	}
}

func TestEnsureBuildStaleInstallFallsThroughToCatalog(t *testing.T) {
	stubPHPSmoke(t)
	archive := fixtureData(t, "valid.tar.gz")
	srv := newAnyFixtureServer(t, archive)
	cat := catalogFor(t, phpEntryFor(t, archive, srv.URL+"/php-8.4.4-r1-linux-x64.tar.gz", 1))
	m, st, home, _ := newManagerWithCatalogFor(t, cat, srv.Client())

	build, err := m.Install(context.Background(), "php@8.4.4-r1")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := os.RemoveAll(build.Path); err != nil {
		t.Fatal(err)
	}

	broken := newManager(st, home, srv.Client(), catalog.Catalog{}, errors.New("catalog exploded"))
	if _, err := broken.EnsureBuild(context.Background(), build.ID); err == nil ||
		!strings.Contains(err.Error(), "catalog exploded") {
		t.Fatalf("EnsureBuild stale row with catalog failure = %v, want catalog error", err)
	}

	repaired, err := m.EnsureBuild(context.Background(), build.ID)
	if err != nil {
		t.Fatalf("EnsureBuild repair: %v", err)
	}
	if repaired.Path != build.Path {
		t.Fatalf("repaired path = %q, want %q", repaired.Path, build.Path)
	}
	if err := m.Verify(context.Background(), build.ID); err != nil {
		t.Fatalf("Verify after repair: %v", err)
	}
	if got := srv.requests.Load(); got != 2 {
		t.Fatalf("downloads = %d, want 2", got)
	}
}

func TestInstalledForLineOrdersNewestFirst(t *testing.T) {
	m, st, _, _ := newManagerFor(t, offlineEntry(t), nil)
	rows := []model.RuntimeBuild{
		{ID: "php@8.4.4-r1", Kind: "php", Version: "8.4.4", Backend: model.BackendLocal, Path: "/runtimes/php/8.4.4-r1-linux-x64"},
		{ID: "php@8.4.4-r2", Kind: "php", Version: "8.4.4", Backend: model.BackendLocal, Path: "/runtimes/php/8.4.4-r2-linux-x64"},
		{ID: "php@8.4.3-r9", Kind: "php", Version: "8.4.3", Backend: model.BackendLocal, Path: "/runtimes/php/8.4.3-r9-linux-x64"},
		{ID: "php@8.5.0-r1", Kind: "php", Version: "8.5.0", Backend: model.BackendLocal, Path: "/runtimes/php/8.5.0-r1-linux-x64"},
		{ID: "node@24.22.1", Kind: "node", Version: "24.22.1", Backend: model.BackendLocal, Path: "/runtimes/node/24.22.1-linux-x64"},
	}
	for _, row := range rows {
		if err := st.UpsertRuntime(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	builds, err := m.InstalledForLine(context.Background(), "php", "8.4")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"php@8.4.4-r2", "php@8.4.4-r1", "php@8.4.3-r9"}
	if len(builds) != len(want) {
		t.Fatalf("InstalledForLine = %+v", builds)
	}
	for i, id := range want {
		if builds[i].ID != id {
			t.Fatalf("InstalledForLine[%d] = %q, want %q", i, builds[i].ID, id)
		}
	}

	none, err := m.InstalledForLine(context.Background(), "php", "8.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("InstalledForLine missing line = %+v", none)
	}
}

func TestCheckBuildPathRequiresExactRevision(t *testing.T) {
	tests := []struct {
		name     string
		build    model.RuntimeBuild
		wantLeaf string
	}{
		{
			name: "php revision mismatch",
			build: model.RuntimeBuild{
				ID: "php@8.4.4-r2", Kind: "php", Version: "8.4.4",
			},
			wantLeaf: "8.4.4-r1-linux-x64",
		},
		{
			name: "node path carries revision",
			build: model.RuntimeBuild{
				ID: "node@24.99.0", Kind: "node", Version: "24.99.0",
			},
			wantLeaf: "24.99.0-r1-linux-x64",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, st, home, _ := newManagerFor(t, offlineEntry(t), nil)
			dir := filepath.Join(home, "runtimes", test.build.Kind, test.wantLeaf)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(dir, "keep.txt")
			if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			test.build.Backend = model.BackendLocal
			test.build.Path = dir
			if err := st.UpsertRuntime(context.Background(), test.build); err != nil {
				t.Fatal(err)
			}
			err := m.Remove(context.Background(), test.build.ID)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("Remove error = %v, want ErrUnsafePath", err)
			}
			if _, statErr := os.Stat(victim); statErr != nil {
				t.Fatalf("Remove touched a mismatched build: %v", statErr)
			}
		})
	}
}

func TestEnsureUsesInstalledBuildOrCatalog(t *testing.T) {
	archive := fixtureData(t, "valid.tar.gz")
	srv := newFixtureServer(t, archive)
	entry := entryFor(t, archive, srv.URL+"/"+testArchive)
	m, _, _, _ := newManagerFor(t, entry, srv.Client())

	first, err := m.Ensure(context.Background(), "node", testVersion)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if first.ID != testID {
		t.Fatalf("Ensure id = %q, want %q", first.ID, testID)
	}
	second, err := m.Ensure(context.Background(), "node", testVersion)
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if second.Path != first.Path {
		t.Fatalf("second Ensure path = %q, want %q", second.Path, first.Path)
	}
	if got := srv.requests.Load(); got != 1 {
		t.Fatalf("downloads = %d, want 1", got)
	}

	if _, err := m.Ensure(context.Background(), "node", "24.98.0"); !errors.Is(err, ErrNoCatalogEntry) {
		t.Fatalf("Ensure unknown version = %v, want ErrNoCatalogEntry", err)
	} else if !strings.Contains(err.Error(), "no catalog entry") {
		t.Fatalf("Ensure unknown version error = %q, want clear message", err)
	}
	if _, err := m.Ensure(context.Background(), "php", "8.3.0"); !errors.Is(err, ErrNoCatalogEntry) {
		t.Fatalf("Ensure unknown kind = %v, want ErrNoCatalogEntry", err)
	}

	// A registered build whose directory disappeared is repaired.
	if err := os.RemoveAll(first.Path); err != nil {
		t.Fatal(err)
	}
	repaired, err := m.Ensure(context.Background(), "node", testVersion)
	if err != nil {
		t.Fatalf("Ensure after deletion: %v", err)
	}
	if repaired.Path != first.Path {
		t.Fatalf("repaired path = %q, want %q", repaired.Path, first.Path)
	}
	if got := srv.requests.Load(); got != 2 {
		t.Fatalf("downloads after repair = %d, want 2", got)
	}
}

func TestEnsureReportsCatalogLoadFailure(t *testing.T) {
	home := t.TempDir()
	st := openTestStore(t, home)
	m := newManager(st, home, nil, catalog.Catalog{}, errors.New("catalog exploded"))

	if _, err := m.Ensure(context.Background(), "node", testVersion); err == nil || !strings.Contains(err.Error(), "catalog exploded") {
		t.Fatalf("Ensure error = %v, want catalog load failure", err)
	}
}

func TestAvailableReturnsCatalog(t *testing.T) {
	entry := offlineEntry(t)
	m, _, _, _ := newManagerFor(t, entry, nil)
	available := m.Available()
	if len(available) != 1 || available[0].ID != testID {
		t.Fatalf("Available = %+v, want one %s entry", available, testID)
	}
	available[0].ID = "mutated"
	if again := m.Available(); again[0].ID != testID {
		t.Fatalf("Available returned an aliased slice: %+v", again)
	}
}

func TestInstallUnknownID(t *testing.T) {
	m, _, _, _ := newManagerFor(t, offlineEntry(t), nil)
	if _, err := m.Install(context.Background(), "node@1.2.3"); !errors.Is(err, ErrNoCatalogEntry) {
		t.Fatalf("Install unknown id = %v, want ErrNoCatalogEntry", err)
	}
}

func TestInstallCancelledContextWritesNothing(t *testing.T) {
	m, _, home, _ := newManagerFor(t, offlineEntry(t), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Install(ctx, testID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Install error = %v, want context.Canceled", err)
	}
	if builds, _ := m.List(context.Background()); len(builds) != 0 {
		t.Fatalf("runtime registered after cancellation: %+v", builds)
	}
	assertNoPartialOrTempFiles(t, home)
}

func TestInstallFileMissingSource(t *testing.T) {
	m, _, home, _ := newManagerFor(t, offlineEntry(t), nil)
	if _, err := m.InstallFile(context.Background(), testID, filepath.Join(t.TempDir(), "missing.tar.gz")); err == nil {
		t.Fatal("InstallFile accepted a missing source")
	}
	if builds, _ := m.List(context.Background()); len(builds) != 0 {
		t.Fatalf("runtime registered after missing source: %+v", builds)
	}
	assertNoPartialOrTempFiles(t, home)
}

func TestManifestRecordsEveryRegularFile(t *testing.T) {
	m, _, _, _ := installValidOffline(t)
	builds, err := m.List(context.Background())
	if err != nil || len(builds) != 1 {
		t.Fatalf("List: %v", err)
	}
	var manifest manifest
	if err := readJSONFile(filepath.Join(builds[0].Path, manifestName), &manifest); err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	want := map[string]uint32{
		"bin/node":       0o755,
		"lib/readme.txt": 0o644,
		"LICENSE":        0o644,
	}
	if len(manifest.Files) != len(want) {
		t.Fatalf("manifest files = %+v, want %d entries", manifest.Files, len(want))
	}
	for _, file := range manifest.Files {
		mode, ok := want[file.Path]
		if !ok {
			t.Errorf("unexpected manifest file %q", file.Path)
			continue
		}
		if file.Mode != mode {
			t.Errorf("%s mode = %04o, want %04o", file.Path, file.Mode, mode)
		}
		if len(file.SHA256) != 64 {
			t.Errorf("%s sha256 = %q, want 64 hex characters", file.Path, file.SHA256)
		}
		sum := sha256.Sum256(mustReadFile(t, filepath.Join(builds[0].Path, filepath.FromSlash(file.Path))))
		if hex.EncodeToString(sum[:]) != file.SHA256 {
			t.Errorf("%s sha256 does not match file contents", file.Path)
		}
	}
	if manifest.ID != testID || manifest.Kind != "node" || manifest.Version != testVersion {
		t.Errorf("manifest identity = %+v", manifest)
	}
}

func installValidOffline(t *testing.T) (*Manager, *store.Store, string, catalog.Catalog) {
	t.Helper()
	entry := offlineEntry(t)
	m, st, home, cat := newManagerFor(t, entry, nil)
	if _, err := m.InstallFile(context.Background(), testID, filepath.Join("testdata", "valid.tar.gz")); err != nil {
		t.Fatalf("InstallFile: %v", err)
	}
	return m, st, home, cat
}

func offlineEntry(t *testing.T) catalog.Entry {
	t.Helper()
	archive := fixtureData(t, "valid.tar.gz")
	return entryFor(t, archive, "https://nodejs.org/dist/v"+testVersion+"/"+testArchive)
}

func newManagerFor(t *testing.T, entry catalog.Entry, client *http.Client) (*Manager, *store.Store, string, catalog.Catalog) {
	t.Helper()
	cat := catalogFor(t, entry)
	return newManagerWithCatalogFor(t, cat, client)
}

func newManagerWithCatalogFor(t *testing.T, cat catalog.Catalog, client *http.Client) (*Manager, *store.Store, string, catalog.Catalog) {
	t.Helper()
	home := t.TempDir()
	st := openTestStore(t, home)
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return NewManagerWithCatalog(st, home, client, cat), st, home, cat
}

func openTestStore(t *testing.T, home string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func catalogFor(t *testing.T, entries ...catalog.Entry) catalog.Catalog {
	t.Helper()
	data, err := json.Marshal(catalog.Catalog{Entries: entries})
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	cat, err := catalog.LoadFrom(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("catalog.LoadFrom: %v", err)
	}
	return cat
}

func entryFor(t *testing.T, archive []byte, url string) catalog.Entry {
	t.Helper()
	sum := sha256.Sum256(archive)
	return catalog.Entry{
		ID:      testID,
		Kind:    "node",
		Version: testVersion,
		Build:   testBuild,
		URL:     url,
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(archive)),
	}
}

func phpEntryFor(t *testing.T, archive []byte, url string, revision int) catalog.Entry {
	t.Helper()
	sum := sha256.Sum256(archive)
	return catalog.Entry{
		ID:       fmt.Sprintf("php@%s-r%d", "8.4.4", revision),
		Kind:     "php",
		Version:  "8.4.4",
		Build:    testBuild,
		Revision: revision,
		URL:      url,
		SHA256:   hex.EncodeToString(sum[:]),
		Size:     int64(len(archive)),
	}
}

func fixtureData(t *testing.T, name string) []byte {
	t.Helper()
	return mustReadFile(t, filepath.Join("testdata", name))
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func readJSONFile(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func upsertRuntime(t *testing.T, st *store.Store, id, path string) {
	t.Helper()
	if err := st.UpsertRuntime(context.Background(), model.RuntimeBuild{
		ID:          id,
		Kind:        "node",
		Version:     testVersion,
		Backend:     model.BackendLocal,
		Path:        path,
		Checksum:    strings.Repeat("a", 64),
		InstalledAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertRuntime: %v", err)
	}
}

func filesNamed(t *testing.T, root, name string) []string {
	t.Helper()
	var matches []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == name {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return matches
}

func assertNoPartialOrTempFiles(t *testing.T, home string) {
	t.Helper()
	cache := filepath.Join(home, "cache")
	if entries, err := os.ReadDir(cache); err == nil {
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".part") {
				t.Errorf("leftover partial download %s", filepath.Join(cache, entry.Name()))
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("read cache: %v", err)
	}

	runtimes := filepath.Join(home, "runtimes")
	if entries, err := os.ReadDir(runtimes); err == nil {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".install-") {
				t.Errorf("leftover extraction directory %s", filepath.Join(runtimes, entry.Name()))
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("read runtimes: %v", err)
	}
}

type fixtureServer struct {
	*httptest.Server
	requests atomic.Int32
}

func newFixtureServer(t *testing.T, archive []byte) *fixtureServer {
	t.Helper()
	fs := &fixtureServer{}
	fs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		if r.URL.Path != "/"+testArchive {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(archive)
	}))
	t.Cleanup(fs.Close)
	return fs
}

func newAnyFixtureServer(t *testing.T, archive []byte) *fixtureServer {
	t.Helper()
	fs := &fixtureServer{}
	fs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(archive)
	}))
	t.Cleanup(fs.Close)
	return fs
}
