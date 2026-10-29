package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// serveHTTP applies request semantics before handing a request to the
// per-route reverse proxy.
func (r *Router) serveHTTP(w http.ResponseWriter, req *http.Request) {
	host := normalizeHost(req.Host)
	route, ok := r.Lookup(host)
	if !ok {
		// Even if not configured in the routing table, devstack.test must not be accessible externally.
		if host == "devstack.test" && !isLoopbackRequest(req) {
			writeBrandedPage(w, req, brandedPage{
				Status:  http.StatusForbidden,
				Title:   "Access forbidden",
				Message: "The devstack dashboard is only accessible from local loopback connections.",
			})
			return
		}
		display := host
		if display == "" {
			display = "an empty Host header"
		}
		writeBrandedPage(w, req, brandedPage{
			Status:  http.StatusNotFound,
			Title:   "Site not found",
			Message: fmt.Sprintf("No devstack site is configured for %s.", display),
			Command: "devstack site list",
		})
		return
	}

	// Security boundary: the dashboard (devstack.test) is strictly loopback-only.
	// Reject any non-loopback request attempting to reach devstack.test.
	if isDashboardRoute(route, host) && !isLoopbackRequest(req) {
		writeBrandedPage(w, req, brandedPage{
			Status:  http.StatusForbidden,
			Title:   "Access forbidden",
			Message: "The devstack dashboard is only accessible from local loopback connections.",
		})
		return
	}

	// A route with TLS disabled still has a valid certificate, but HTTPS must
	// never serve content for it: send the browser back to HTTP.
	if !route.TLS && req.TLS != nil {
		redirectPermanent(w, req, "http", redirectHost(route, host, "http"))
		return
	}
	if route.ForceHTTPS && route.TLS && req.TLS == nil {
		redirectPermanent(w, req, "https", redirectHost(route, host, "https"))
		return
	}

	switch {
	case route.DesiredState == "stopped" || route.State == "stopped":
		writeBrandedPage(w, req, brandedPage{
			Status:  http.StatusServiceUnavailable,
			Title:   "Site stopped",
			Message: fmt.Sprintf("%s is configured but not running.", host),
			Command: "devstack site start " + route.SiteID,
		})
		return
	case route.State == "starting":
		writeBrandedPage(w, req, brandedPage{
			Status:  http.StatusServiceUnavailable,
			Title:   "Site starting",
			Message: fmt.Sprintf("%s is starting; retry in a moment.", host),
		})
		return
	case route.State == "crashed":
		writeBrandedPage(w, req, brandedPage{
			Status:  http.StatusBadGateway,
			Title:   "Site crashed",
			Message: fmt.Sprintf("%s stopped unexpectedly.", host),
			Command: "devstack site restart " + route.SiteID,
		})
		return
	}

	if route.Kind == KindPHP {
		r.servePHP(w, req, route)
		return
	}

	proxy, err := r.reverseProxy(route)
	if err != nil {
		r.logProxyError(route, err)
		writeUpstreamFailure(w, req, route)
		return
	}
	proxy.ServeHTTP(w, req)
}

// reverseProxy builds a proxy for one route. Building it per request keeps the
// routing snapshot immutable; transports are shared and pooled.
func (r *Router) reverseProxy(route Route) (*httputil.ReverseProxy, error) {
	target, err := upstreamURL(route.Upstream)
	if err != nil {
		return nil, err
	}
	transport := r.staticUpstream
	if transport == nil {
		transport = r.staticTransport
	}
	switch route.Kind {
	case KindNode:
		transport = r.nodeUpstream
		if transport == nil {
			transport = r.nodeTransport
		}
	case KindStatic, "":
		// Static sites are HTTP services started by the local backend.
	case KindPHP:
		// PHP routes are served by the FastCGI transport, never as HTTP
		// upstreams.
		return nil, errors.New("proxy: PHP routes are served over FastCGI")
	default:
		return nil, fmt.Errorf("proxy: unknown route kind %q", route.Kind)
	}

	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Preserve the original Host: applications route and build URLs
			// from the site domain, not the loopback upstream address.
			pr.Out.Host = pr.In.Host
			// Never trust a client-supplied forwarding chain at the local edge.
			// The only client address we know is the one that connected to us.
			pr.Out.Header.Del("X-Forwarded-For")
			pr.SetXForwarded()
		},
		ModifyResponse: func(resp *http.Response) error {
			// HSTS is never emitted, even if a Node application sets it.
			resp.Header.Del("Strict-Transport-Security")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, proxyErr error) {
			r.handleProxyError(w, req, route, proxyErr)
		},
	}
	return proxy, nil
}

func (r *Router) handleProxyError(w http.ResponseWriter, req *http.Request, route Route, err error) {
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
}

func (r *Router) logProxyError(route Route, err error) {
	slog.Default().Error("proxy upstream request failed",
		"site", route.SiteID,
		"host", route.Host,
		"kind", string(route.Kind),
		"upstream", route.Upstream,
		"error", err,
	)
}

func writeUpstreamFailure(w http.ResponseWriter, req *http.Request, route Route) {
	name := route.Host
	if name == "" {
		name = route.SiteID
	}
	if name == "" {
		name = normalizeHost(req.Host)
	}
	command := "devstack site logs"
	if name != "" {
		command += " " + name
	}
	writeBrandedPage(w, req, brandedPage{
		Status:  http.StatusBadGateway,
		Title:   "Site unavailable",
		Message: fmt.Sprintf("The devstack site %s is not responding.", name),
		Command: command,
	})
}

func redirectHost(route Route, requestHost, scheme string) string {
	host := requestHost
	if route.Host != "" {
		host = normalizeHost(route.Host)
	}
	if host == "" {
		return host
	}
	if port := publicURLPort(scheme); port != "" {
		if trimmed, _, err := net.SplitHostPort(host); err == nil {
			host = trimmed
		}
		return net.JoinHostPort(host, port)
	}
	return host
}

// publicURLPort returns the non-default host port a browser must use for the
// given scheme. Deployments behind a reverse proxy can set these so redirects
// preserve the published ports instead of falling back to 80/443.
func publicURLPort(scheme string) string {
	key := "DEVSTACK_PUBLIC_HTTP_PORT"
	defaultPort := "80"
	if scheme == "https" {
		key = "DEVSTACK_PUBLIC_HTTPS_PORT"
		defaultPort = "443"
	}
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" || value == defaultPort {
		return ""
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 65535 {
		return ""
	}
	return strconv.Itoa(n)
}

func redirectPermanent(w http.ResponseWriter, req *http.Request, scheme, host string) {
	host = strings.ReplaceAll(host, "\r", "")
	host = strings.ReplaceAll(host, "\n", "")
	uri := ""
	if req.URL != nil {
		uri = req.URL.RequestURI()
	}
	uri = strings.ReplaceAll(uri, "\r", "")
	uri = strings.ReplaceAll(uri, "\n", "")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", scheme+"://"+host+uri)
	w.WriteHeader(http.StatusPermanentRedirect)
}

// upstreamURL validates a loopback-style host:port upstream and returns its
// base URL. Schemes and paths are rejected so a route can never redirect the
// proxy at an arbitrary URL.
func upstreamURL(upstream string) (*url.URL, error) {
	upstream = strings.TrimSpace(upstream)
	host, port, err := net.SplitHostPort(upstream)
	if err != nil {
		return nil, fmt.Errorf("proxy: invalid upstream %q: %w", upstream, err)
	}
	if host == "" || strings.ContainsAny(host, "/?#") {
		return nil, fmt.Errorf("proxy: invalid upstream host %q", host)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, fmt.Errorf("proxy: invalid upstream port %q", port)
	}
	return &url.URL{Scheme: "http", Host: upstream}, nil
}

// limitedBody enforces the configured request body ceiling. 0 disables the
// limit. Content-Length is checked up front so oversized uploads never reach an
// upstream; chunked bodies are still capped by http.MaxBytesReader.
func limitedBody(next http.Handler, maxBytes int64) http.Handler {
	if maxBytes <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBytes {
			writeBrandedPage(w, r, brandedPage{
				Status:  http.StatusRequestEntityTooLarge,
				Title:   "Request too large",
				Message: fmt.Sprintf("The request body exceeds the %d byte limit.", maxBytes),
			})
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

type brandedPage struct {
	Status  int
	Title   string
	Message string
	Command string
}

var brandedTemplate = template.Must(template.New("devstack-error").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · devstack</title>
<style>
:root { color-scheme: light dark; }
* { box-sizing: border-box; }
body {
  margin: 0; min-height: 100vh; display: grid; place-items: center;
  padding: 2rem;
  font: 16px/1.55 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
  background: #f6f3ff; color: #241b3a;
}
.card {
  width: min(34rem, 100%); padding: 2rem 2.25rem;
  background: #fff; border: 1px solid #e3d9ff; border-radius: 1rem;
  box-shadow: 0 18px 45px -25px rgba(59, 32, 122, .45);
}
.brand { font-weight: 700; letter-spacing: .02em; color: #6d3bd4; }
.status { margin: 1.5rem 0 0; font-size: .8rem; font-weight: 700; letter-spacing: .12em; text-transform: uppercase; color: #8a78aa; }
h1 { margin: .35rem 0 .75rem; font-size: clamp(1.5rem, 5vw, 2rem); line-height: 1.2; }
p { margin: .5rem 0 0; color: #4e4168; }
code { font: .9em ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; background: #f1ecff; color: #4a2599; padding: .15em .4em; border-radius: .35em; }
.hint { margin-top: 1.25rem; }
@media (prefers-color-scheme: dark) {
  body { background: #171223; color: #f1ecff; }
  .card { background: #211a33; border-color: #362a52; box-shadow: none; }
  .brand { color: #b69cff; }
  .status { color: #a996c9; }
  p { color: #cfc3e5; }
  code { background: #2d2345; color: #d6c6ff; }
}
</style>
</head>
<body>
<main class="card">
<div class="brand">devstack</div>
<p class="status">{{.Status}}</p>
<h1>{{.Title}}</h1>
<p>{{.Message}}</p>
{{if .Command}}<p class="hint">Run <code>{{.Command}}</code> for details.</p>{{end}}
</main>
</body>
</html>
`))

// writeBrandedPage renders a devstack error page. It never sets HSTS and never
// caches the response.
func writeBrandedPage(w http.ResponseWriter, req *http.Request, page brandedPage) {
	var body bytes.Buffer
	if err := brandedTemplate.Execute(&body, page); err != nil {
		http.Error(w, http.StatusText(page.Status), page.Status)
		return
	}
	header := w.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Length", strconv.Itoa(body.Len()))
	header.Del("Strict-Transport-Security")
	w.WriteHeader(page.Status)
	if req == nil || req.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}

func isDashboardRoute(route Route, host string) bool {
	return route.SiteID == "__dashboard__" || route.Host == "devstack.test" || host == "devstack.test"
}

// isLoopbackRequest reports whether req originated from a local loopback interface
// and contains no untrusted external forwarding headers.
func isLoopbackRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !isLoopbackAddress(r.RemoteAddr) {
		return false
	}
	for _, xff := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(xff, ",") {
			part = strings.TrimSpace(part)
			if part != "" && !isLoopbackAddress(part) {
				return false
			}
		}
	}
	for _, xri := range r.Header.Values("X-Real-IP") {
		xri = strings.TrimSpace(xri)
		if xri != "" && !isLoopbackAddress(xri) {
			return false
		}
	}
	for _, fwd := range r.Header.Values("Forwarded") {
		for _, part := range strings.Split(fwd, ";") {
			part = strings.TrimSpace(part)
			lower := strings.ToLower(part)
			if strings.HasPrefix(lower, "for=") {
				target := strings.Trim(strings.TrimPrefix(part[4:], "="), `"' `)
				if target != "" && !isLoopbackAddress(target) {
					return false
				}
			}
		}
	}
	return true
}

// isLoopbackAddress reports whether addr (host:port or bare IP) is an IPv4 or IPv6 loopback address.
func isLoopbackAddress(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	host := addr
	if parsedHost, _, err := net.SplitHostPort(addr); err == nil {
		host = parsedHost
	}
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if idx := strings.IndexByte(host, '%'); idx != -1 {
		host = host[:idx]
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
