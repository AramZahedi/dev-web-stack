package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

const testArtifactChecksum = "eb6728e77c576a35162c122c45ccadf224d89ff00abc8dae46cc2897c6002d7c"

func validPinnedNodeArtifact() agentproto.RuntimeArtifact {
	return agentproto.RuntimeArtifact{
		ID:      "node@24.22.1",
		Kind:    "node",
		Version: "24.22.1",
		Build:   "linux-x64",
		URL:     "https://nodejs.org/dist/v24.22.1/node-v24.22.1-linux-x64.tar.gz",
		SHA256:  testArtifactChecksum,
		Size:    58_290_700,
	}
}

// stageNodeBuild creates the runnable binary the agent requires after an
// install. Tests set DEVSTACK_NO_DOWNLOAD so the assertion is about the
// install path, not nodejs.org availability.
func stageNodeBuild(t *testing.T, root, id string) {
	t.Helper()
	binary := filepath.Join(root, "runtimes", id, "bin", "node")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAgentInstallPHPRuntimeWritesCompensatingBaseIni(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	buildDir := filepath.Join(root, "runtimes", "php@8.5.12-r1")
	for _, binary := range []string{"bin/php", "sbin/php-fpm"} {
		path := filepath.Join(buildDir, filepath.FromSlash(binary))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	extDir := filepath.Join(buildDir, "lib", "php", "extensions")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "opcache.so"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	iniPath := filepath.Join(buildDir, "etc", "php.ini")
	if err := os.MkdirAll(filepath.Dir(iniPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iniPath, []byte("[PHP]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.InstallRuntime(context.Background(), agentproto.RuntimeRequest{BuildID: "php@8.5.12-r1"}); err != nil {
		t.Fatalf("InstallRuntime(php@8.5.12-r1) = %v", err)
	}
	data, err := os.ReadFile(iniPath)
	if err != nil {
		t.Fatal(err)
	}
	want := `extension_dir = "` + filepath.ToSlash(extDir) + `"`
	if !strings.Contains(string(data), want) {
		t.Fatalf("base php.ini = %q, missing %q", data, want)
	}

	// A second install must not append a duplicate directive.
	if _, err := runtime.InstallRuntime(context.Background(), agentproto.RuntimeRequest{BuildID: "php@8.5.12-r1"}); err != nil {
		t.Fatalf("InstallRuntime second call = %v", err)
	}
	data, err = os.ReadFile(iniPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "extension_dir ="); got != 1 {
		t.Fatalf("base php.ini has %d extension_dir lines:\n%s", got, data)
	}
}

func TestAgentInstallRuntimeAcceptsPinnedArtifact(t *testing.T) {
	t.Setenv("DEVSTACK_NO_DOWNLOAD", "1")
	agentRuntime, root := newTestRuntimeOnly(t)
	artifact := validPinnedNodeArtifact()
	stageNodeBuild(t, root, artifact.ID)

	response, err := agentRuntime.InstallRuntime(context.Background(), agentproto.RuntimeRequest{Artifact: &artifact})
	if err != nil {
		t.Fatalf("InstallRuntime(pinned artifact) = %v", err)
	}
	if response.ID != artifact.ID || response.Kind != artifact.Kind || response.Version != artifact.Version {
		t.Fatalf("runtime response = %+v, want artifact %+v", response, artifact)
	}
	if response.Checksum != artifact.SHA256 {
		t.Fatalf("checksum = %q, want %q", response.Checksum, artifact.SHA256)
	}
	if response.Path != filepath.Join(root, "runtimes", artifact.ID) {
		t.Fatalf("path = %q", response.Path)
	}
	list, err := agentRuntime.ListRuntimes(context.Background())
	if err != nil || len(list.Runtimes) != 1 || list.Runtimes[0].ID != artifact.ID {
		t.Fatalf("ListRuntimes = %+v, %v", list, err)
	}
}

func TestRuntimeArtifactEntryPreservesPinnedFields(t *testing.T) {
	artifact := validPinnedNodeArtifact()
	entry, err := runtimeArtifactEntry(artifact)
	if err != nil {
		t.Fatalf("runtimeArtifactEntry = %v", err)
	}
	if entry.ID != artifact.ID || entry.Kind != artifact.Kind || entry.Version != artifact.Version {
		t.Fatalf("identity fields = %+v, want %+v", entry, artifact)
	}
	if entry.Build != "linux-x64" {
		t.Fatalf("build = %q", entry.Build)
	}
	if entry.URL != artifact.URL || entry.SHA256 != artifact.SHA256 || entry.Size != artifact.Size {
		t.Fatalf("pinned fields = %+v, want url=%q sha256=%q size=%d", entry, artifact.URL, artifact.SHA256, artifact.Size)
	}
}

func TestAgentInstallRuntimePassesPinnedArtifactToDownloader(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runtime downloads are disabled on native Windows")
	}
	t.Setenv("DEVSTACK_NO_DOWNLOAD", "")
	agentRuntime, root := newTestRuntimeOnly(t)
	artifact := validPinnedNodeArtifact()
	// Use an id outside the embedded catalog to prove the artifact path, not
	// a catalog lookup, drives the download.
	artifact.ID = "node@99.1.2"
	artifact.Version = "99.1.2"
	artifact.URL = "https://nodejs.org/dist/v99.1.2/node-v99.1.2-linux-x64.tar.gz"
	artifact.SHA256 = strings.Repeat("b", 64)
	artifact.Size = 123_456

	type downloadCall struct {
		url, sha256, dest string
		size              int64
	}
	var calls []downloadCall
	sentinel := errors.New("stub download failed")
	previous := downloadRuntimeArchive
	downloadRuntimeArchive = func(ctx context.Context, rawURL, sha256 string, size int64, dest string) error {
		calls = append(calls, downloadCall{url: rawURL, sha256: sha256, size: size, dest: dest})
		return sentinel
	}
	t.Cleanup(func() { downloadRuntimeArchive = previous })

	_, err := agentRuntime.InstallRuntime(context.Background(), agentproto.RuntimeRequest{Artifact: &artifact})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InstallRuntime error = %v, want downloader error", err)
	}
	if len(calls) != 1 {
		t.Fatalf("downloader calls = %d, want 1", len(calls))
	}
	got := calls[0]
	if got.url != artifact.URL || got.sha256 != artifact.SHA256 || got.size != artifact.Size {
		t.Fatalf("downloader args = %+v, want url=%q sha256=%q size=%d", got, artifact.URL, artifact.SHA256, artifact.Size)
	}
	if got.dest != filepath.Join(root, "runtimes", artifact.ID) {
		t.Fatalf("downloader destination = %q", got.dest)
	}
	list, err := agentRuntime.ListRuntimes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Runtimes) != 0 {
		t.Fatalf("failed download registered runtimes: %+v", list.Runtimes)
	}
}

func TestAgentRuntimeHTTPInstallPinnedArtifact(t *testing.T) {
	t.Setenv("DEVSTACK_NO_DOWNLOAD", "1")
	agentRuntime, root := newTestRuntimeOnly(t)
	artifact := validPinnedNodeArtifact()
	stageNodeBuild(t, root, artifact.ID)

	body, err := json.Marshal(agentproto.RuntimeRequest{Artifact: &artifact})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(agentproto.PathRuntimes, func(w http.ResponseWriter, r *http.Request) {
		handleRuntimes(w, r, agentRuntime)
	})
	request := httptest.NewRequest(http.MethodPost, agentproto.PathRuntimes, bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", recorder.Code, recorder.Body.String())
	}
	var response agentproto.RuntimeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != artifact.ID || response.Checksum != artifact.SHA256 {
		t.Fatalf("response = %+v", response)
	}
}

func TestAgentInstallRuntimeAcceptsPinnedArtifactWithoutBuild(t *testing.T) {
	t.Setenv("DEVSTACK_NO_DOWNLOAD", "1")
	agentRuntime, root := newTestRuntimeOnly(t)
	artifact := validPinnedNodeArtifact()
	artifact.ID = "node@22.23.4"
	artifact.Version = "22.23.4"
	artifact.Build = ""
	artifact.URL = "https://nodejs.org/dist/v22.23.4/node-v22.23.4-linux-x64.tar.gz"
	stageNodeBuild(t, root, artifact.ID)

	if _, err := agentRuntime.InstallRuntime(context.Background(), agentproto.RuntimeRequest{Artifact: &artifact}); err != nil {
		t.Fatalf("InstallRuntime(artifact without build) = %v", err)
	}
}

func TestAgentInstallRuntimeKeepsEmbeddedBuildIDPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runtime downloads and binary checks are disabled on native Windows")
	}
	t.Setenv("DEVSTACK_NO_DOWNLOAD", "1")
	agentRuntime, root := newTestRuntimeOnly(t)
	stageNodeBuild(t, root, "node@22.23.4")

	response, err := agentRuntime.InstallRuntime(context.Background(), agentproto.RuntimeRequest{BuildID: "node@22.23.4"})
	if err != nil {
		t.Fatalf("InstallRuntime(embedded build id) = %v", err)
	}
	if response.ID != "node@22.23.4" {
		t.Fatalf("response = %+v", response)
	}
}

func TestAgentInstallRuntimeRejectsUnsafeArtifact(t *testing.T) {
	t.Setenv("DEVSTACK_NO_DOWNLOAD", "1")

	cases := []struct {
		name   string
		mutate func(*agentproto.RuntimeArtifact)
	}{
		{name: "missing id", mutate: func(a *agentproto.RuntimeArtifact) { a.ID = "" }},
		{name: "missing kind", mutate: func(a *agentproto.RuntimeArtifact) { a.Kind = "" }},
		{name: "missing version", mutate: func(a *agentproto.RuntimeArtifact) { a.Version = "" }},
		{name: "missing url", mutate: func(a *agentproto.RuntimeArtifact) { a.URL = "" }},
		{name: "missing checksum", mutate: func(a *agentproto.RuntimeArtifact) { a.SHA256 = "" }},
		{name: "zero size", mutate: func(a *agentproto.RuntimeArtifact) { a.Size = 0 }},
		{name: "negative size", mutate: func(a *agentproto.RuntimeArtifact) { a.Size = -1 }},
		{name: "size over archive limit", mutate: func(a *agentproto.RuntimeArtifact) { a.Size = maxRuntimeArchiveBytes + 1 }},
		{name: "http url", mutate: func(a *agentproto.RuntimeArtifact) {
			a.URL = "http://nodejs.org/dist/v24.22.1/node-v24.22.1-linux-x64.tar.gz"
		}},
		{name: "host mismatch", mutate: func(a *agentproto.RuntimeArtifact) {
			a.URL = "https://evil.example.com/dist/v24.22.1/node-v24.22.1-linux-x64.tar.gz"
		}},
		{name: "host suffix", mutate: func(a *agentproto.RuntimeArtifact) {
			a.URL = "https://nodejs.org.evil.example/dist/v24.22.1/node-v24.22.1-linux-x64.tar.gz"
		}},
		{name: "host userinfo confusion", mutate: func(a *agentproto.RuntimeArtifact) {
			a.URL = "https://evil.example@nodejs.org/dist/v24.22.1/node-v24.22.1-linux-x64.tar.gz"
		}},
		{name: "node id with revision", mutate: func(a *agentproto.RuntimeArtifact) { a.ID = "node@24.22.1-r1" }},
		{name: "traversal id", mutate: func(a *agentproto.RuntimeArtifact) { a.ID = "node@24.22.1/../../evil" }},
		{name: "backslash traversal id", mutate: func(a *agentproto.RuntimeArtifact) { a.ID = `node@24.22.1\..\evil` }},
		{name: "id and version differ", mutate: func(a *agentproto.RuntimeArtifact) { a.Version = "24.22.2" }},
		{name: "unsupported kind", mutate: func(a *agentproto.RuntimeArtifact) { a.Kind = "php" }},
		{name: "unsupported build", mutate: func(a *agentproto.RuntimeArtifact) { a.Build = "win-x64" }},
		{name: "short checksum", mutate: func(a *agentproto.RuntimeArtifact) { a.SHA256 = "abcd" }},
		{name: "uppercase checksum", mutate: func(a *agentproto.RuntimeArtifact) { a.SHA256 = strings.ToUpper(a.SHA256) }},
		{name: "non hex checksum", mutate: func(a *agentproto.RuntimeArtifact) { a.SHA256 = strings.Repeat("g", 64) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agentRuntime, root := newTestRuntimeOnly(t)
			artifact := validPinnedNodeArtifact()
			tc.mutate(&artifact)

			if _, err := agentRuntime.InstallRuntime(context.Background(), agentproto.RuntimeRequest{Artifact: &artifact}); err == nil {
				t.Fatal("InstallRuntime accepted an unsafe artifact")
			}
			list, err := agentRuntime.ListRuntimes(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Runtimes) != 0 {
				t.Fatalf("rejected artifact registered runtimes: %+v", list.Runtimes)
			}
			if entries, err := os.ReadDir(filepath.Join(root, "runtimes")); err == nil && len(entries) != 0 {
				t.Fatalf("rejected artifact created runtime directories: %v", entries)
			}
		})
	}
}

func TestDownloadAndExtractArchiveVerifiesSize(t *testing.T) {
	archive := testRuntimeArchive(t)
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	for _, tc := range []struct {
		name    string
		size    int64
		wantErr bool
	}{
		{name: "exact size", size: int64(len(archive))},
		{name: "too small", size: int64(len(archive)) - 1, wantErr: true},
		{name: "too large", size: int64(len(archive)) + 1, wantErr: true},
		{name: "missing size", size: 0, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "runtime")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			err := downloadAndExtractArchive(context.Background(), server.URL+"/php.tar.gz", digest, tc.size, dest)
			if tc.wantErr {
				if err == nil {
					t.Fatal("downloadAndExtractArchive accepted a mismatched archive size")
				}
				entries, readErr := os.ReadDir(dest)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if len(entries) != 0 {
					t.Fatalf("failed size check left %d entries in destination", len(entries))
				}
				return
			}
			if err != nil {
				t.Fatalf("downloadAndExtractArchive = %v", err)
			}
		})
	}
}
