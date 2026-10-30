package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/nodereleases"
)

// nodeReleaseService is the narrow Node release discovery surface used by the
// handlers below. The daemon runtime services implement it; older runtime
// services that predate discovery simply report service unavailable.
type nodeReleaseService interface {
	NodeReleases(ctx context.Context, major string, all, offline bool) (nodereleases.Snapshot, error)
	NodeRelease(ctx context.Context, version string) (nodereleases.Preflight, error)
	RefreshNodeReleases(ctx context.Context) (nodereleases.Snapshot, error)
}

// nodeReleaseHandlerService returns the discovery surface when the configured
// runtime service supports it.
func (s *Server) nodeReleaseHandlerService() (nodeReleaseService, bool) {
	if s == nil || s.runtimes == nil {
		return nil, false
	}
	service, ok := s.runtimes.(nodeReleaseService)
	return service, ok
}

// handleListNodeReleases returns the discovered Node releases. By default the
// snapshot contains only the newest patch of each major; all expands every
// stable release and major filters to one release line. offline forbids network
// requests, so the response explicitly reports cache or embedded provenance.
func (s *Server) handleListNodeReleases(w http.ResponseWriter, r *http.Request) {
	service, ok := s.nodeReleaseHandlerService()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable,
			"node release discovery is not available", nil)
		return
	}

	major, err := parseNodeReleaseMajor(r.URL.Query().Get("major"))
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, err.Error(), nil)
		return
	}
	all, err := parseNodeReleaseFlag(r.URL.Query(), "all")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, err.Error(), nil)
		return
	}
	offline, err := parseNodeReleaseFlag(r.URL.Query(), "offline")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, err.Error(), nil)
		return
	}

	snapshot, err := service.NodeReleases(r.Context(), major, all, offline)
	if err != nil {
		s.writeNodeReleaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sanitizeNodeReleaseSnapshot(snapshot))
}

// handleGetNodeRelease returns fully pinned install metadata for one release.
// The checksum, size, and official URL are only available after this explicit
// preflight request.
func (s *Server) handleGetNodeRelease(w http.ResponseWriter, r *http.Request) {
	service, ok := s.nodeReleaseHandlerService()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable,
			"node release discovery is not available", nil)
		return
	}

	version := strings.TrimSpace(r.PathValue("version"))
	if err := validateNodeReleaseSelector(version); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, err.Error(), nil)
		return
	}
	preflight, err := service.NodeRelease(r.Context(), version)
	if err != nil {
		s.writeNodeReleaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, preflight)
}

// handleRefreshNodeReleases forces one index fetch and returns the refreshed
// default snapshot with cache age and any warning. It never prefetches
// checksums or archive sizes.
func (s *Server) handleRefreshNodeReleases(w http.ResponseWriter, r *http.Request) {
	service, ok := s.nodeReleaseHandlerService()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable,
			"node release discovery is not available", nil)
		return
	}

	snapshot, err := service.RefreshNodeReleases(r.Context())
	if err != nil {
		s.writeNodeReleaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sanitizeNodeReleaseSnapshot(snapshot))
}

// parseNodeReleaseMajor validates and normalizes the optional major query. An
// empty value keeps every major; malformed and non-positive values are rejected
// instead of being treated as "all".
func parseNodeReleaseMajor(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	major, err := strconv.Atoi(raw)
	if err != nil || major < 1 {
		return "", errors.New("node major must be a positive integer")
	}
	return strconv.Itoa(major), nil
}

// parseNodeReleaseFlag parses a boolean query flag. A bare flag (?offline) means
// enabled; an explicit true/false value is honored.
func parseNodeReleaseFlag(values url.Values, name string) (bool, error) {
	if !values.Has(name) {
		return false, nil
	}
	raw := strings.TrimSpace(values.Get(name))
	if raw == "" {
		return true, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("node %s flag must be true or false", name)
	}
	return value, nil
}

// validateNodeReleaseSelector rejects path traversal and control characters
// before the selector reaches discovery. Resolution and eligibility are
// enforced by the release service.
func validateNodeReleaseSelector(version string) error {
	if version == "" || version == "." || version == ".." {
		return errors.New("node version is required")
	}
	if strings.ContainsAny(version, "/\\\x00") {
		return errors.New("invalid node version")
	}
	return nil
}

// writeNodeReleaseError maps discovery failures onto the stable API envelope.
// Unknown releases are a typed not-found; every upstream failure is a sanitized
// bad gateway that never exposes cache paths or fetch URLs.
func (s *Server) writeNodeReleaseError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, nodereleases.ErrReleaseNotFound) {
		writeError(w, http.StatusNotFound, codeNotFound, "node release is not available", nil)
		return
	}
	if s.logger != nil {
		s.logger.Error("node release request failed",
			"method", r.Method,
			"path", r.URL.Path,
			"error", sanitizeNodeReleaseMessage(err.Error()),
		)
	}
	writeError(w, http.StatusBadGateway, codeServiceUnavailable,
		"node release discovery is unavailable", nil)
}

var (
	// nodeReleaseCachePath matches private cache locations that discovery can
	// mention in warnings, including Windows paths with spaces and POSIX forms.
	nodeReleaseCachePath = regexp.MustCompile(
		`(?i)[A-Za-z]:\\[^:*?"<>|\r\n]*\\cache\\node(?:\\[^\\/:*?"<>|\r\n]*)*` +
			`|(?:[A-Za-z]:)?(?:[\\/][^\s"'<>]+)*[\\/]cache[\\/]node(?:[\\/][^\s"'<>]+)*`,
	)
	// nodeReleaseURL matches official and fallback fetch URLs.
	nodeReleaseURL = regexp.MustCompile(`(?i)https?://\S+`)
	// nodeReleaseTransportDetail matches the transport diagnostics appended by
	// net/http and the operating system. The rest of the message is redacted so
	// host addresses, port numbers, and OS error text cannot leak.
	nodeReleaseTransportDetail = regexp.MustCompile(
		`(?i)\b(?:dial\s+(?:tcp|udp)\d*|connectex|no such host|i/o timeout` +
			`|connection (?:refused|reset|attempt failed)|network is unreachable` +
			`|tls handshake (?:failure|timeout)|context deadline exceeded` +
			`|server misbehaving|unexpected EOF)\b.*$`,
	)
)

// sanitizeNodeReleaseMessage keeps discovery messages actionable while hiding
// private cache locations, fetch URLs, and transport diagnostics. Discovery
// errors carry a structured "nodereleases:" detail tail; only the human prefix
// before it is preserved. The same sanitizer is used for responses and logs.
func sanitizeNodeReleaseMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	if index := strings.Index(strings.ToLower(message), "nodereleases:"); index >= 0 {
		prefix := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(message[:index]), ":"))
		if prefix != "" {
			message = prefix
		} else {
			message = "node release discovery failed"
		}
	}
	message = nodeReleaseCachePath.ReplaceAllString(message, "<cache>")
	message = nodeReleaseURL.ReplaceAllString(message, "<url>")
	message = nodeReleaseTransportDetail.ReplaceAllString(message, "transport error")
	return strings.TrimSpace(message)
}

// sanitizeNodeReleaseSnapshot applies the same redaction to user-visible
// warnings and keeps the collection fields as arrays rather than null before
// the snapshot leaves the daemon.
func sanitizeNodeReleaseSnapshot(snapshot nodereleases.Snapshot) nodereleases.Snapshot {
	snapshot.Warning = sanitizeNodeReleaseMessage(snapshot.Warning)
	if snapshot.Majors == nil {
		snapshot.Majors = []nodereleases.Major{}
	}
	if snapshot.All == nil {
		snapshot.All = []nodereleases.Release{}
	}
	return snapshot
}
