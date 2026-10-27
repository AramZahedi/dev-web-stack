package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/fcgi"
)

// phpRequestTimeout bounds one FastCGI exchange, including a slow front
// controller and a streamed response.
const phpRequestTimeout = 120 * time.Second

// phpBackupSuffixes block editor, swap, and temporary files that would
// otherwise disclose PHP source when served directly. The native nginx and
// Apache templates deny the same set; keep the lists in parity.
var phpBackupSuffixes = []string{
	".php~", ".php.bak", ".php.orig", ".php.save", ".php.old", ".php.swp", ".php.swo", ".php.tmp",
}

// phpPathInfoPattern splits /app.php/extra/path into a script and the suffix
// that FPM must receive as PATH_INFO. Matching is case-insensitive so .PHP
// folds to .php.
var phpPathInfoPattern = regexp.MustCompile(`(?i)^(.+\.php)(/.*)?$`)

// hopByHopHeaders are connection-scoped headers that must never be forwarded
// in either direction.
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// phpLookup is the tri-state result of resolving a request path. An escape (a
// symlink leaving the document root) is denied and must never fall through to
// the front controller.
type phpLookup int

const (
	phpNotFound phpLookup = iota
	phpFound
	phpDenied
)

// phpTarget is one resolved PHP request: either a script to execute or a file
// to serve directly.
type phpTarget struct {
	documentRoot string
	scriptPath   string
	scriptName   string
	pathInfo     string
	staticPath   string
	staticName   string
}

// servePHP resolves a request against a PHP route's document root and serves
// either a static file or a FastCGI response.
func (r *Router) servePHP(w http.ResponseWriter, req *http.Request, route Route) {
	requestPath := ""
	if req.URL != nil {
		requestPath = req.URL.Path
	}
	target, ok := resolvePHPTarget(route, requestPath)
	if !ok {
		writePHPNotFound(w, req, route)
		return
	}
	if target.staticPath != "" {
		if target.staticName == "index.html" && !strings.HasSuffix(requestPath, "/") &&
			path.Base(requestPath) != "index.html" {
			locationPath := "/" + strings.TrimPrefix(path.Clean(requestPath), "/") + "/"
			location := (&url.URL{Path: locationPath}).EscapedPath()
			if req.URL != nil && req.URL.RawQuery != "" {
				location += "?" + req.URL.RawQuery
			}
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		r.servePHPStatic(w, req, route, target)
		return
	}
	r.serveFastCGI(w, req, route, target)
}

// servePHPStatic serves a resolved file with conditional-request and range
// support. Directories are never served as files and never listed.
func (r *Router) servePHPStatic(w http.ResponseWriter, req *http.Request, route Route, target phpTarget) {
	root, err := os.OpenRoot(target.documentRoot)
	if err != nil {
		writePHPNotFound(w, req, route)
		return
	}
	defer func() { _ = root.Close() }()
	rel, err := filepath.Rel(target.documentRoot, target.staticPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		writePHPNotFound(w, req, route)
		return
	}
	file, err := root.Open(rel)
	if err != nil {
		writePHPNotFound(w, req, route)
		return
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writePHPNotFound(w, req, route)
		return
	}

	header := w.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, req, target.staticName, info.ModTime(), file)
}

// serveFastCGI sends one request to the route's FPM pool and streams the
// response. Errors before headers become the branded 502 page; errors after
// headers are logged because the response can no longer be replaced.
func (r *Router) serveFastCGI(w http.ResponseWriter, req *http.Request, route Route, target phpTarget) {
	ctx, cancel := context.WithTimeout(req.Context(), phpRequestTimeout)
	defer cancel()

	client, err := r.dialFastCGI(ctx, route.Upstream)
	if err != nil {
		r.logProxyError(route, err)
		writeUpstreamFailure(w, req, route)
		return
	}
	defer func() { _ = client.Close() }()

	bodyLength := req.ContentLength
	if bodyLength < 0 {
		bodyLength = -1
	}
	resp, err := client.Do(ctx, fcgi.Request{
		Params:     phpCGIParams(req, route, target),
		Body:       req.Body,
		BodyLength: bodyLength,
	})
	if err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			writeBrandedPage(w, req, brandedPage{
				Status:  http.StatusRequestEntityTooLarge,
				Title:   "Request too large",
				Message: fmt.Sprintf("The request body exceeds the %d byte limit.", maxBytes.Limit),
			})
			return
		}
		r.logProxyError(route, err)
		writeUpstreamFailure(w, req, route)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	writeFastCGIResponse(w, req, route, resp)
}

func (r *Router) dialFastCGI(ctx context.Context, addr string) (fcgiClient, error) {
	if r.fcgiDial == nil {
		return nil, errors.New("proxy: FastCGI dialer is not configured")
	}
	return r.fcgiDial(ctx, addr)
}

// writeFastCGIResponse copies the CGI status and headers, then streams the
// body. HSTS is never emitted and HEAD responses carry headers only.
type flushWriter struct {
	w          io.Writer
	controller *http.ResponseController
}

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil {
		_ = f.controller.Flush()
	}
	return n, err
}

func writeFastCGIResponse(w http.ResponseWriter, req *http.Request, route Route, resp *fcgi.Response) {
	drop := hopByHopHeaderSet(resp.Header)
	header := w.Header()
	for name, values := range resp.Header {
		canonical := http.CanonicalHeaderKey(name)
		if canonical == "" || drop[canonical] {
			continue
		}
		if strings.EqualFold(canonical, "Status") || strings.EqualFold(canonical, "Strict-Transport-Security") {
			continue
		}
		for _, value := range values {
			header.Add(canonical, value)
		}
	}

	w.WriteHeader(resp.StatusCode)
	if req.Method == http.MethodHead {
		return
	}
	controller := http.NewResponseController(w)
	if _, err := io.Copy(flushWriter{w: w, controller: controller}, resp.Body); err != nil {
		slog.Default().Error("proxy PHP response body failed",
			"site", route.SiteID,
			"host", route.Host,
			"error", err,
		)
	}
}

// phpCGIParams builds the CGI/1.1 environment for one request. Client-supplied
// forwarding headers are ignored: HTTP_X_FORWARDED_* is synthesized from the
// actual connection, TLS state, and Host.
func phpCGIParams(req *http.Request, route Route, target phpTarget) map[string]string {
	serverName, serverPort := phpServerNamePort(req)
	params := map[string]string{
		"GATEWAY_INTERFACE":      "CGI/1.1",
		"SERVER_PROTOCOL":        req.Proto,
		"SERVER_SOFTWARE":        "devstack",
		"REQUEST_METHOD":         req.Method,
		"SCRIPT_FILENAME":        fastCGIScriptPath(route, target),
		"SCRIPT_NAME":            target.scriptName,
		"PATH_INFO":              target.pathInfo,
		"DOCUMENT_ROOT":          fastCGIRoot(route, target),
		"REQUEST_URI":            phpRequestURI(req),
		"QUERY_STRING":           phpQueryString(req),
		"SERVER_NAME":            serverName,
		"SERVER_PORT":            serverPort,
		"HTTP_HOST":              req.Host,
		"HTTP_X_FORWARDED_PROTO": phpScheme(req),
		"HTTP_X_FORWARDED_HOST":  req.Host,
	}

	if req.TLS != nil {
		params["HTTPS"] = "on"
	}
	if remoteHost, remotePort, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		params["REMOTE_ADDR"] = remoteHost
		params["REMOTE_PORT"] = remotePort
		params["HTTP_X_FORWARDED_FOR"] = remoteHost
	}
	if value := req.Header.Get("Content-Type"); value != "" {
		params["CONTENT_TYPE"] = value
	}
	if req.ContentLength >= 0 {
		params["CONTENT_LENGTH"] = strconv.FormatInt(req.ContentLength, 10)
	}

	hopByHop := hopByHopHeaderSet(req.Header)
	for name, values := range req.Header {
		canonical := http.CanonicalHeaderKey(name)
		if canonical == "" || hopByHop[canonical] {
			continue
		}
		switch canonical {
		case "Content-Type", "Content-Length", "Host":
			continue
		}
		if strings.HasPrefix(strings.ToLower(canonical), "x-forwarded-") {
			continue
		}
		key := "HTTP_" + strings.ReplaceAll(strings.ToUpper(canonical), "-", "_")
		params[key] = strings.Join(values, ", ")
	}
	return params
}

func phpRequestURI(req *http.Request) string {
	if req.URL == nil {
		return ""
	}
	return req.URL.RequestURI()
}

func phpQueryString(req *http.Request) string {
	if req.URL == nil {
		return ""
	}
	return req.URL.RawQuery
}

func phpScheme(req *http.Request) string {
	if req.TLS != nil {
		return "https"
	}
	return "http"
}

// phpServerNamePort splits Host into the CGI server name and port. An explicit
// Host port wins; otherwise the scheme default (443 for TLS, 80 for HTTP)
// applies.
func phpServerNamePort(req *http.Request) (string, string) {
	defaultPort := "80"
	if req.TLS != nil {
		defaultPort = "443"
	}
	host := strings.TrimSpace(req.Host)
	if name, port, err := net.SplitHostPort(host); err == nil {
		if port == "" {
			port = defaultPort
		}
		return normalizeHost(name), port
	}
	name := host
	if strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]") {
		name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
	}
	return normalizeHost(name), defaultPort
}

// hopByHopHeaderSet returns the hop-by-hop header names in header, including
// the names listed by its Connection header.
func hopByHopHeaderSet(header http.Header) map[string]bool {
	set := make(map[string]bool, len(hopByHopHeaders)+2)
	for _, name := range hopByHopHeaders {
		set[http.CanonicalHeaderKey(name)] = true
	}
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				set[http.CanonicalHeaderKey(name)] = true
			}
		}
	}
	return set
}

// resolvePHPTarget maps a decoded URL path onto a script or a static file.
// Traversal, dotfiles, backup names, and symlink escapes are hard denials.
func resolvePHPTarget(route Route, requestPath string) (phpTarget, bool) {
	root := strings.TrimSpace(route.ProxyDocumentRoot)
	if root == "" {
		root = strings.TrimSpace(route.DocumentRoot)
	}
	if root == "" {
		return phpTarget{}, false
	}
	if requestPath == "" {
		requestPath = "/"
	}
	if strings.ContainsRune(requestPath, 0) {
		return phpTarget{}, false
	}
	for _, segment := range strings.Split(requestPath, "/") {
		if segment == "" {
			continue
		}
		if strings.HasPrefix(segment, ".") {
			return phpTarget{}, false
		}
		lowered := strings.ToLower(segment)
		for _, suffix := range phpBackupSuffixes {
			if strings.HasSuffix(lowered, suffix) {
				return phpTarget{}, false
			}
		}
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return phpTarget{}, false
	}
	rootInfo, err := os.Stat(resolvedRoot)
	if err != nil || !rootInfo.IsDir() {
		return phpTarget{}, false
	}

	cleaned := path.Clean("/" + requestPath)
	rel := strings.TrimPrefix(cleaned, "/")
	if rel == "" {
		rel = "."
	}

	// A script followed by a URL suffix keeps that suffix as PATH_INFO, even
	// when the suffix is only a trailing slash. The script is a file, so it
	// cannot also contain the requested suffix as a real path; trying it
	// before the complete path therefore does not change file precedence.
	target, status := resolvePHPPathInfo(resolvedRoot, requestPath)
	if status == phpFound {
		return target, true
	}
	if status == phpDenied {
		return phpTarget{}, false
	}

	target, status = resolvePHPCandidate(resolvedRoot, rel, cleaned)
	if status == phpFound {
		return target, true
	}
	if status == phpDenied {
		return phpTarget{}, false
	}

	if route.FrontController {
		target, status = resolvePHPFrontController(resolvedRoot, cleaned)
		if status == phpFound {
			return target, true
		}
	}
	return phpTarget{}, false
}

// fastCGIRoot is the backend document root FPM must see. When the proxy uses
// a separate Windows-visible root for file checks, DocumentRoot is the backend
// path.
func fastCGIRoot(route Route, target phpTarget) string {
	if root := strings.TrimSpace(route.DocumentRoot); root != "" {
		if strings.HasPrefix(root, "/") {
			return strings.ReplaceAll(root, `\`, "/")
		}
		return root
	}
	return target.documentRoot
}

// fastCGIScriptPath maps a script resolved under the proxy root back into the
// backend namespace so FPM can open it.
func fastCGIScriptPath(route Route, target phpTarget) string {
	proxyRoot := strings.TrimSpace(route.ProxyDocumentRoot)
	backendRoot := strings.TrimSpace(route.DocumentRoot)
	if proxyRoot == "" || backendRoot == "" || proxyRoot == backendRoot {
		return target.scriptPath
	}
	rel, err := filepath.Rel(proxyRoot, target.scriptPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return target.scriptPath
	}
	if strings.HasPrefix(backendRoot, "/") {
		return path.Join(backendRoot, strings.ReplaceAll(rel, `\`, "/"))
	}
	return filepath.Join(backendRoot, rel)
}

// resolvePHPCandidate tries the complete requested path, preferring index.php
// and then index.html for a directory.
func resolvePHPCandidate(root, rel, cleaned string) (phpTarget, phpLookup) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	resolved, info, status := evalPHPPath(root, full)
	if status != phpFound {
		return phpTarget{}, status
	}
	if info.IsDir() {
		return resolvePHPIndex(root, rel, cleaned)
	}
	if !info.Mode().IsRegular() {
		return phpTarget{}, phpNotFound
	}
	if isPHPScriptName(path.Base(cleaned)) {
		return phpTarget{
			documentRoot: root,
			scriptPath:   resolved,
			scriptName:   cleaned,
		}, phpFound
	}
	return phpTarget{
		documentRoot: root,
		staticPath:   resolved,
		staticName:   path.Base(cleaned),
	}, phpFound
}

// resolvePHPIndex applies the directory preference order. A directory without
// an index is a 404, never a listing.
func resolvePHPIndex(root, rel, cleaned string) (phpTarget, phpLookup) {
	scriptRel := path.Join(rel, "index.php")
	resolved, info, status := evalPHPPath(root, filepath.Join(root, filepath.FromSlash(scriptRel)))
	switch status {
	case phpDenied:
		return phpTarget{}, phpDenied
	case phpFound:
		if info.Mode().IsRegular() {
			return phpTarget{
				documentRoot: root,
				scriptPath:   resolved,
				scriptName:   path.Join(cleaned, "index.php"),
			}, phpFound
		}
	}

	htmlRel := path.Join(rel, "index.html")
	resolved, info, status = evalPHPPath(root, filepath.Join(root, filepath.FromSlash(htmlRel)))
	if status == phpDenied {
		return phpTarget{}, phpDenied
	}
	if status == phpFound && info.Mode().IsRegular() {
		return phpTarget{
			documentRoot: root,
			staticPath:   resolved,
			staticName:   "index.html",
		}, phpFound
	}
	return phpTarget{}, phpNotFound
}

// resolvePHPPathInfo executes an existing script and hands the URL suffix to
// FPM as PATH_INFO.
func resolvePHPPathInfo(root, cleaned string) (phpTarget, phpLookup) {
	match := phpPathInfoPattern.FindStringSubmatch(cleaned)
	if match == nil {
		return phpTarget{}, phpNotFound
	}
	scriptName, pathInfo := match[1], match[2]
	rel := strings.TrimPrefix(scriptName, "/")
	resolved, info, status := evalPHPPath(root, filepath.Join(root, filepath.FromSlash(rel)))
	if status != phpFound {
		return phpTarget{}, status
	}
	if !info.Mode().IsRegular() {
		return phpTarget{}, phpNotFound
	}
	return phpTarget{
		documentRoot: root,
		scriptPath:   resolved,
		scriptName:   scriptName,
		pathInfo:     pathInfo,
	}, phpFound
}

// resolvePHPFrontController runs <docroot>/index.php with the cleaned request
// path as PATH_INFO.
func resolvePHPFrontController(root, cleaned string) (phpTarget, phpLookup) {
	resolved, info, status := evalPHPPath(root, filepath.Join(root, "index.php"))
	if status != phpFound {
		return phpTarget{}, status
	}
	if !info.Mode().IsRegular() {
		return phpTarget{}, phpNotFound
	}
	return phpTarget{
		documentRoot: root,
		scriptPath:   resolved,
		scriptName:   "/index.php",
		pathInfo:     cleaned,
	}, phpFound
}

// evalPHPPath resolves symlinks and requires the result to stay inside root.
func evalPHPPath(root, candidate string) (string, os.FileInfo, phpLookup) {
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", nil, phpNotFound
	}
	if !phpPathWithin(root, resolved) {
		return "", nil, phpDenied
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, phpNotFound
	}
	return resolved, info, phpFound
}

func phpPathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isPHPScriptName reports whether name folds to a .php extension.
func isPHPScriptName(name string) bool {
	return strings.EqualFold(path.Ext(name), ".php")
}

// writePHPNotFound renders the branded 404 page used by PHP routes.
func writePHPNotFound(w http.ResponseWriter, req *http.Request, route Route) {
	name := route.Host
	if name == "" {
		name = route.SiteID
	}
	requestPath := "/"
	if req != nil && req.URL != nil && req.URL.Path != "" {
		requestPath = req.URL.Path
	}

	message := "The requested page could not be found."
	command := ""
	if name != "" {
		message = fmt.Sprintf("The devstack site %s has no file for %s.", name, requestPath)
		command = "devstack site logs " + name
	}
	writeBrandedPage(w, req, brandedPage{
		Status:  http.StatusNotFound,
		Title:   "Page not found",
		Message: message,
		Command: command,
	})
}
