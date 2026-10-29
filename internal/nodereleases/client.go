package nodereleases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
)

const (
	// DefaultIndexTTL is how long a cached release index is served without a
	// network request. Refreshes stay explicit.
	DefaultIndexTTL = 6 * time.Hour

	// MaxArchiveSize bounds an official Node archive, matching the runtime
	// manager's 2 GiB extraction limit.
	MaxArchiveSize int64 = 2 << 30

	officialHost   = "nodejs.org"
	officialOrigin = "https://" + officialHost
	indexURL       = officialOrigin + "/dist/index.json"

	maxIndexBytes     = 16 << 20
	maxChecksumsBytes = 1 << 20
)

// Snapshot sources reported by List and Refresh.
const (
	SourceLive     = "live"
	SourceCache    = "cache"
	SourceEmbedded = "embedded"
)

// Config configures a discovery client. Home is the devstack home directory.
// HTTPClient is optional and exists for tests and custom transports; redirects
// are always restricted to the official host. Now and IndexTTL are optional
// test seams.
type Config struct {
	Home       string
	HTTPClient *http.Client
	Now        func() time.Time
	IndexTTL   time.Duration
}

// ListOptions controls how a snapshot is presented. Major limits the result to
// one major version (zero keeps every major). All expands each major's Releases
// from just the newest patch to every stable release. Offline forbids network
// requests and serves the cache or embedded catalog.
type ListOptions struct {
	Major   int
	All     bool
	Offline bool
}

// Snapshot is one consistent view of the discovered Node releases. Majors is
// grouped and honors the All expansion; All is the flat, newest-first list of
// the same selection before expansion.
type Snapshot struct {
	Majors   []Major   `json:"majors"`
	All      []Release `json:"all"`
	Source   string    `json:"source"`
	CachedAt time.Time `json:"cached_at"`
	Stale    bool      `json:"stale"`
	Warning  string    `json:"warning,omitempty"`
}

// Preflight is the fully pinned install metadata for one exact release.
type Preflight struct {
	Entry   catalog.Entry `json:"entry"`
	Release Release       `json:"release"`
}

// Client caches the official Node release index and performs lazy checksum and
// size preflight for installs.
type Client struct {
	cache cacheLayout
	http  *http.Client
	now   func() time.Time
	ttl   time.Duration

	mu sync.Mutex
}

// loadedIndex is the internal release set with its provenance.
type loadedIndex struct {
	releases []Release
	source   string
	cachedAt time.Time
	stale    bool
	warning  string
}

// New validates the configuration and returns a discovery client. No network
// request is made until List, Refresh, or Preflight needs fresh data.
func New(config Config) (*Client, error) {
	if config.Home == "" {
		return nil, errors.New("nodereleases: home is required")
	}

	httpClient := http.Client{Timeout: 30 * time.Second}
	if config.HTTPClient != nil {
		httpClient = *config.HTTPClient
	}
	httpClient.CheckRedirect = checkRedirect

	now := time.Now
	if config.Now != nil {
		now = config.Now
	}
	ttl := config.IndexTTL
	if ttl <= 0 {
		ttl = DefaultIndexTTL
	}

	return &Client{
		cache: newCacheLayout(config.Home),
		http:  &httpClient,
		now:   now,
		ttl:   ttl,
	}, nil
}

// List returns a snapshot while honoring the freshness policy: a fresh cache
// performs no request, a failed refresh serves the cache as stale, and a
// missing cache falls back to the embedded catalog.
func (c *Client) List(ctx context.Context, options ListOptions) (Snapshot, error) {
	if options.Major < 0 {
		return Snapshot{}, fmt.Errorf("nodereleases: invalid major %d", options.Major)
	}

	loaded, err := c.loadIndex(ctx, options.Offline, false)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshotFrom(loaded, options), nil
}

// Refresh forces an index fetch. When the fetch fails it serves the cached
// index as stale, or the embedded catalog when no cache exists. It returns an
// error only when no snapshot can be produced.
func (c *Client) Refresh(ctx context.Context) (Snapshot, error) {
	loaded, err := c.loadIndex(ctx, false, true)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshotFrom(loaded, ListOptions{}), nil
}

// Preflight resolves a selector and returns the exact archive entry derived
// from the official checksum file and a positive Content-Length. Cached
// checksum and size documents are reused only after revalidation.
func (c *Client) Preflight(ctx context.Context, selector string) (Preflight, error) {
	loaded, err := c.loadIndex(ctx, false, false)
	if err != nil {
		return Preflight{}, err
	}
	release, err := Select(loaded.releases, selector)
	if err != nil {
		return Preflight{}, err
	}

	filename := archiveName(release.Version)
	checksum, err := c.checksum(ctx, release.Version, filename)
	if err != nil {
		return Preflight{}, err
	}
	size, err := c.archiveSize(ctx, release.Version)
	if err != nil {
		return Preflight{}, err
	}

	entry := catalog.Entry{
		ID:      "node@" + release.Version,
		Kind:    "node",
		Version: release.Version,
		Build:   "linux-x64",
		URL:     archiveURL(release.Version),
		SHA256:  checksum,
		Size:    size,
	}
	if err := validatePinnedEntry(entry); err != nil {
		return Preflight{}, err
	}
	return Preflight{Entry: entry, Release: release}, nil
}

func (c *Client) loadIndex(ctx context.Context, offline, force bool) (loadedIndex, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cached, hasCache := c.cache.readIndex()
	if offline {
		if hasCache {
			stale := !c.fresh(cached.CachedAt)
			warning := ""
			if stale {
				warning = fmt.Sprintf("offline: cached node release index is older than %s", c.ttl)
			}
			return c.fromCache(cached, stale, warning), nil
		}
		return c.embeddedIndex("offline: no cached node release index")
	}

	if hasCache && !force && c.fresh(cached.CachedAt) {
		return c.fromCache(cached, false, ""), nil
	}

	releases, err := c.fetchIndex(ctx)
	if err != nil {
		return c.indexFallback(cached, hasCache,
			fmt.Sprintf("could not refresh the node release index: %v", err))
	}
	// Checksums and sizes are tied to the index generation. Drop lazily
	// fetched metadata so the next Preflight revalidates it against this
	// refresh instead of serving documents cached for an older index.
	if err := c.cache.invalidateMetadata(); err != nil {
		return c.indexFallback(cached, hasCache,
			fmt.Sprintf("could not revalidate cached checksums and sizes: %v", err))
	}

	now := c.now()
	warning := ""
	if err := c.cache.writeIndex(now, releases); err != nil {
		warning = fmt.Sprintf("could not cache the node release index: %v", err)
	}
	return loadedIndex{
		releases: c.mergeEmbedded(releases),
		source:   SourceLive,
		cachedAt: now,
		warning:  warning,
	}, nil
}

// indexFallback serves the stale cache or embedded catalog after a refresh
// failure. It returns an error only when neither source can be loaded.
func (c *Client) indexFallback(cached indexCacheDocument, hasCache bool, warning string) (loadedIndex, error) {
	if hasCache {
		return c.fromCache(cached, true, warning), nil
	}
	return c.embeddedIndex(warning)
}

func (c *Client) fromCache(cached indexCacheDocument, stale bool, warning string) loadedIndex {
	return loadedIndex{
		releases: c.mergeEmbedded(cached.Releases),
		source:   SourceCache,
		cachedAt: cached.CachedAt,
		stale:    stale,
		warning:  warning,
	}
}

func (c *Client) embeddedIndex(warning string) (loadedIndex, error) {
	releases, err := embeddedReleases()
	if err != nil {
		return loadedIndex{}, err
	}
	return loadedIndex{
		releases: releases,
		source:   SourceEmbedded,
		stale:    true,
		warning:  warning,
	}, nil
}

func (c *Client) fresh(cachedAt time.Time) bool {
	age := c.now().Sub(cachedAt)
	return age >= 0 && age < c.ttl
}

func (c *Client) fetchIndex(ctx context.Context) ([]Release, error) {
	data, err := c.get(ctx, indexURL, maxIndexBytes)
	if err != nil {
		return nil, err
	}
	releases, err := ParseIndex(data)
	if err != nil {
		return nil, err
	}
	if len(releases) == 0 {
		return nil, errors.New("nodereleases: index has no eligible releases")
	}
	return releases, nil
}

func (c *Client) checksum(ctx context.Context, version, filename string) (string, error) {
	if checksum, ok := c.cache.readChecksum(version, filename); ok {
		return checksum, nil
	}

	data, err := c.get(ctx, checksumURL(version), maxChecksumsBytes)
	if err != nil {
		return "", fmt.Errorf("nodereleases: fetch checksums for %s: %w", version, err)
	}
	checksum, err := checksumForFile(data, filename)
	if err != nil {
		return "", err
	}
	// The checksum is already authoritative for this request; a cache write
	// failure must not block an otherwise verified install.
	_ = c.cache.writeChecksums(version, data)
	return checksum, nil
}

func (c *Client) archiveSize(ctx context.Context, version string) (int64, error) {
	if size, ok := c.cache.readSize(version); ok {
		return size, nil
	}

	url := archiveURL(version)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, fmt.Errorf("nodereleases: HEAD %s: %w", url, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("nodereleases: HEAD %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("nodereleases: HEAD %s: unexpected status %s", url, resp.Status)
	}

	raw := resp.Header.Get("Content-Length")
	if raw == "" {
		return 0, fmt.Errorf("nodereleases: archive %s has no Content-Length", url)
	}
	size, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("nodereleases: archive %s has an invalid Content-Length %q", url, raw)
	}
	if size <= 0 {
		return 0, fmt.Errorf("nodereleases: archive %s has a non-positive Content-Length %d", url, size)
	}
	if size > MaxArchiveSize {
		return 0, fmt.Errorf("nodereleases: archive %s size %d exceeds the %d-byte limit", url, size, MaxArchiveSize)
	}
	// The size is already validated for this request; a cache write failure
	// must not block an otherwise verified install.
	_ = c.cache.writeSize(version, size)
	return size, nil
}

func (c *Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("nodereleases: GET %s: %w", url, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nodereleases: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nodereleases: GET %s: unexpected status %s", url, resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("nodereleases: GET %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("nodereleases: GET %s: response exceeds %d bytes", url, limit)
	}
	return data, nil
}

func (c *Client) mergeEmbedded(releases []Release) []Release {
	embedded, err := embeddedReleases()
	if err != nil {
		return append([]Release(nil), releases...)
	}
	return mergeReleases(releases, embedded)
}

// embeddedReleases converts the pinned Node builds in the embedded catalog to
// release records. The catalog is always available offline.
func embeddedReleases() ([]Release, error) {
	embedded, err := catalog.Load()
	if err != nil {
		return nil, fmt.Errorf("nodereleases: load embedded catalog: %w", err)
	}
	releases := make([]Release, 0, len(embedded.Entries))
	for _, entry := range embedded.Entries {
		if entry.Kind != "node" || entry.Build != "linux-x64" {
			continue
		}
		releases = append(releases, Release{
			Version: entry.Version,
			File:    archiveName(entry.Version),
		})
	}
	if len(releases) == 0 {
		return nil, errors.New("nodereleases: embedded catalog has no node releases")
	}
	return mergeReleases(nil, releases), nil
}

// mergeReleases keeps live entries authoritative and adds embedded versions
// that the live index does not mention. Results are newest first.
func mergeReleases(primary, embedded []Release) []Release {
	merged := make([]Release, 0, len(primary)+len(embedded))
	seen := make(map[string]struct{}, len(primary)+len(embedded))
	for _, group := range [][]Release{primary, embedded} {
		for _, release := range group {
			if _, duplicate := seen[release.Version]; duplicate {
				continue
			}
			seen[release.Version] = struct{}{}
			merged = append(merged, release)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		return compareVersions(merged[i].Version, merged[j].Version) > 0
	})
	return merged
}

func snapshotFrom(loaded loadedIndex, options ListOptions) Snapshot {
	releases := loaded.releases
	if options.Major > 0 {
		filtered := make([]Release, 0, len(releases))
		for _, release := range releases {
			major, _, _, ok := splitVersion(release.Version)
			if ok && major == options.Major {
				filtered = append(filtered, release)
			}
		}
		releases = filtered
	}

	majors := GroupByMajor(releases)
	if !options.All {
		for i := range majors {
			majors[i].Releases = []Release{majors[i].Latest}
		}
	}
	all := append([]Release(nil), releases...)
	if all == nil {
		all = []Release{}
	}

	return Snapshot{
		Majors:   majors,
		All:      all,
		Source:   loaded.source,
		CachedAt: loaded.cachedAt,
		Stale:    loaded.stale,
		Warning:  loaded.warning,
	}
}

// checkRedirect fails closed on redirects that leave HTTPS or nodejs.org.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("nodereleases: stopped after 10 redirects")
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("nodereleases: redirect to non-HTTPS URL %q", req.URL.Redacted())
	}
	if !strings.EqualFold(req.URL.Host, officialHost) {
		return fmt.Errorf("nodereleases: redirect to off-host URL %q (want %s)", req.URL.Redacted(), officialHost)
	}
	if req.URL.User != nil {
		return fmt.Errorf("nodereleases: redirect URL %q contains userinfo", req.URL.Redacted())
	}
	return nil
}

func archiveName(version string) string {
	return "node-v" + version + "-linux-x64.tar.gz"
}

func archiveURL(version string) string {
	return officialOrigin + "/dist/v" + version + "/" + archiveName(version)
}

func checksumURL(version string) string {
	return officialOrigin + "/dist/v" + version + "/SHASUMS256.txt"
}

// validatePinnedEntry rejects an entry that could not have come from the
// official Linux x64 archive derivation.
func validatePinnedEntry(entry catalog.Entry) error {
	if entry.Kind != "node" {
		return fmt.Errorf("nodereleases: pinned entry kind %q is not node", entry.Kind)
	}
	if entry.Version == "" || entry.ID != "node@"+entry.Version {
		return fmt.Errorf("nodereleases: pinned entry id %q does not match version %q", entry.ID, entry.Version)
	}
	if _, _, _, ok := splitVersion(entry.Version); !ok {
		return fmt.Errorf("nodereleases: pinned entry version %q is invalid", entry.Version)
	}
	if entry.Build != "linux-x64" {
		return fmt.Errorf("nodereleases: pinned entry build %q is not linux-x64", entry.Build)
	}
	if entry.Revision != 0 {
		return fmt.Errorf("nodereleases: pinned node entry carries revision %d", entry.Revision)
	}
	if entry.URL != archiveURL(entry.Version) {
		return fmt.Errorf("nodereleases: pinned entry url %q is not the official archive", entry.URL)
	}
	if !checksumLinePattern.MatchString(entry.SHA256) {
		return fmt.Errorf("nodereleases: pinned entry sha256 %q is not 64 lowercase hex characters", entry.SHA256)
	}
	if entry.Size <= 0 {
		return fmt.Errorf("nodereleases: pinned entry size %d is not positive", entry.Size)
	}
	if entry.Size > MaxArchiveSize {
		return fmt.Errorf("nodereleases: pinned entry size %d exceeds the %d-byte limit", entry.Size, MaxArchiveSize)
	}
	return nil
}
