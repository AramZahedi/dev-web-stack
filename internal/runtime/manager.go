// Package runtime installs, verifies, and removes the PHP and Node
// distributions used by devstack sites. Installation is deliberately strict:
// archives are pinned by size and SHA-256, extracted with traversal and link
// guards, and recorded with a manifest that Verify can re-check later.
package runtime

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// Entry is one runtime build known to the catalog.
type Entry = catalog.Entry

var (
	// ErrNoCatalogEntry is returned when no pinned build exists for a runtime
	// kind and version. Callers can surface it as an actionable "version not
	// available" message without parsing strings.
	ErrNoCatalogEntry = errors.New("runtime: no catalog entry")
	// ErrChecksumMismatch reports an archive whose size or SHA-256 differs from
	// the pinned catalog entry.
	ErrChecksumMismatch = errors.New("runtime: checksum mismatch")
	// ErrUnsafeArchive reports an archive member that could escape the
	// extraction root or uses an unsupported special file type.
	ErrUnsafeArchive = errors.New("runtime: unsafe archive")
	// ErrUnsafePath reports a stored runtime path outside the managed root or
	// one that traverses a symlink.
	ErrUnsafePath = errors.New("runtime: path outside managed runtime root")
)

const manifestName = "manifest.json"

// archiveLimits bound how much a decompressed tarball may produce. They are
// defensive limits: every runtime archive is checksum-pinned, but a corrupt or
// malicious archive must not be able to exhaust memory or disk during
// extraction.
type archiveLimits struct {
	maxEntries int
	maxBytes   int64
}

var defaultArchiveLimits = archiveLimits{
	maxEntries: 200_000,
	maxBytes:   2 << 30, // 2 GiB
}

// errDecompressionLimit is returned by limitedReader when the archive expands
// beyond the configured budget.
var errDecompressionLimit = errors.New("archive decompression limit exceeded")

// manifest records every regular file produced by an extraction. Verify uses it
// to detect deleted, added, mode-changed, and modified files. Relative symlinks
// are recorded separately so npm/npx/corepack links can be checked as well.
type manifest struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	Version     string         `json:"version"`
	Build       string         `json:"build"`
	SHA256      string         `json:"sha256"`
	Size        int64          `json:"size"`
	InstalledAt time.Time      `json:"installed_at"`
	Files       []manifestFile `json:"files"`
	Links       []manifestLink `json:"links,omitempty"`
}

type manifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}

type manifestLink struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

// Manager owns the on-disk runtime tree and its store metadata. Mutating
// operations are serialised per devstack home, including across Manager
// instances in the same process, so concurrent installs of one id cannot
// interleave downloads or extractions.
type Manager struct {
	stores       *store.Store
	runtimesRoot string
	cacheDir     string
	client       *http.Client
	catalog      catalog.Catalog
	catalogErr   error
	limits       archiveLimits
	mu           *sync.RWMutex
}

// homeLocks serialises mutations for a devstack home. A keyed lock rather than
// a struct field means two managers opened on the same home cooperate.
var homeLocks sync.Map // map[string]*sync.RWMutex

// NewManager returns a runtime manager rooted at home. The catalog is loaded
// from the binary; a load failure is surfaced by operations that need it rather
// than by construction. A nil client uses http.DefaultClient.
func NewManager(stores *store.Store, home string, client *http.Client) *Manager {
	cat, err := catalog.Load()
	return newManager(stores, home, client, cat, err)
}

// NewManagerWithCatalog returns a manager using an explicit catalog. It is the
// seam used by tests and by callers that already hold a validated catalog.
func NewManagerWithCatalog(stores *store.Store, home string, client *http.Client, cat catalog.Catalog) *Manager {
	return newManager(stores, home, client, cat, nil)
}

// newManager is the test seam that accepts a fixture catalog or a deliberate
// catalog error.
func newManager(stores *store.Store, home string, client *http.Client, cat catalog.Catalog, loadErr error) *Manager {
	resolved := resolveHome(home)
	if client == nil {
		client = http.DefaultClient
	}
	return &Manager{
		stores:       stores,
		runtimesRoot: filepath.Join(resolved, "runtimes"),
		cacheDir:     filepath.Join(resolved, "cache"),
		client:       client,
		catalog:      cat,
		catalogErr:   loadErr,
		limits:       defaultArchiveLimits,
		mu:           lockForHome(resolved),
	}
}

func resolveHome(home string) string {
	if home == "" {
		home = "."
	}
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	return filepath.Clean(home)
}

func lockForHome(home string) *sync.RWMutex {
	value, _ := homeLocks.LoadOrStore(home, &sync.RWMutex{})
	return value.(*sync.RWMutex)
}

// List returns every installed runtime build.
func (m *Manager) List(ctx context.Context) ([]model.RuntimeBuild, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if m.stores == nil {
		return nil, errors.New("runtime: no store configured")
	}
	return m.stores.ListRuntimes(ctx)
}

// Available returns the catalog entries compiled into this binary.
func (m *Manager) Available() []Entry {
	entries := make([]Entry, len(m.catalog.Entries))
	copy(entries, m.catalog.Entries)
	return entries
}

// Install downloads the pinned archive for id, verifies it, and installs it
// under runtimes/<kind>/<version>-<build>. Reinstalling an intact build is a
// no-op.
func (m *Manager) Install(ctx context.Context, id string) (model.RuntimeBuild, error) {
	entry, err := m.entryByID(id)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	return m.installEntry(ctx, entry, "")
}

// InstallFile installs a tarball supplied by the caller, using the same pinned
// checksum and extraction path as a download. It supports fully offline
// machines.
func (m *Manager) InstallFile(ctx context.Context, id, source string) (model.RuntimeBuild, error) {
	entry, err := m.entryByID(id)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	if source == "" {
		return model.RuntimeBuild{}, errors.New("runtime: install file: empty source path")
	}
	return m.installEntry(ctx, entry, source)
}

func (m *Manager) entryByID(id string) (catalog.Entry, error) {
	if m.catalogErr != nil {
		return catalog.Entry{}, fmt.Errorf("runtime: load catalog: %w", m.catalogErr)
	}
	entry, ok := m.catalog.Find(id)
	if !ok {
		return catalog.Entry{}, fmt.Errorf("runtime: install %q: %w", id, ErrNoCatalogEntry)
	}
	return entry, nil
}

func (m *Manager) installEntry(ctx context.Context, entry catalog.Entry, source string) (model.RuntimeBuild, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return model.RuntimeBuild{}, err
	}
	if m.stores == nil {
		return model.RuntimeBuild{}, errors.New("runtime: no store configured")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return model.RuntimeBuild{}, err
	}

	target, err := m.targetPath(entry)
	if err != nil {
		return model.RuntimeBuild{}, err
	}

	// Replace an existing row when it is stale, points at a different path, or
	// was pinned to a different archive. An intact installation is returned
	// unchanged, which makes concurrent installs idempotent.
	existing, err := m.stores.GetRuntime(ctx, entry.ID)
	switch {
	case err == nil:
		if m.installedBuildMatches(existing, entry, target) {
			return existing, nil
		}
		if err := m.removeLocked(ctx, existing); err != nil && !errors.Is(err, store.ErrNotFound) {
			return model.RuntimeBuild{}, err
		}
	case errors.Is(err, store.ErrNotFound):
		// No row: a crash may still have left the target behind.
	default:
		return model.RuntimeBuild{}, fmt.Errorf("runtime: read installed %q: %w", entry.ID, err)
	}

	if err := os.MkdirAll(m.runtimesRoot, 0o755); err != nil {
		return model.RuntimeBuild{}, fmt.Errorf("runtime: create %s: %w", m.runtimesRoot, err)
	}
	// Remove a directory left by an interrupted rename/upsert. target has
	// already been checked to be below the managed root.
	if err := os.RemoveAll(target); err != nil {
		return model.RuntimeBuild{}, fmt.Errorf("runtime: clear stale target %s: %w", target, err)
	}

	partial := ""
	if source == "" {
		if err := os.MkdirAll(m.cacheDir, 0o755); err != nil {
			return model.RuntimeBuild{}, fmt.Errorf("runtime: create %s: %w", m.cacheDir, err)
		}
		partial = filepath.Join(m.cacheDir, entry.ID+".part")
		if err := m.download(ctx, entry, partial); err != nil {
			_ = os.Remove(partial)
			return model.RuntimeBuild{}, err
		}
		defer func() {
			_ = os.Remove(partial)
		}()
		source = partial
	} else if err := verifyArchiveFile(source, entry); err != nil {
		return model.RuntimeBuild{}, err
	}

	if err := ctx.Err(); err != nil {
		return model.RuntimeBuild{}, err
	}

	tempDir, err := os.MkdirTemp(m.runtimesRoot, "install-"+safeTempName(entry.ID)+"-")
	if err != nil {
		return model.RuntimeBuild{}, fmt.Errorf("runtime: create extraction directory: %w", err)
	}
	if err := os.Chmod(tempDir, 0o755); err != nil {
		_ = os.RemoveAll(tempDir)
		return model.RuntimeBuild{}, fmt.Errorf("runtime: chmod extraction directory: %w", err)
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.RemoveAll(tempDir)
		}
	}()

	wrapper, err := scanArchive(source, m.limits)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	files, links, err := extractArchive(source, tempDir, wrapper, m.limits)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	if err := writeManifest(tempDir, manifest{
		ID:          entry.ID,
		Kind:        entry.Kind,
		Version:     entry.Version,
		Build:       entry.Build,
		SHA256:      entry.SHA256,
		Size:        entry.Size,
		InstalledAt: time.Now().UTC(),
		Files:       files,
		Links:       links,
	}); err != nil {
		return model.RuntimeBuild{}, err
	}

	if err := ctx.Err(); err != nil {
		return model.RuntimeBuild{}, err
	}

	// A PHP build is only registered after its own FPM master has started and
	// answered a FastCGI request. The check runs against the complete temporary
	// tree, so a failure removes the temp directory and leaves no runtime row.
	if entry.Kind == "php" {
		if err := phpSmoke(ctx, tempDir); err != nil {
			return model.RuntimeBuild{}, fmt.Errorf("runtime: php smoke test %q: %w", entry.ID, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return model.RuntimeBuild{}, fmt.Errorf("runtime: create %s: %w", filepath.Dir(target), err)
	}

	// The rename is atomic: readers see either the complete extracted tree or
	// no build at all.
	if err := os.Rename(tempDir, target); err != nil {
		return model.RuntimeBuild{}, fmt.Errorf("runtime: install %q at %s: %w", entry.ID, target, err)
	}
	keepTemp = true

	build := model.RuntimeBuild{
		ID:          entry.ID,
		Kind:        entry.Kind,
		Version:     entry.Version,
		Backend:     model.BackendLocal,
		Path:        target,
		Checksum:    entry.SHA256,
		InstalledAt: time.Now().UTC(),
	}
	if err := m.stores.UpsertRuntime(ctx, build); err != nil {
		_ = os.RemoveAll(target)
		return model.RuntimeBuild{}, fmt.Errorf("runtime: register %q: %w", entry.ID, err)
	}
	return build, nil
}

// Remove deletes an installed build and its store row. Removing an already
// absent build is a no-op so cleanup is idempotent. It refuses paths outside the
// managed runtime root and paths that are not an exact
// <root>/<kind>/<version>-<build> directory.
func (m *Manager) Remove(ctx context.Context, buildID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if m.stores == nil {
		return errors.New("runtime: no store configured")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	build, err := m.stores.GetRuntime(ctx, buildID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("runtime: remove %q: %w", buildID, err)
	}
	return m.removeLocked(ctx, build)
}

func (m *Manager) removeLocked(ctx context.Context, build model.RuntimeBuild) error {
	if err := m.checkBuildPath(build); err != nil {
		return fmt.Errorf("runtime: remove %q: %w", build.ID, err)
	}
	if err := os.RemoveAll(build.Path); err != nil {
		return fmt.Errorf("runtime: remove %q files: %w", build.ID, err)
	}
	if err := m.stores.DeleteRuntime(ctx, build.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("runtime: remove %q row: %w", build.ID, err)
	}
	return nil
}

// Ensure returns an intact local build for kind and version, or installs the
// pinned catalog entry. It is the backend.RuntimeProvider entry point.
func (m *Manager) Ensure(ctx context.Context, kind, version string) (model.RuntimeBuild, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return model.RuntimeBuild{}, err
	}
	if m.stores == nil {
		return model.RuntimeBuild{}, errors.New("runtime: no store configured")
	}

	builds, err := m.stores.ListRuntimes(ctx)
	if err != nil {
		return model.RuntimeBuild{}, fmt.Errorf("runtime: list installed builds: %w", err)
	}
	for _, build := range builds {
		if build.Kind != kind || build.Version != version || build.Backend != model.BackendLocal {
			continue
		}
		if m.installedBuildAt(build.Path) {
			return build, nil
		}
		// A registered build whose files vanished is repaired by installing
		// the pinned archive again.
		if err := m.Remove(ctx, build.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return model.RuntimeBuild{}, err
		}
	}

	if m.catalogErr != nil {
		return model.RuntimeBuild{}, fmt.Errorf("runtime: ensure %s %s: %w", kind, version, m.catalogErr)
	}
	entry, ok := m.catalog.ForKindVersion(kind, version)
	if !ok {
		return model.RuntimeBuild{}, fmt.Errorf("runtime: ensure %s %s: %w", kind, version, ErrNoCatalogEntry)
	}
	return m.Install(ctx, entry.ID)
}

// EnsureBuild returns an intact installed build for the exact id, or installs
// the pinned catalog entry. An intact row is returned without consulting the
// catalog, so a retained build remains usable after it leaves the embedded
// catalog and a catalog load failure cannot break rollback.
func (m *Manager) EnsureBuild(ctx context.Context, buildID string) (model.RuntimeBuild, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return model.RuntimeBuild{}, err
	}
	if m.stores == nil {
		return model.RuntimeBuild{}, errors.New("runtime: no store configured")
	}

	existing, err := m.stores.GetRuntime(ctx, buildID)
	switch {
	case err == nil:
		if m.intactInstalledBuild(existing) {
			return existing, nil
		}
	case errors.Is(err, store.ErrNotFound):
	default:
		return model.RuntimeBuild{}, fmt.Errorf("runtime: ensure build %q: %w", buildID, err)
	}

	entry, err := m.entryByID(buildID)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	return m.installEntry(ctx, entry, "")
}

// intactInstalledBuild reports whether a store row still describes a complete
// managed installation. It deliberately does not require a catalog entry.
func (m *Manager) intactInstalledBuild(build model.RuntimeBuild) bool {
	parsed, err := catalog.ParseID(build.ID)
	if err != nil || build.Backend != model.BackendLocal ||
		parsed.Kind != build.Kind || parsed.Version != build.Version {
		return false
	}
	if err := m.checkBuildPath(build); err != nil {
		return false
	}
	return m.installedBuildAt(build.Path)
}

// InstalledForLine returns installed builds for a runtime kind and major.minor
// line, newest version first and newest revision first within a version.
func (m *Manager) InstalledForLine(ctx context.Context, kind, line string) ([]model.RuntimeBuild, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if m.stores == nil {
		return nil, errors.New("runtime: no store configured")
	}
	builds, err := m.stores.ListRuntimes(ctx)
	if err != nil {
		return nil, fmt.Errorf("runtime: list installed builds: %w", err)
	}
	installed := make([]model.RuntimeBuild, 0, len(builds))
	for _, build := range builds {
		if build.Kind != kind || catalog.LineOf(build.Version) != line {
			continue
		}
		parsed, err := catalog.ParseID(build.ID)
		if err != nil || parsed.Kind != kind || parsed.Version != build.Version {
			continue
		}
		installed = append(installed, build)
	}
	sort.SliceStable(installed, func(i, j int) bool {
		if cmp := compareRuntimeVersions(installed[i].Version, installed[j].Version); cmp != 0 {
			return cmp > 0
		}
		return runtimeRevision(installed[i].ID) > runtimeRevision(installed[j].ID)
	})
	return installed, nil
}

func compareRuntimeVersions(a, b string) int {
	ap, bp := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		var av, bv int
		if i < len(ap) {
			av, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			bv, _ = strconv.Atoi(bp[i])
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

func runtimeRevision(id string) int {
	parsed, err := catalog.ParseID(id)
	if err != nil {
		return 0
	}
	return parsed.Revision
}

// Verify re-hashes every file recorded in the build's manifest and reports
// missing, extra, mode-changed, symlinked, and modified files.
func (m *Manager) Verify(ctx context.Context, buildID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if m.stores == nil {
		return errors.New("runtime: no store configured")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	build, err := m.stores.GetRuntime(ctx, buildID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("runtime: verify %q: %w", buildID, store.ErrNotFound)
		}
		return fmt.Errorf("runtime: verify %q: %w", buildID, err)
	}
	if err := m.checkManagedPath(build.Path); err != nil {
		return fmt.Errorf("runtime: verify %q: %w", buildID, err)
	}
	info, err := os.Lstat(build.Path)
	if err != nil {
		return fmt.Errorf("runtime: verify %q: %w", buildID, err)
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("runtime: verify %q: %s is not a directory", buildID, build.Path)
	}
	return verifyInstalled(build)
}

func (m *Manager) installedBuildMatches(build model.RuntimeBuild, entry catalog.Entry, target string) bool {
	return build.Kind == entry.Kind &&
		build.Version == entry.Version &&
		build.Backend == model.BackendLocal &&
		build.Path == target &&
		build.Checksum == entry.SHA256 &&
		m.installedBuildAt(build.Path)
}

func (m *Manager) installedBuildAt(dir string) bool {
	if err := m.checkManagedPath(dir); err != nil {
		return false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	info, err = os.Stat(filepath.Join(dir, manifestName))
	return err == nil && info.Mode().IsRegular()
}

// targetPath derives and validates the managed install path for entry. PHP
// revisions get their own leaf so two revisions of one version never collide.
func (m *Manager) targetPath(entry catalog.Entry) (string, error) {
	for _, component := range []string{entry.Kind, entry.Version, entry.Build} {
		if err := safeComponent(component); err != nil {
			return "", err
		}
	}
	parsed, err := catalog.ParseID(entry.ID)
	if err != nil {
		return "", fmt.Errorf("%w: install %q: %v", ErrUnsafePath, entry.ID, err)
	}
	if parsed.Kind != entry.Kind || parsed.Version != entry.Version || parsed.Revision != entry.Revision {
		return "", fmt.Errorf("%w: install %q: id does not match entry identity", ErrUnsafePath, entry.ID)
	}
	leaf := entry.Version + "-" + entry.Build
	if entry.Revision > 0 {
		leaf = fmt.Sprintf("%s-r%d-%s", entry.Version, entry.Revision, entry.Build)
	}
	target := filepath.Join(m.runtimesRoot, entry.Kind, leaf)
	if err := m.checkManagedPath(target); err != nil {
		return "", fmt.Errorf("runtime: install %q: %w", entry.ID, err)
	}
	return target, nil
}

func safeComponent(value string) error {
	if value == "" || value == "." || value == ".." ||
		strings.ContainsAny(value, "/\\") || strings.ContainsRune(value, 0) ||
		filepath.Base(value) != value {
		return fmt.Errorf("%w: invalid path component %q", ErrUnsafePath, value)
	}
	return nil
}

// checkBuildPath requires the row to describe exactly one managed build:
// <root>/<kind>/<version>[-r<revision>]-<build>. The revision prefix comes from
// the row id, so a row cannot claim revision 2 while pointing at revision 1's
// directory.
func (m *Manager) checkBuildPath(build model.RuntimeBuild) error {
	if err := m.checkManagedPath(build.Path); err != nil {
		return err
	}
	if err := safeComponent(build.Kind); err != nil {
		return err
	}
	if err := safeComponent(build.Version); err != nil {
		return err
	}
	parsed, err := catalog.ParseID(build.ID)
	if err != nil {
		return fmt.Errorf("%w: runtime %q: %v", ErrUnsafePath, build.ID, err)
	}
	if parsed.Kind != build.Kind || parsed.Version != build.Version {
		return fmt.Errorf("%w: runtime %q id does not match row kind/version %s/%s",
			ErrUnsafePath, build.ID, build.Kind, build.Version)
	}

	abs, err := filepath.Abs(build.Path)
	if err != nil {
		return fmt.Errorf("%w: resolve %q: %v", ErrUnsafePath, build.Path, err)
	}
	rel, err := filepath.Rel(m.runtimesRoot, filepath.Clean(abs))
	if err != nil {
		return fmt.Errorf("%w: %q is not below %q", ErrUnsafePath, build.Path, m.runtimesRoot)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 2 {
		return fmt.Errorf("%w: runtime %q path %q is not <root>/<kind>/<version>-<build>",
			ErrUnsafePath, build.ID, build.Path)
	}
	kind, leaf := parts[0], parts[1]
	if kind != build.Kind {
		return fmt.Errorf("%w: runtime %q path kind %q does not match row kind %q",
			ErrUnsafePath, build.ID, kind, build.Kind)
	}
	if err := safeComponent(leaf); err != nil {
		return err
	}
	prefix := parsed.Version + "-"
	if parsed.Revision > 0 {
		prefix = fmt.Sprintf("%s-r%d-", parsed.Version, parsed.Revision)
	}
	if !strings.HasPrefix(leaf, prefix) {
		return fmt.Errorf("%w: runtime %q path %q does not start with %q",
			ErrUnsafePath, build.ID, build.Path, prefix)
	}
	buildPart := strings.TrimPrefix(leaf, prefix)
	if buildPart == "" {
		return fmt.Errorf("%w: runtime %q path %q has no build suffix", ErrUnsafePath, build.ID, build.Path)
	}
	if err := safeComponent(buildPart); err != nil {
		return err
	}
	if parsed.Revision == 0 && hasRevisionPrefix(buildPart) {
		return fmt.Errorf("%w: runtime %q path %q carries a revision for a node build",
			ErrUnsafePath, build.ID, build.Path)
	}
	return nil
}

// hasRevisionPrefix reports whether a build suffix starts with r<digits>-.
func hasRevisionPrefix(value string) bool {
	if !strings.HasPrefix(value, "r") {
		return false
	}
	rest := value[1:]
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	return digits > 0 && digits < len(rest) && rest[digits] == '-'
}

// checkManagedPath requires p to be strictly below the runtime root and every
// existing component to be a real directory rather than a symlink.
func (m *Manager) checkManagedPath(p string) error {
	if p == "" {
		return fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("%w: resolve %q: %v", ErrUnsafePath, p, err)
	}
	abs = filepath.Clean(abs)
	root := filepath.Clean(m.runtimesRoot)

	if info, statErr := os.Lstat(root); statErr == nil && info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: runtime root %q is a symlink", ErrUnsafePath, root)
	}

	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return fmt.Errorf("%w: %q is not below %q", ErrUnsafePath, p, root)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: %q is not below %q", ErrUnsafePath, p, root)
	}

	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, fs.ErrNotExist) {
			break
		}
		if statErr != nil {
			return fmt.Errorf("%w: inspect %q: %v", ErrUnsafePath, current, statErr)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %q traverses symlink %q", ErrUnsafePath, p, current)
		}
	}
	return nil
}

// verifyArchiveFile checks a caller-supplied tarball against the pinned size
// and SHA-256.
func verifyArchiveFile(source string, entry catalog.Entry) error {
	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("runtime: open archive %s: %w", source, err)
	}
	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("runtime: stat archive %s: %w", source, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("runtime: archive %s is not a regular file", source)
	}
	if info.Size() != entry.Size {
		return fmt.Errorf("runtime: archive %s size %d, want %d: %w",
			source, info.Size(), entry.Size, ErrChecksumMismatch)
	}

	hash := sha256.New()
	written, err := io.Copy(hash, file)
	if err != nil {
		return fmt.Errorf("runtime: hash archive %s: %w", source, err)
	}
	if written != entry.Size {
		return fmt.Errorf("runtime: archive %s read %d bytes, want %d: %w",
			source, written, entry.Size, ErrChecksumMismatch)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != entry.SHA256 {
		return fmt.Errorf("runtime: archive %s sha256 %s, want %s: %w",
			source, got, entry.SHA256, ErrChecksumMismatch)
	}
	return nil
}

// scanArchive validates every header and identifies an optional wrapper
// directory. Official Node tarballs put everything below
// node-vX-linux-x64/; a tarball whose roots are already bin/, lib/, or another
// standard entry is left untouched.
func scanArchive(archivePath string, limits archiveLimits) (string, error) {
	stream, err := openTarStream(archivePath, limits)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = stream.Close()
	}()

	roots := map[string]struct{}{}
	var firstRoot string
	nested := false
	budget := archiveBudget{limits: limits}
	for {
		header, err := stream.tar.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", archiveReadError(archivePath, err)
		}
		if err := budget.add(header); err != nil {
			return "", err
		}
		clean, err := validateHeader(header)
		if err != nil {
			return "", err
		}
		if clean == "." {
			continue
		}
		parts := strings.Split(clean, "/")
		if len(parts) > 1 {
			nested = true
		}
		if _, ok := roots[parts[0]]; !ok {
			roots[parts[0]] = struct{}{}
			if firstRoot == "" {
				firstRoot = parts[0]
			}
		}
	}
	if len(roots) == 1 && nested {
		candidate := firstRoot
		if _, standard := standardRootEntries[strings.ToLower(candidate)]; !standard {
			return candidate, nil
		}
	}
	return "", nil
}

// standardRootEntries are names that mean the archive is already rooted at the
// install layout and must not have its first component removed.
var standardRootEntries = map[string]struct{}{
	"bin": {}, "lib": {}, "lib64": {}, "include": {}, "share": {}, "src": {},
	"etc": {}, "license": {}, "license.md": {}, "license.txt": {},
	"readme": {}, "readme.md": {}, "changelog": {}, "changelog.md": {},
	"package.json": {}, "node": {}, "npm": {}, "node_modules": {},
}

// extractArchive extracts a validated tarball into dest, optionally removing a
// shared wrapper directory. It returns manifest entries for every regular file
// and safely-contained symlink written.
func extractArchive(archivePath, dest, wrapper string, limits archiveLimits) ([]manifestFile, []manifestLink, error) {
	stream, err := openTarStream(archivePath, limits)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = stream.Close()
	}()

	var files []manifestFile
	var links []manifestLink
	budget := archiveBudget{limits: limits}
	for {
		header, err := stream.tar.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, archiveReadError(archivePath, err)
		}
		if err := budget.add(header); err != nil {
			return nil, nil, err
		}
		clean, err := validateHeader(header)
		if err != nil {
			return nil, nil, err
		}
		if clean == "." {
			continue
		}
		rel := clean
		if wrapper != "" {
			if clean == wrapper {
				continue
			}
			if !strings.HasPrefix(clean, wrapper+"/") {
				return nil, nil, fmt.Errorf("%w: entry %q escapes wrapper %q", ErrUnsafeArchive, header.Name, wrapper)
			}
			rel = strings.TrimPrefix(clean, wrapper+"/")
		}
		if rel == "" || rel == "." {
			continue
		}
		if rel == manifestName {
			return nil, nil, fmt.Errorf("%w: entry %q uses the reserved manifest name", ErrUnsafeArchive, header.Name)
		}
		target, err := secureJoin(dest, rel)
		if err != nil {
			return nil, nil, err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := mkdirAllNoSymlink(dest, target); err != nil {
				return nil, nil, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := mkdirAllNoSymlink(dest, filepath.Dir(target)); err != nil {
				return nil, nil, err
			}
			mode := regularFileMode(header)
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, mode)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: create %q: %v", ErrUnsafeArchive, target, err)
			}
			hash := sha256.New()
			written, copyErr := io.Copy(io.MultiWriter(out, hash), stream.tar)
			closeErr := out.Close()
			if copyErr != nil {
				if errors.Is(copyErr, errDecompressionLimit) {
					return nil, nil, fmt.Errorf("%w: archive %s exceeds the decompression limit",
						ErrUnsafeArchive, archivePath)
				}
				return nil, nil, fmt.Errorf("runtime: extract %s: %w", target, copyErr)
			}
			if closeErr != nil {
				return nil, nil, fmt.Errorf("runtime: close %s: %w", target, closeErr)
			}
			if written != header.Size {
				return nil, nil, fmt.Errorf("%w: %q wrote %d bytes, want %d", ErrUnsafeArchive, header.Name, written, header.Size)
			}
			// O_CREATE honours the process umask, so set the recorded mode
			// explicitly. This is what keeps bin/node executable.
			if err := os.Chmod(target, mode); err != nil {
				return nil, nil, fmt.Errorf("runtime: chmod %s: %w", target, err)
			}
			files = append(files, manifestFile{
				Path:   filepath.ToSlash(rel),
				SHA256: hex.EncodeToString(hash.Sum(nil)),
				Mode:   uint32(mode.Perm()),
			})
		case tar.TypeSymlink:
			// Node's bin/npm, bin/npx, and bin/corepack are relative links
			// into lib/node_modules. Absolute links and links that resolve
			// outside the extraction root are rejected, including links that
			// only escape after following another link.
			if err := mkdirAllNoSymlink(dest, filepath.Dir(target)); err != nil {
				return nil, nil, err
			}
			if err := resolveLinkTarget(dest, rel, header.Linkname); err != nil {
				return nil, nil, err
			}
			if _, err := os.Lstat(target); err == nil {
				return nil, nil, fmt.Errorf("%w: duplicate entry %q", ErrUnsafeArchive, header.Name)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return nil, nil, fmt.Errorf("runtime: inspect %s: %w", target, err)
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return nil, nil, fmt.Errorf("runtime: create symlink %s: %w", target, err)
			}
			links = append(links, manifestLink{
				Path:   filepath.ToSlash(rel),
				Target: header.Linkname,
			})
		default:
			return nil, nil, fmt.Errorf("%w: unsupported entry type for %q", ErrUnsafeArchive, header.Name)
		}
	}

	// A link created before its target could become unsafe once later entries
	// exist (for example a link whose target path itself becomes a symlink).
	// Re-resolve every link against the complete tree before publishing it.
	if err := validateExtractedLinks(dest, links); err != nil {
		return nil, nil, err
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Slice(links, func(i, j int) bool { return links[i].Path < links[j].Path })
	return files, links, nil
}

// mkdirAllNoSymlink creates dir and its parents below root while refusing to
// traverse an existing symlink or non-directory component.
func mkdirAllNoSymlink(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return fmt.Errorf("%w: resolve %q: %v", ErrUnsafeArchive, dir, err)
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: directory %q escapes the extraction root", ErrUnsafeArchive, dir)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return fmt.Errorf("runtime: create directory %s: %w", current, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("runtime: inspect %s: %w", current, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: directory %q traverses symlink %q", ErrUnsafeArchive, dir, current)
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: %q is not a directory", ErrUnsafeArchive, current)
		}
	}
	return nil
}

// validateExtractedLinks re-resolves every link against the complete tree. A
// link created before its target could become unsafe once later entries exist.
func validateExtractedLinks(root string, links []manifestLink) error {
	for _, link := range links {
		if err := resolveLinkTarget(root, link.Path, link.Target); err != nil {
			return err
		}
	}
	return nil
}

const maxSymlinkHops = 40

// resolveLinkTarget follows rel's target component-by-component against the
// current tree. Following existing links before interpreting ".." is essential:
// a lexical path.Clean can hide an escape that the kernel will take after a
// symlink hop.
func resolveLinkTarget(root, rel, linkname string) error {
	if linkname == "" {
		return fmt.Errorf("%w: symlink %q has an empty target", ErrUnsafeArchive, rel)
	}
	if strings.ContainsRune(linkname, 0) || strings.Contains(linkname, "\\") {
		return fmt.Errorf("%w: symlink %q has an invalid target", ErrUnsafeArchive, rel)
	}
	if path.IsAbs(linkname) || strings.HasPrefix(linkname, "/") {
		return fmt.Errorf("%w: symlink %q is absolute", ErrUnsafeArchive, rel)
	}

	var parts []string
	base := path.Dir(rel)
	if base != "." && base != "" {
		parts = append(parts, strings.Split(base, "/")...)
	}
	parts = append(parts, strings.Split(linkname, "/")...)

	resolved, err := resolveLinkComponents(root, parts)
	if err != nil {
		return fmt.Errorf("%w: symlink %q target %q: %v", ErrUnsafeArchive, rel, linkname, err)
	}
	if resolved == "" || resolved == rel || strings.HasPrefix(rel, resolved+"/") {
		return fmt.Errorf("%w: symlink %q points at itself or an ancestor", ErrUnsafeArchive, rel)
	}
	return nil
}

func resolveLinkComponents(root string, parts []string) (string, error) {
	resolved := ""
	hops := 0
	for len(parts) > 0 {
		component := parts[0]
		parts = parts[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			if resolved == "" {
				return "", errors.New("target escapes the extraction root")
			}
			resolved = path.Dir(resolved)
			if resolved == "." {
				resolved = ""
			}
			continue
		}

		candidate := component
		if resolved != "" {
			candidate = resolved + "/" + component
		}
		full := filepath.Join(root, filepath.FromSlash(candidate))
		info, err := os.Lstat(full)
		if errors.Is(err, fs.ErrNotExist) {
			// Everything from here is dangling. Resolve the remainder
			// lexically but still reject any attempt to leave the root.
			return resolveMissingLinkComponents(resolved, append([]string{component}, parts...))
		}
		if err != nil {
			return "", fmt.Errorf("inspect %q: %v", candidate, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			hops++
			if hops > maxSymlinkHops {
				return "", fmt.Errorf("more than %d symlink hops", maxSymlinkHops)
			}
			target, err := os.Readlink(full)
			if err != nil {
				return "", fmt.Errorf("read symlink %q: %v", candidate, err)
			}
			if target == "" || strings.ContainsRune(target, 0) || strings.Contains(target, "\\") {
				return "", fmt.Errorf("link %q has an invalid target", candidate)
			}
			if path.IsAbs(target) || strings.HasPrefix(target, "/") {
				return "", fmt.Errorf("link %q has an absolute target", candidate)
			}
			parts = append(strings.Split(target, "/"), parts...)
			continue
		}
		if !info.IsDir() && len(parts) > 0 {
			return "", fmt.Errorf("component %q is not a directory", candidate)
		}
		resolved = candidate
	}
	return resolved, nil
}

func resolveMissingLinkComponents(base string, parts []string) (string, error) {
	resolved := base
	for _, component := range parts {
		switch component {
		case "", ".":
			continue
		case "..":
			if resolved == "" {
				return "", errors.New("target escapes the extraction root")
			}
			resolved = path.Dir(resolved)
			if resolved == "." {
				resolved = ""
			}
		default:
			if resolved == "" {
				resolved = component
			} else {
				resolved += "/" + component
			}
		}
	}
	return resolved, nil
}

func regularFileMode(header *tar.Header) fs.FileMode {
	mode := fs.FileMode(header.Mode).Perm()
	if mode == 0 {
		mode = 0o644
	}
	return mode
}

// validateHeader rejects hardlinks, devices, and other special entries. Symlink
// targets are checked syntactically here; containment is checked after the
// optional wrapper directory has been stripped.
func validateHeader(header *tar.Header) (string, error) {
	switch header.Typeflag {
	case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
	case tar.TypeSymlink:
		if header.Linkname == "" || strings.ContainsRune(header.Linkname, 0) || strings.Contains(header.Linkname, "\\") {
			return "", fmt.Errorf("%w: symlink %q has an invalid target", ErrUnsafeArchive, header.Name)
		}
		if path.IsAbs(header.Linkname) || strings.HasPrefix(header.Linkname, "/") {
			return "", fmt.Errorf("%w: symlink %q is absolute", ErrUnsafeArchive, header.Name)
		}
	default:
		return "", fmt.Errorf("%w: entry %q has unsupported type %q", ErrUnsafeArchive, header.Name, header.Typeflag)
	}
	clean, err := cleanArchivePath(header.Name)
	if err != nil {
		return "", err
	}
	return clean, nil
}

func cleanArchivePath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty archive entry name", ErrUnsafeArchive)
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: entry name contains NUL", ErrUnsafeArchive)
	}
	if strings.Contains(name, "\\") {
		return "", fmt.Errorf("%w: entry %q contains a backslash", ErrUnsafeArchive, name)
	}
	if path.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("%w: entry %q is absolute", ErrUnsafeArchive, name)
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: entry %q escapes the extraction root", ErrUnsafeArchive, name)
	}
	return clean, nil
}

func secureJoin(root, rel string) (string, error) {
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || path.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: entry %q escapes the extraction root", ErrUnsafeArchive, rel)
	}
	target := filepath.Join(root, filepath.FromSlash(clean))
	within, err := filepath.Rel(root, target)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) || filepath.IsAbs(within) {
		return "", fmt.Errorf("%w: entry %q escapes the extraction root", ErrUnsafeArchive, rel)
	}
	return target, nil
}

type tarStream struct {
	file *os.File
	gzip *gzip.Reader
	tar  *tar.Reader
}

// limitedReader caps the number of decompressed bytes read from gzip. Tar
// metadata overhead is covered by a small per-entry allowance in addition to
// the payload budget.
type limitedReader struct {
	reader    io.Reader
	remaining int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		return 0, errDecompressionLimit
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.reader.Read(p)
	l.remaining -= int64(n)
	return n, err
}

// archiveBudget enforces the per-archive entry and uncompressed-size limits
// before any member is written.
type archiveBudget struct {
	limits  archiveLimits
	entries int
	bytes   int64
}

func (b *archiveBudget) add(header *tar.Header) error {
	b.entries++
	if b.limits.maxEntries > 0 && b.entries > b.limits.maxEntries {
		return fmt.Errorf("%w: archive has more than %d entries",
			ErrUnsafeArchive, b.limits.maxEntries)
	}
	if header.Size < 0 {
		return fmt.Errorf("%w: entry %q has a negative size", ErrUnsafeArchive, header.Name)
	}
	if b.limits.maxBytes > 0 {
		if header.Size > b.limits.maxBytes-b.bytes {
			return fmt.Errorf("%w: archive expands beyond the %d byte limit",
				ErrUnsafeArchive, b.limits.maxBytes)
		}
		b.bytes += header.Size
	}
	return nil
}

func (l archiveLimits) decompressionLimit() int64 {
	if l.maxBytes <= 0 {
		return 1 << 30
	}
	overhead := int64(l.maxEntries) * 1024
	if overhead < 1<<20 {
		overhead = 1 << 20
	}
	return l.maxBytes + overhead
}

func openTarStream(archivePath string, limits archiveLimits) (*tarStream, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("runtime: open archive %s: %w", archivePath, err)
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: open gzip %s: %v", ErrUnsafeArchive, archivePath, err)
	}
	limited := &limitedReader{reader: reader, remaining: limits.decompressionLimit()}
	return &tarStream{file: file, gzip: reader, tar: tar.NewReader(limited)}, nil
}

func archiveReadError(archivePath string, err error) error {
	if errors.Is(err, errDecompressionLimit) {
		return fmt.Errorf("%w: archive %s exceeds the decompression limit", ErrUnsafeArchive, archivePath)
	}
	return fmt.Errorf("%w: read %s: %v", ErrUnsafeArchive, archivePath, err)
}

func (s *tarStream) Close() error {
	gzipErr := s.gzip.Close()
	fileErr := s.file.Close()
	if gzipErr != nil {
		return gzipErr
	}
	return fileErr
}

func writeManifest(dir string, document manifest) error {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("runtime: encode manifest: %w", err)
	}
	temp := filepath.Join(dir, manifestName+".tmp")
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return fmt.Errorf("runtime: write manifest: %w", err)
	}
	if err := os.Rename(temp, filepath.Join(dir, manifestName)); err != nil {
		return fmt.Errorf("runtime: install manifest: %w", err)
	}
	return nil
}

func verifyInstalled(build model.RuntimeBuild) error {
	data, err := os.ReadFile(filepath.Join(build.Path, manifestName))
	if err != nil {
		return fmt.Errorf("runtime: %q: read manifest: %w", build.ID, err)
	}
	var document manifest
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("runtime: %q: decode manifest: %w", build.ID, err)
	}
	if document.ID != build.ID || document.Kind != build.Kind || document.Version != build.Version {
		return fmt.Errorf("runtime: %q: manifest identity %s/%s/%s does not match the build",
			build.ID, document.Kind, document.Version, document.ID)
	}
	if document.SHA256 != "" && document.SHA256 != build.Checksum {
		return fmt.Errorf("runtime: %q: manifest archive checksum does not match the build", build.ID)
	}

	expected := make(map[string]manifestFile, len(document.Files))
	expectedLinks := make(map[string]string, len(document.Links))
	var issues []string
	for _, file := range document.Files {
		clean, err := cleanManifestPath(file.Path)
		if err != nil {
			issues = append(issues, err.Error())
			continue
		}
		if _, ok := expected[clean]; ok {
			issues = append(issues, fmt.Sprintf("duplicate manifest entry %q", clean))
			continue
		}
		expected[clean] = file
	}
	for _, link := range document.Links {
		clean, err := cleanManifestPath(link.Path)
		if err != nil {
			issues = append(issues, err.Error())
			continue
		}
		if _, ok := expectedLinks[clean]; ok {
			issues = append(issues, fmt.Sprintf("duplicate manifest symlink %q", clean))
			continue
		}
		if _, ok := expected[clean]; ok {
			issues = append(issues, fmt.Sprintf("manifest path %q is both a file and a symlink", clean))
			continue
		}
		expectedLinks[clean] = link.Target
	}

	seen := make(map[string]struct{}, len(expected))
	seenLinks := make(map[string]struct{}, len(expectedLinks))
	walkErr := filepath.WalkDir(build.Path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(build.Path, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == manifestName {
			if !entry.Type().IsRegular() {
				issues = append(issues, fmt.Sprintf("%s is not a regular file", manifestName))
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			want, ok := expectedLinks[rel]
			if !ok {
				issues = append(issues, fmt.Sprintf("unexpected symlink %q", rel))
				return nil
			}
			seenLinks[rel] = struct{}{}
			got, err := os.Readlink(current)
			if err != nil {
				issues = append(issues, fmt.Sprintf("read symlink %q: %v", rel, err))
				return nil
			}
			if got != want {
				issues = append(issues, fmt.Sprintf("symlink %q targets %q, want %q", rel, got, want))
			}
			if err := resolveLinkTarget(build.Path, rel, got); err != nil {
				issues = append(issues, err.Error())
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			issues = append(issues, fmt.Sprintf("unexpected non-regular file %q", rel))
			return nil
		}
		want, ok := expected[rel]
		if !ok {
			if _, linkPath := expectedLinks[rel]; linkPath {
				issues = append(issues, fmt.Sprintf("%q is a regular file, want a symlink", rel))
				return nil
			}
			issues = append(issues, fmt.Sprintf("unexpected file %q", rel))
			return nil
		}
		seen[rel] = struct{}{}
		info, err := entry.Info()
		if err != nil {
			issues = append(issues, fmt.Sprintf("stat %q: %v", rel, err))
			return nil
		}
		if uint32(info.Mode().Perm()) != want.Mode {
			issues = append(issues, fmt.Sprintf("mode of %q is %04o, want %04o", rel, info.Mode().Perm(), want.Mode))
		}
		sum, err := hashFile(current)
		if err != nil {
			issues = append(issues, fmt.Sprintf("hash %q: %v", rel, err))
			return nil
		}
		if sum != want.SHA256 {
			issues = append(issues, fmt.Sprintf("checksum mismatch for %q", rel))
		}
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("runtime: %q: walk installed files: %w", build.ID, walkErr)
	}
	for rel := range expected {
		if _, ok := seen[rel]; !ok {
			issues = append(issues, fmt.Sprintf("missing file %q", rel))
		}
	}
	for rel := range expectedLinks {
		if _, ok := seenLinks[rel]; !ok {
			issues = append(issues, fmt.Sprintf("missing symlink %q", rel))
		}
	}
	if len(issues) != 0 {
		sort.Strings(issues)
		return fmt.Errorf("runtime: %q: verification failed: %s", build.ID, strings.Join(issues, "; "))
	}
	return nil
}

func cleanManifestPath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, "\\") {
		return "", fmt.Errorf("invalid manifest path %q", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || path.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid manifest path %q", value)
	}
	if clean == manifestName {
		return "", fmt.Errorf("manifest path %q is reserved", value)
	}
	return clean, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func safeTempName(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	if builder.Len() == 0 {
		return "runtime"
	}
	return builder.String()
}
