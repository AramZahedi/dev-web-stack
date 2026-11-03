package performance

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Built-in load generator identities. The daemon ships both engines so a run
// can trade HTTP/2 compatibility for fasthttp throughput without an external
// binary.
const (
	EngineFastHTTP = "fasthttp"
	EngineNetHTTP  = "nethttp"

	FastHTTPToolName = "devstack-fasthttp-load"
	NetHTTPToolName  = "devstack-go-http-load"

	LoadToolName    = FastHTTPToolName
	LoadToolVersion = "1"
)

const (
	loadSampleLimit    = 200_000
	loadWorkerCap      = 2048
	loadRequestTimeout = 10 * time.Second
	maxLoadDuration    = time.Hour
	// MaxBenchmarkBodyBytes bounds a custom request body so a benchmark cannot
	// turn into an accidental file-upload test.
	MaxBenchmarkBodyBytes = 1 << 20
	maxBenchmarkResponse  = 1 << 20
)

// FastHTTPLimitations is reported for the default fasthttp engine.
var FastHTTPLimitations = []string{
	"Built-in Go load generator using fasthttp; not wrk, hey, bombardier, or another pinned external tool.",
	"Requests use the configured method, path, headers, and optional body.",
	"fasthttp uses HTTP/1.1; HTTP/2-only targets are not supported by this engine.",
	"Requests are sequential per worker; the requested connection count is used as the HTTP transport connection limit.",
	"Worker goroutines are capped at 2048 for host safety.",
	"Latency percentiles use a bounded reservoir sample of up to 200000 completed requests.",
	"RPS counts completed HTTP exchanges (including non-2xx responses and transport failures); errors are reported separately.",
	"Redirects are not followed; 3xx, 4xx, and 5xx responses count as errors.",
	"Response bodies are read up to 1 MiB; larger responses count as errors.",
	"TLS uses InsecureSkipVerify only for loopback benchmark targets.",
	"Header values and request bodies are sent but never stored; results keep only the method, path, header names, and body size.",
}

// NetHTTPLimitations is reported for the net/http engine.
var NetHTTPLimitations = []string{
	"Built-in Go net/http load generator; not wrk, hey, bombardier, or another pinned external tool.",
	"Requests use the configured method, path, headers, and optional body.",
	"net/http supports HTTP/2 when the target negotiates it.",
	"Requests are sequential per worker; the requested connection count is used as the HTTP transport connection limit.",
	"Worker goroutines are capped at 2048 for host safety.",
	"Latency percentiles use a bounded reservoir sample of up to 200000 completed requests.",
	"RPS counts completed HTTP exchanges (including non-2xx responses and transport failures); errors are reported separately.",
	"Redirects are not followed; 3xx, 4xx, and 5xx responses count as errors.",
	"Response bodies are read up to 4 KiB and then closed to bound memory and output.",
	"TLS uses InsecureSkipVerify only for loopback benchmark targets.",
	"Header values and request bodies are sent but never stored; results keep only the method, path, header names, and body size.",
}

// LoadLimitations aliases the default engine limitations for compatibility.
var LoadLimitations = FastHTTPLimitations

// NormalizeBenchmarkEngine returns the engine selected for a run. An empty
// value selects the faster fasthttp engine.
func NormalizeBenchmarkEngine(engine string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "", EngineFastHTTP, "fast":
		return EngineFastHTTP, nil
	case EngineNetHTTP, "net/http", "go", "net-http":
		return EngineNetHTTP, nil
	default:
		return "", invalid("benchmark engine", "%q must be fasthttp or nethttp", engine)
	}
}

// BenchmarkToolName reports the tool identity stored with a run.
func BenchmarkToolName(engine string) string {
	if engine == EngineNetHTTP {
		return NetHTTPToolName
	}
	return FastHTTPToolName
}

// BenchmarkLimitations reports the engine-specific limitations stored with a run.
func BenchmarkLimitations(engine string) []string {
	if engine == EngineNetHTTP {
		return NetHTTPLimitations
	}
	return FastHTTPLimitations
}

// LoadOptions configures one measured run.
type LoadOptions struct {
	Engine      string
	URL         string
	HostHeader  string
	Method      string
	Headers     map[string]string
	Body        []byte
	Duration    time.Duration
	Connections int
	InsecureTLS bool
}

// LoadResult is a measured load-generator result. Zero values mean no result
// was produced; RPS is never estimated.
type LoadResult struct {
	Tool        string        `json:"tool"`
	Version     string        `json:"version"`
	Engine      string        `json:"engine"`
	Limitations []string      `json:"limitations"`
	URL         string        `json:"url"`
	Method      string        `json:"method"`
	BodyBytes   int           `json:"body_bytes"`
	Requests    int64         `json:"requests"`
	Errors      int64         `json:"errors"`
	RPS         float64       `json:"requests_per_second"`
	P50MS       float64       `json:"p50_ms"`
	P95MS       float64       `json:"p95_ms"`
	P99MS       float64       `json:"p99_ms"`
	Duration    time.Duration `json:"duration"`
	Connections int           `json:"connections"`
	Workers     int           `json:"workers"`
	SampleSize  int           `json:"sample_size"`
	SampleLimit int           `json:"sample_limit"`
	StartedAt   time.Time     `json:"started_at"`
	FinishedAt  time.Time     `json:"finished_at"`
}

// RunLoad dispatches a real HTTP load run to the selected engine. It never
// invents results: on cancellation or transport failure the returned result has
// no requests-per-second or latency claim and the error is non-nil.
func RunLoad(ctx context.Context, opts LoadOptions) (LoadResult, []byte, error) {
	engine, err := NormalizeBenchmarkEngine(opts.Engine)
	if err != nil {
		return LoadResult{}, nil, err
	}
	opts.Engine = engine
	switch engine {
	case EngineNetHTTP:
		return runLoadNetHTTP(ctx, opts)
	case EngineFastHTTP:
		return runLoadFastHTTP(ctx, opts)
	default:
		return LoadResult{}, nil, invalid("benchmark engine", "%q is not supported", engine)
	}
}

// runLoadNetHTTP performs a measured run with Go's net/http client.
func runLoadNetHTTP(ctx context.Context, opts LoadOptions) (LoadResult, []byte, error) {
	if err := validateLoadOptions(opts); err != nil {
		return LoadResult{}, nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	method, err := NormalizeBenchmarkMethod(opts.Method)
	if err != nil {
		return LoadResult{}, nil, err
	}
	headers, err := NormalizeBenchmarkHeaders(opts.Headers)
	if err != nil {
		return LoadResult{}, nil, err
	}
	workers := opts.Connections
	if workers > loadWorkerCap {
		workers = loadWorkerCap
	}
	result := LoadResult{
		Tool:        NetHTTPToolName,
		Version:     LoadToolVersion,
		Engine:      EngineNetHTTP,
		Limitations: append([]string(nil), NetHTTPLimitations...),
		URL:         opts.URL,
		Method:      method,
		BodyBytes:   len(opts.Body),
		Connections: opts.Connections,
		Workers:     workers,
		SampleLimit: loadSampleLimit,
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err == nil && isTestHost(host) {
				// ".test" is reserved for local development, and the daemon
				// only accepts loopback benchmark targets. Never let a hosts-
				// file or DNS entry redirect this run to a remote address.
				return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
			}
			return dialer.DialContext(ctx, network, addr)
		},
		MaxIdleConns:          opts.Connections,
		MaxIdleConnsPerHost:   opts.Connections,
		MaxConnsPerHost:       opts.Connections,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: loadRequestTimeout,
		DisableCompression:    true,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: opts.InsecureTLS}, //nolint:gosec // loopback benchmark only
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	requestHeaders := make(http.Header, len(headers))
	for name, value := range headers {
		requestHeaders.Set(name, value)
	}

	var (
		mu       sync.Mutex
		requests int64
		errors   int64
		samples         = make([]float64, 0, loadSampleLimit)
		seed     uint64 = 1
	)
	record := func(latencyMS float64, failed bool) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if failed {
			errors++
		}
		if len(samples) < loadSampleLimit {
			samples = append(samples, latencyMS)
			return
		}
		// Deterministic reservoir replacement without a global RNG.
		seed = seed*6364136223846793005 + 1442695040888963407
		index := int(seed % uint64(loadSampleLimit))
		samples[index] = latencyMS
	}

	loopCtx, cancel := context.WithTimeout(ctx, opts.Duration)
	defer cancel()
	runDeadline := time.Now().Add(opts.Duration)
	start := time.Now().UTC()
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if loopCtx.Err() != nil || !time.Now().Before(runDeadline) {
					return
				}
				reqCtx, reqCancel := context.WithTimeout(loopCtx, loadRequestTimeout)
				var body io.Reader
				if len(opts.Body) > 0 {
					body = bytes.NewReader(opts.Body)
				}
				req, err := http.NewRequestWithContext(reqCtx, method, opts.URL, body)
				if err != nil {
					reqCancel()
					record(0, true)
					return
				}
				req.Header = requestHeaders.Clone()
				if opts.HostHeader != "" {
					req.Host = opts.HostHeader
				}
				requested := time.Now()
				resp, err := client.Do(req)
				latency := float64(time.Since(requested).Microseconds()) / 1000
				failed := err != nil
				if err == nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
					_ = resp.Body.Close()
					if resp.StatusCode >= http.StatusMultipleChoices {
						failed = true
					}
				}
				reqCancel()
				if loopCtx.Err() != nil {
					// Requests cut off by the run boundary are not failures.
					if err == nil {
						record(latency, failed)
					}
					return
				}
				record(latency, failed)
			}
		}()
	}
	wg.Wait()
	finished := time.Now().UTC()
	mu.Lock()
	finalRequests, finalErrors := requests, errors
	finalSamples := append([]float64(nil), samples...)
	mu.Unlock()
	return finishLoadResult(ctx, result, finalRequests, finalErrors, finalSamples, start, finished, loopCtx.Err())
}

// finishLoadResult computes the measured metrics and never synthesizes RPS for
// a canceled or failed run.
func finishLoadResult(ctx context.Context, result LoadResult, requests int64, errorCount int64, samples []float64, start, finished time.Time, loopErr error) (LoadResult, []byte, error) {
	elapsed := finished.Sub(start)
	result.Requests = requests
	result.Errors = errorCount
	result.SampleSize = len(samples)
	result.StartedAt = start
	result.FinishedAt = finished
	result.Duration = elapsed

	if err := ctx.Err(); err != nil {
		return result, []byte(renderLoadRaw(result, nil)), fmt.Errorf("%w: load run canceled: %w", ErrInvalid, err)
	}
	if loopErr != nil && !errors.Is(loopErr, context.DeadlineExceeded) {
		return result, []byte(renderLoadRaw(result, nil)), fmt.Errorf("%w: load run canceled: %w", ErrInvalid, loopErr)
	}
	if result.Requests == 0 {
		return result, []byte(renderLoadRaw(result, nil)), fmt.Errorf("%w: load run completed no requests", ErrInvalid)
	}
	if result.Errors == result.Requests {
		return result, []byte(renderLoadRaw(result, nil)), fmt.Errorf("%w: load run completed with no successful responses", ErrInvalid)
	}
	if elapsed > 0 {
		result.RPS = float64(result.Requests) / elapsed.Seconds()
	}
	sort.Float64s(samples)
	result.P50MS = percentile(samples, 50)
	result.P95MS = percentile(samples, 95)
	result.P99MS = percentile(samples, 99)
	return result, []byte(renderLoadRaw(result, samples)), nil
}

// NormalizeBenchmarkMethod returns the supported HTTP method used for a
// benchmark request. An empty method means GET.
func NormalizeBenchmarkMethod(method string) (string, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return http.MethodGet, nil
	}
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return method, nil
	default:
		return "", invalid("benchmark method", "%q must be GET, POST, PUT, PATCH, DELETE, HEAD, or OPTIONS", method)
	}
}

// NormalizeBenchmarkPath accepts a path or path?query value and returns an
// absolute in-app path. It rejects schemes, hosts, control characters,
// fragments, and traversal outside the requested site.
func NormalizeBenchmarkPath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "/", nil
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	if strings.HasPrefix(value, "//") || strings.Contains(value, "://") || strings.ContainsAny(value, "\x00\r\n\t ") {
		return "", invalid("benchmark path", "%q must be a URL path without a scheme, host, or control characters", raw)
	}
	if len(value) > 2048 {
		return "", invalid("benchmark path", "is longer than 2048 bytes")
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" {
		return "", invalid("benchmark path", "%q is not a valid URL path", raw)
	}
	return parsed.RequestURI(), nil
}

// NormalizeBenchmarkHeaders validates custom request headers and returns them
// canonicalized. Host is controlled by the selected site, and hop-by-hop
// headers are reserved for the transport.
func NormalizeBenchmarkHeaders(headers map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	if len(headers) > 100 {
		return nil, invalid("benchmark headers", "at most 100 headers are allowed")
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		name = strings.TrimSpace(name)
		if !validHeaderName(name) {
			return nil, invalid("benchmark header", "%q is not a valid header name", name)
		}
		canonical := http.CanonicalHeaderKey(name)
		switch canonical {
		case "Host", "Content-Length", "Transfer-Encoding", "Connection", "Proxy-Connection", "Upgrade", "Trailer", "Te":
			return nil, invalid("benchmark header", "%s is controlled by the benchmark transport", canonical)
		}
		if len(value) > 8192 || strings.ContainsAny(value, "\x00\r\n") {
			return nil, invalid("benchmark header", "%s contains a control character or is too long", canonical)
		}
		if _, exists := out[canonical]; exists {
			return nil, invalid("benchmark header", "%s is specified more than once", canonical)
		}
		out[canonical] = value
	}
	return out, nil
}

func validHeaderName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", char) {
			continue
		}
		return false
	}
	return true
}

func validateBenchmarkBody(method string, body []byte) error {
	if len(body) > MaxBenchmarkBodyBytes {
		return invalid("benchmark body", "is larger than %d bytes", MaxBenchmarkBodyBytes)
	}
	if len(body) > 0 && (method == http.MethodGet || method == http.MethodHead) {
		return invalid("benchmark body", "%s requests cannot include a body", method)
	}
	return nil
}

// ValidateBenchmarkBody exposes the body limits so the service can reject an
// invalid request before it creates a result directory or takes the run lock.
func ValidateBenchmarkBody(method string, body []byte) error {
	return validateBenchmarkBody(method, body)
}

func validateLoadOptions(opts LoadOptions) error {
	if _, err := NormalizeBenchmarkEngine(opts.Engine); err != nil {
		return err
	}
	parsed, err := url.Parse(opts.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return invalid("load url", "%q is not an absolute URL", opts.URL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return invalid("load url", "scheme %q must be http or https", parsed.Scheme)
	}
	host := parsed.Hostname()
	if !isLoopbackHost(host) {
		return invalid("load url", "host %q is not a loopback address", host)
	}
	if opts.Duration <= 0 || opts.Duration > maxLoadDuration {
		return invalid("load duration", "%s must be between 1ns and %s", opts.Duration, maxLoadDuration)
	}
	if opts.Connections < 1 || opts.Connections > 100000 {
		return invalid("load connections", "%d is outside 1-100000", opts.Connections)
	}
	if strings.ContainsAny(opts.HostHeader, "\x00\r\n \t/") {
		return invalid("load host header", "contains whitespace, a slash, or a control character")
	}
	method, err := NormalizeBenchmarkMethod(opts.Method)
	if err != nil {
		return err
	}
	if err := validateBenchmarkBody(method, opts.Body); err != nil {
		return err
	}
	if _, err := NormalizeBenchmarkHeaders(opts.Headers); err != nil {
		return err
	}
	return nil
}

func isLoopbackHost(host string) bool {
	return IsLocalOrTestHost(host)
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func renderLoadRaw(result LoadResult, samples []float64) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "tool=%s/%s\n", result.Tool, result.Version)
	fmt.Fprintf(&out, "engine=%s\n", result.Engine)
	fmt.Fprintf(&out, "method=%s url=%s body_bytes=%d\n", result.Method, result.URL, result.BodyBytes)
	fmt.Fprintf(&out, "started_at=%s\n", result.StartedAt.Format(time.RFC3339Nano))
	fmt.Fprintf(&out, "finished_at=%s\n", result.FinishedAt.Format(time.RFC3339Nano))
	fmt.Fprintf(&out, "duration=%s\n", result.Duration)
	fmt.Fprintf(&out, "connections=%d workers=%d sample_limit=%d\n", result.Connections, result.Workers, result.SampleLimit)
	fmt.Fprintf(&out, "requests=%d errors=%d requests_per_second=%.3f\n", result.Requests, result.Errors, result.RPS)
	fmt.Fprintf(&out, "p50_ms=%.3f p95_ms=%.3f p99_ms=%.3f\n", result.P50MS, result.P95MS, result.P99MS)
	out.WriteString("limitations:\n")
	for _, limitation := range result.Limitations {
		fmt.Fprintf(&out, "- %s\n", limitation)
	}
	if len(samples) > 0 {
		out.WriteString("latency_samples_ms=")
		step := len(samples) / 1000
		if step < 1 {
			step = 1
		}
		written := 0
		for i := 0; i < len(samples) && written < 1000; i += step {
			if written > 0 {
				out.WriteByte(',')
			}
			fmt.Fprintf(&out, "%.3f", samples[i])
			written++
		}
		out.WriteByte('\n')
	}
	return out.Bytes()
}
