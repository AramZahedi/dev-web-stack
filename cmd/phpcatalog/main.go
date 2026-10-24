// Command phpcatalog turns one built PHP artifact into one catalog entry. The
// CI release job runs it once per artifact and combines the entries itself; it
// never edits internal/catalog/catalog.json.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
)

var (
	versionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Options describes one PHP artifact and the release URL it will be published
// under. Output is optional: when set, Generate writes the single entry there.
type Options struct {
	Version  string
	Revision int
	Artifact string
	SHA256   string
	Size     int64
	BaseURL  string
	Output   string
}

// Generate validates opts, verifies the artifact on disk, and returns the
// catalog entry for it. It writes exactly one entry object to opts.Output when
// that field is set. An output path that resolves to the embedded production
// catalog is rejected; Generate never touches internal/catalog/catalog.json.
func Generate(opts Options) (catalog.Entry, error) {
	if !versionPattern.MatchString(opts.Version) {
		return catalog.Entry{}, fmt.Errorf("invalid PHP version %q (want x.y.z)", opts.Version)
	}
	if opts.Revision < 1 {
		return catalog.Entry{}, fmt.Errorf("invalid revision %d (want at least 1)", opts.Revision)
	}
	name := fmt.Sprintf("php-%s-r%d-linux-x64.tar.gz", opts.Version, opts.Revision)
	if filepath.Base(opts.Artifact) != name {
		return catalog.Entry{}, fmt.Errorf("artifact basename must be %q, got %q", name, filepath.Base(opts.Artifact))
	}
	if opts.Size <= 0 {
		return catalog.Entry{}, fmt.Errorf("invalid size %d (want a positive byte count)", opts.Size)
	}
	if !checksumPattern.MatchString(opts.SHA256) {
		return catalog.Entry{}, fmt.Errorf("invalid sha256 %q (want 64 lowercase hex characters)", opts.SHA256)
	}
	baseURL, err := validateBaseURL(opts.BaseURL)
	if err != nil {
		return catalog.Entry{}, err
	}

	info, err := os.Stat(opts.Artifact)
	if err != nil {
		return catalog.Entry{}, fmt.Errorf("stat artifact %s: %w", opts.Artifact, err)
	}
	if !info.Mode().IsRegular() {
		return catalog.Entry{}, fmt.Errorf("artifact %s is not a regular file", opts.Artifact)
	}
	if info.Size() != opts.Size {
		return catalog.Entry{}, fmt.Errorf("size mismatch: artifact is %d bytes, options say %d", info.Size(), opts.Size)
	}
	actual, err := fileSHA256(opts.Artifact)
	if err != nil {
		return catalog.Entry{}, err
	}
	if actual != opts.SHA256 {
		return catalog.Entry{}, fmt.Errorf("sha256 mismatch: artifact is %s, options say %s", actual, opts.SHA256)
	}

	entry := catalog.Entry{
		ID:       catalog.FormatID("php", opts.Version, opts.Revision),
		Kind:     "php",
		Version:  opts.Version,
		Build:    "linux-x64",
		Revision: opts.Revision,
		URL:      baseURL + "/" + name,
		SHA256:   actual,
		Size:     info.Size(),
	}
	if opts.Output != "" {
		if err := ensureNotProductionCatalog(opts.Output); err != nil {
			return catalog.Entry{}, err
		}
		if err := writeEntry(opts.Output, entry); err != nil {
			return catalog.Entry{}, err
		}
	}
	return entry, nil
}

// baseURL validates the release base URL. A single trailing slash is accepted
// and removed so joining the artifact name cannot produce a double separator.
func validateBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("base url is required")
	}
	if strings.ContainsAny(raw, "\x00\r\n") {
		return "", errors.New("base url contains a control character")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid base url %q: %w", raw, err)
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("base url %q must use https", raw)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("base url %q has no host", raw)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("base url %q must not contain userinfo", raw)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return "", fmt.Errorf("base url %q must not contain a query", raw)
	}
	if parsed.Fragment != "" || strings.ContainsRune(raw, '#') {
		return "", fmt.Errorf("base url %q must not contain a fragment", raw)
	}
	if parsed.Opaque != "" {
		return "", fmt.Errorf("base url %q must be hierarchical", raw)
	}
	if strings.Contains(parsed.Path, "//") {
		return "", fmt.Errorf("base url %q must not contain a double path separator", raw)
	}
	trimmed := strings.TrimRight(raw, "/")
	if trimmed == "" {
		return "", errors.New("base url is empty after removing trailing slashes")
	}
	return trimmed, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open artifact %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash artifact %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ensureNotProductionCatalog rejects relative and absolute paths whose cleaned
// form ends in internal/catalog/catalog.json. The generator writes one entry,
// never a catalog document, and must not be able to overwrite the embedded one.
func ensureNotProductionCatalog(path string) error {
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == "internal/catalog/catalog.json" ||
		strings.HasSuffix(cleaned, "/internal/catalog/catalog.json") {
		return fmt.Errorf("refusing to write %s: output must not be internal/catalog/catalog.json", path)
	}
	return nil
}

// writeEntry writes the JSON object atomically: a temporary file in the target
// directory is fsynced and renamed over the destination.
func writeEntry(path string, entry catalog.Entry) error {
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode entry: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output directory %s: %w", dir, err)
	}
	file, err := os.CreateTemp(dir, ".phpcatalog-*")
	if err != nil {
		return fmt.Errorf("create output file in %s: %w", dir, err)
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write output file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync output file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close output file: %w", err)
	}
	if err := os.Chmod(temporary, 0o644); err != nil {
		return fmt.Errorf("chmod output file: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace output file %s: %w", path, err)
	}
	return nil
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("phpcatalog", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var opts Options
	flags.StringVar(&opts.Version, "version", "", "PHP version x.y.z")
	flags.IntVar(&opts.Revision, "revision", 0, "PHP build revision (at least 1)")
	flags.StringVar(&opts.Artifact, "artifact", "", "path to the built tarball")
	flags.StringVar(&opts.SHA256, "sha256", "", "expected lowercase SHA-256 of the tarball")
	flags.Int64Var(&opts.Size, "size", 0, "expected tarball size in bytes")
	flags.StringVar(&opts.BaseURL, "base-url", "", "HTTPS base URL where release assets are published")
	flags.StringVar(&opts.Output, "output", "", "write the entry JSON here instead of stdout")
	flags.Usage = func() {
		fmt.Fprintf(stderr, "usage: phpcatalog --version <x.y.z> --revision <n> --artifact <tarball> --sha256 <hex> --size <bytes> --base-url <https-url> [--output <file>]\n\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	entry, err := Generate(opts)
	if err != nil {
		return err
	}
	if opts.Output != "" {
		return nil
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode entry: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, string(data)); err != nil {
		return fmt.Errorf("print entry: %w", err)
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "phpcatalog:", err)
		os.Exit(1)
	}
}
