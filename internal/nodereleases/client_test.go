package nodereleases

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	fixtureVersion = "19.9.0"
	fixtureSHA256  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	fixtureSize    = int64(1_234_567)
)

const discoveryIndex = `[
	{
		"version": "v19.9.0",
		"date": "2023-04-10",
		"files": ["headers", "linux-x64", "osx-arm64-tar"],
		"npm": "9.6.7",
		"modules": "111",
		"lts": false,
		"security": false
	},
	{
		"version": "v19.8.0",
		"date": "2023-03-14",
		"files": ["linux-x64"],
		"npm": "9.6.6",
		"modules": "111",
		"lts": false,
		"security": true
	}
]`

const refreshedIndex = `[
	{
		"version": "v19.10.0",
		"date": "2023-05-01",
		"files": ["linux-x64"],
		"npm": "9.6.7",
		"modules": "111",
		"lts": false,
		"security": false
	},
	{
		"version": "v19.9.0",
		"date": "2023-04-10",
		"files": ["linux-x64"],
		"npm": "9.6.7",
		"modules": "111",
		"lts": false,
		"security": false
	}
]`

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// fixture serves the official Node endpoints on a local test server. Tests
// reach it through a transport that rewrites official URLs to the server, so
// host validation in the client stays strict.
type fixture struct {
	*httptest.Server
	indexRequests    atomic.Int64
	checksumRequests atomic.Int64
	sizeRequests     atomic.Int64

	mu        sync.Mutex
	indexBody string
	shasums   map[string]string
	sizes     map[string]int64

	fail              atomic.Bool
	redirectChecksums string
	redirectArchive   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	f := &fixture{
		indexBody: discoveryIndex,
		shasums: map[string]string{
			fixtureVersion: readTestdata(t, "SHASUMS256.txt"),
		},
		sizes: map[string]int64{fixtureVersion: fixtureSize},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

func (f *fixture) handle(w http.ResponseWriter, r *http.Request) {
	if f.fail.Load() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case r.URL.Path == "/dist/index.json":
		f.indexRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, f.indexBody)
	case strings.HasPrefix(r.URL.Path, "/dist/v") && strings.HasSuffix(r.URL.Path, "/SHASUMS256.txt"):
		f.checksumRequests.Add(1)
		if f.redirectChecksums != "" {
			http.Redirect(w, r, f.redirectChecksums, http.StatusFound)
			return
		}
		body, ok := f.shasums[versionFromPath(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	case r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, ".tar.gz"):
		f.sizeRequests.Add(1)
		if f.redirectArchive != "" {
			http.Redirect(w, r, f.redirectArchive, http.StatusFound)
			return
		}
		size, ok := f.sizes[versionFromPath(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func versionFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || !strings.HasPrefix(parts[1], "v") {
		return ""
	}
	return strings.TrimPrefix(parts[1], "v")
}

func (f *fixture) setChecksums(version, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shasums[version] = body
}

func (f *fixture) setIndexBody(body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.indexBody = body
}

func (f *fixture) setRedirects(checksums, archive string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redirectChecksums = checksums
	f.redirectArchive = archive
}

// officialClient routes requests for nodejs.org to the fixture server while
// leaving the request URL (and therefore redirect validation) untouched.
func officialClient(server *httptest.Server) *http.Client {
	target := strings.TrimPrefix(server.URL, "http://")
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return rewriteAndSend(req, target)
		}),
		Timeout: 5 * time.Second,
	}
}

func rewriteAndSend(req *http.Request, target string) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = target
	clone.Host = target
	return http.DefaultTransport.RoundTrip(clone)
}

func newTestClient(t *testing.T, home string, httpClient *http.Client, clock func() time.Time) *Client {
	t.Helper()

	client, err := New(Config{Home: home, HTTPClient: httpClient, Now: clock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func officialClientForFailure() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network disabled")
	})}
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return string(data)
}

func assertHasVersion(t *testing.T, releases []Release, version string) {
	t.Helper()

	for _, release := range releases {
		if release.Version == version {
			return
		}
	}
	t.Fatalf("releases do not contain %s: %+v", version, releases)
}

func TestListCachesIndexAndServesFreshCache(t *testing.T) {
	f := newFixture(t)
	home := t.TempDir()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	client := newTestClient(t, home, officialClient(f.Server), func() time.Time { return now })

	first, err := client.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if first.Source != SourceLive {
		t.Fatalf("first snapshot source = %q, want %q", first.Source, SourceLive)
	}
	if first.Stale {
		t.Error("first snapshot is stale, want fresh")
	}
	if !first.CachedAt.Equal(now) {
		t.Errorf("first snapshot cached at %s, want %s", first.CachedAt, now)
	}
	if got := f.indexRequests.Load(); got != 1 {
		t.Fatalf("index requests = %d, want 1", got)
	}

	indexPath := filepath.Join(home, "cache", "node", "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read cached index: %v", err)
	}
	var cached struct {
		CachedAt time.Time `json:"cached_at"`
		Releases []Release `json:"releases"`
	}
	if err := json.Unmarshal(data, &cached); err != nil {
		t.Fatalf("decode cached index: %v", err)
	}
	if !cached.CachedAt.Equal(now) {
		t.Errorf("cached index timestamp = %s, want %s", cached.CachedAt, now)
	}
	if len(cached.Releases) == 0 {
		t.Fatal("cached index has no releases")
	}

	for _, version := range []string{fixtureVersion, "19.8.0", "24.22.1", "22.23.4"} {
		assertHasVersion(t, first.All, version)
	}
	for i, major := range first.Majors {
		if len(major.Releases) != 1 {
			t.Errorf("summary major %d (%d) has %d releases, want only the latest",
				i, major.Major, len(major.Releases))
		}
		if major.Latest != major.Releases[0] {
			t.Errorf("summary major %d latest = %+v, want %+v", i, major.Latest, major.Releases[0])
		}
	}

	second, err := client.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("second List: %v", err)
	}
	if second.Source != SourceCache {
		t.Fatalf("second snapshot source = %q, want %q", second.Source, SourceCache)
	}
	if second.Stale {
		t.Error("second snapshot is stale, want fresh cache")
	}
	if !second.CachedAt.Equal(now) {
		t.Errorf("second snapshot cached at %s, want %s", second.CachedAt, now)
	}
	if got := f.indexRequests.Load(); got != 1 {
		t.Fatalf("fresh cache performed %d index requests, want 1 total", got)
	}
	assertHasVersion(t, second.All, fixtureVersion)

	expanded, err := client.List(context.Background(), ListOptions{Major: 19, All: true})
	if err != nil {
		t.Fatalf("List major 19 all: %v", err)
	}
	if len(expanded.Majors) != 1 || expanded.Majors[0].Major != 19 {
		t.Fatalf("major-filtered snapshot majors = %+v, want only major 19", expanded.Majors)
	}
	if len(expanded.Majors[0].Releases) != 2 {
		t.Errorf("major 19 releases = %d, want 2", len(expanded.Majors[0].Releases))
	}
	if len(expanded.All) != 2 {
		t.Errorf("major 19 flat releases = %d, want 2", len(expanded.All))
	}
}

func TestRefreshForcesIndexFetch(t *testing.T) {
	f := newFixture(t)
	home := t.TempDir()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	client := newTestClient(t, home, officialClient(f.Server), func() time.Time { return now })

	first, err := client.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if first.Source != SourceLive {
		t.Fatalf("first snapshot source = %q, want %q", first.Source, SourceLive)
	}

	// A fresh cache must not suppress an explicit refresh.
	f.setIndexBody(refreshedIndex)
	now = now.Add(time.Hour)
	refreshed, err := client.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.Source != SourceLive {
		t.Fatalf("refreshed source = %q, want %q", refreshed.Source, SourceLive)
	}
	if !refreshed.CachedAt.Equal(now) {
		t.Errorf("refreshed cached at %s, want %s", refreshed.CachedAt, now)
	}
	if got := f.indexRequests.Load(); got != 2 {
		t.Errorf("index requests = %d, want 2", got)
	}
	assertHasVersion(t, refreshed.All, "19.10.0")
}

func TestListServesStaleCacheAfterRefreshFailure(t *testing.T) {
	f := newFixture(t)
	home := t.TempDir()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	client := newTestClient(t, home, officialClient(f.Server), func() time.Time { return now })

	first, err := client.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if first.Source != SourceLive {
		t.Fatalf("first snapshot source = %q, want %q", first.Source, SourceLive)
	}

	indexPath := filepath.Join(home, "cache", "node", "index.json")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read cached index: %v", err)
	}

	f.fail.Store(true)
	now = now.Add(7 * time.Hour)

	stale, err := client.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List with stale cache: %v", err)
	}
	if stale.Source != SourceCache {
		t.Fatalf("stale snapshot source = %q, want %q", stale.Source, SourceCache)
	}
	if !stale.Stale {
		t.Error("stale snapshot is not marked stale")
	}
	if stale.Warning == "" {
		t.Error("stale snapshot has no warning")
	}
	if !stale.CachedAt.Equal(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("stale snapshot cached at %s, want the original timestamp", stale.CachedAt)
	}
	assertHasVersion(t, stale.All, fixtureVersion)

	refreshed, refreshErr := client.Refresh(context.Background())
	if refreshErr == nil {
		t.Fatalf("Refresh succeeded with an unreachable server: %+v", refreshed)
	}
	if refreshed.All != nil {
		t.Errorf("failed Refresh returned releases: %+v", refreshed)
	}

	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read cached index after failed refresh: %v", err)
	}
	if string(before) != string(after) {
		t.Error("failed refresh replaced the cached index")
	}

	again, err := client.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List after failed Refresh: %v", err)
	}
	if again.Source != SourceCache || !again.Stale {
		t.Errorf("List after failed Refresh = source %q stale %v, want stale cache", again.Source, again.Stale)
	}
}

func TestListOfflineFallsBackToEmbeddedCatalog(t *testing.T) {
	var calls atomic.Int64
	offline := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("network disabled")
	})}
	client := newTestClient(t, t.TempDir(), offline, nil)

	snapshot, err := client.List(context.Background(), ListOptions{Offline: true})
	if err != nil {
		t.Fatalf("offline List: %v", err)
	}
	if snapshot.Source != SourceEmbedded {
		t.Fatalf("offline source = %q, want %q", snapshot.Source, SourceEmbedded)
	}
	if !snapshot.Stale {
		t.Error("offline snapshot is not marked stale")
	}
	if snapshot.Warning == "" {
		t.Error("offline snapshot has no warning")
	}
	if !snapshot.CachedAt.IsZero() {
		t.Errorf("offline snapshot cached at %s, want zero", snapshot.CachedAt)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("offline List performed %d requests, want 0", got)
	}
	assertHasVersion(t, snapshot.All, "24.22.1")
	assertHasVersion(t, snapshot.All, "22.23.4")
	if len(snapshot.Majors) == 0 {
		t.Fatal("offline snapshot has no majors")
	}

	unreachable := newTestClient(t, t.TempDir(), offline, nil)
	fallback, err := unreachable.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("unreachable List: %v", err)
	}
	if fallback.Source != SourceEmbedded || !fallback.Stale || fallback.Warning == "" {
		t.Errorf("unreachable snapshot = source %q stale %v warning %q, want embedded stale warning",
			fallback.Source, fallback.Stale, fallback.Warning)
	}
	if got := calls.Load(); got == 0 {
		t.Error("unreachable List did not attempt the index")
	}
}

func TestPreflightRequiresChecksumAndPositiveSize(t *testing.T) {
	t.Run("pins checksum and size", func(t *testing.T) {
		f := newFixture(t)
		home := t.TempDir()
		now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
		client := newTestClient(t, home, officialClient(f.Server), func() time.Time { return now })

		preflight, err := client.Preflight(context.Background(), "19")
		if err != nil {
			t.Fatalf("Preflight: %v", err)
		}
		entry := preflight.Entry
		if entry.ID != "node@"+fixtureVersion {
			t.Errorf("entry id = %q, want node@%s", entry.ID, fixtureVersion)
		}
		if entry.Kind != "node" {
			t.Errorf("entry kind = %q, want node", entry.Kind)
		}
		if entry.Version != fixtureVersion {
			t.Errorf("entry version = %q, want %s", entry.Version, fixtureVersion)
		}
		if entry.Build != "linux-x64" {
			t.Errorf("entry build = %q, want linux-x64", entry.Build)
		}
		if entry.Revision != 0 {
			t.Errorf("entry revision = %d, want 0", entry.Revision)
		}
		wantURL := "https://nodejs.org/dist/v" + fixtureVersion + "/node-v" + fixtureVersion + "-linux-x64.tar.gz"
		if entry.URL != wantURL {
			t.Errorf("entry url = %q, want %q", entry.URL, wantURL)
		}
		if entry.SHA256 != fixtureSHA256 {
			t.Errorf("entry sha256 = %q, want %q", entry.SHA256, fixtureSHA256)
		}
		if entry.Size != fixtureSize {
			t.Errorf("entry size = %d, want %d", entry.Size, fixtureSize)
		}
		if preflight.Release.Version != fixtureVersion {
			t.Errorf("release version = %q, want %s", preflight.Release.Version, fixtureVersion)
		}
		if preflight.Release.NPM != "9.6.7" {
			t.Errorf("release npm = %q, want 9.6.7", preflight.Release.NPM)
		}

		if _, err := client.Preflight(context.Background(), "99"); !errors.Is(err, ErrReleaseNotFound) {
			t.Errorf("Preflight unknown selector error = %v, want ErrReleaseNotFound", err)
		}

		checksumPath := filepath.Join(home, "cache", "node", "shasums", fixtureVersion+".txt")
		cachedChecksums, err := os.ReadFile(checksumPath)
		if err != nil {
			t.Fatalf("read cached checksums: %v", err)
		}
		if string(cachedChecksums) != readTestdata(t, "SHASUMS256.txt") {
			t.Error("cached checksum file does not match the official fixture")
		}

		sizePath := filepath.Join(home, "cache", "node", "sizes", fixtureVersion+".json")
		sizeData, err := os.ReadFile(sizePath)
		if err != nil {
			t.Fatalf("read cached size: %v", err)
		}
		var cachedSize struct {
			Size int64 `json:"size"`
		}
		if err := json.Unmarshal(sizeData, &cachedSize); err != nil {
			t.Fatalf("decode cached size: %v", err)
		}
		if cachedSize.Size != fixtureSize {
			t.Errorf("cached size = %d, want %d", cachedSize.Size, fixtureSize)
		}

		requests := [3]int64{f.indexRequests.Load(), f.checksumRequests.Load(), f.sizeRequests.Load()}
		f.fail.Store(true)
		second, err := client.Preflight(context.Background(), fixtureVersion)
		if err != nil {
			t.Fatalf("Preflight with cached metadata: %v", err)
		}
		if second.Entry != entry {
			t.Errorf("cached preflight entry = %+v, want %+v", second.Entry, entry)
		}
		after := [3]int64{f.indexRequests.Load(), f.checksumRequests.Load(), f.sizeRequests.Load()}
		if requests != after {
			t.Errorf("cached preflight performed requests: before %v after %v", requests, after)
		}
	})

	checksumCases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "no matching line",
			body:    strings.Repeat("a", 64) + "  node-v19.9.0-darwin-arm64.tar.gz\n",
			wantErr: "exactly one",
		},
		{
			name: "duplicate matching lines",
			body: fixtureSHA256 + "  node-v19.9.0-linux-x64.tar.gz\n" +
				fixtureSHA256 + "  node-v19.9.0-linux-x64.tar.gz\n",
			wantErr: "exactly one",
		},
		{
			name:    "uppercase checksum",
			body:    strings.ToUpper(fixtureSHA256) + "  node-v19.9.0-linux-x64.tar.gz\n",
			wantErr: "checksum",
		},
		{
			name:    "short checksum",
			body:    "abc123  node-v19.9.0-linux-x64.tar.gz\n",
			wantErr: "checksum",
		},
	}
	for _, test := range checksumCases {
		t.Run("checksum "+test.name, func(t *testing.T) {
			f := newFixture(t)
			f.setChecksums(fixtureVersion, test.body)
			client := newTestClient(t, t.TempDir(), officialClient(f.Server), nil)

			_, err := client.Preflight(context.Background(), fixtureVersion)
			if err == nil {
				t.Fatalf("Preflight accepted a checksum file with %s", test.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.wantErr)) {
				t.Fatalf("Preflight error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}

	sizeCases := []struct {
		name          string
		contentLength *int64
		wantErr       string
	}{
		{name: "missing content length", contentLength: nil, wantErr: "content-length"},
		{name: "zero content length", contentLength: int64Ptr(0), wantErr: "positive"},
		{name: "oversized archive", contentLength: int64Ptr(MaxArchiveSize + 1), wantErr: "limit"},
	}
	for _, test := range sizeCases {
		t.Run("size "+test.name, func(t *testing.T) {
			f := newFixture(t)
			client := newTestClient(t, t.TempDir(),
				clientWithHead(f, test.contentLength), nil)

			_, err := client.Preflight(context.Background(), fixtureVersion)
			if err == nil {
				t.Fatalf("Preflight accepted an archive with %s", test.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.wantErr)) {
				t.Fatalf("Preflight error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestPreflightRejectsOffHostRedirect(t *testing.T) {
	redirectCases := []struct {
		name           string
		checksumTarget string
		archiveTarget  string
		wantErr        string
	}{
		{
			name:           "checksum redirect to off-host",
			checksumTarget: "https://evil.example/SHASUMS256.txt",
			wantErr:        "nodejs.org",
		},
		{
			name:          "archive redirect to off-host",
			archiveTarget: "https://evil.example/node-v19.9.0-linux-x64.tar.gz",
			wantErr:       "nodejs.org",
		},
		{
			name:          "archive redirect to insecure scheme",
			archiveTarget: "http://nodejs.org/dist/v19.9.0/node-v19.9.0-linux-x64.tar.gz",
			wantErr:       "https",
		},
	}
	for _, test := range redirectCases {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			f.setRedirects(test.checksumTarget, test.archiveTarget)
			client := newTestClient(t, t.TempDir(), officialClient(f.Server), nil)

			_, err := client.Preflight(context.Background(), fixtureVersion)
			if err == nil {
				t.Fatalf("Preflight followed a %s", test.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.wantErr)) {
				t.Fatalf("Preflight error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}

// clientWithHead serves index and checksum requests from the fixture but
// synthesizes HEAD responses so tests can omit or spoof Content-Length.
func clientWithHead(f *fixture, contentLength *int64) *http.Client {
	target := strings.TrimPrefix(f.URL, "http://")
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodHead && strings.HasSuffix(req.URL.Path, ".tar.gz") {
				f.sizeRequests.Add(1)
				header := make(http.Header)
				if contentLength != nil {
					header.Set("Content-Length", strconv.FormatInt(*contentLength, 10))
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     header,
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    req,
				}, nil
			}
			return rewriteAndSend(req, target)
		}),
		Timeout: 5 * time.Second,
	}
}
