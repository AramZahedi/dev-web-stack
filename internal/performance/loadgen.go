package performance

import (
	"bytes"
	"context"
	"crypto/tls"
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

// Built-in load generator identity. No external wrk/hey image is pinned in
// this repository, so benchmarks are labelled as the built-in Go generator.
const (
	LoadToolName    = "devstack-go-http-load"
	LoadToolVersion = "1"
)

const (
	loadSampleLimit    = 200_000
	loadWorkerCap      = 2048
	loadRequestTimeout = 10 * time.Second
	maxLoadDuration    = time.Hour
)

// LoadLimitations is reported with every benchmark so a result is never
// presented as a wrk/hey-comparable measurement.
var LoadLimitations = []string{
	"Built-in Go HTTP load generator; not wrk, hey, or another pinned external tool.",
	"Requests are sequential per worker; the requested connection count is used as the HTTP transport connection limit.",
	"Worker goroutines are capped at 2048 for host safety.",
	"Latency percentiles use a bounded reservoir sample of up to 200000 completed requests.",
	"RPS counts completed HTTP exchanges (including non-2xx responses and transport failures); errors are reported separately.",
	"Redirects are not followed; 3xx, 4xx, and 5xx responses count as errors.",
	"Response bodies are read up to 4 KiB and then closed to bound memory and output.",
	"TLS uses InsecureSkipVerify only for loopback benchmark targets.",
}

// LoadOptions configures one measured run.
type LoadOptions struct {
	URL         string
	HostHeader  string
	Duration    time.Duration
	Connections int
	InsecureTLS bool
}

// LoadResult is a measured load-generator result. Zero values mean no result
// was produced; RPS is never estimated.
type LoadResult struct {
	Tool        string        `json:"tool"`
	Version     string        `json:"version"`
	Limitations []string      `json:"limitations"`
	URL         string        `json:"url"`
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

// RunLoad performs a real HTTP load run against a loopback URL. It never
// invents results: on cancellation or transport failure the returned result has
// no requests-per-second or latency claim and the error is non-nil.
func RunLoad(ctx context.Context, opts LoadOptions) (LoadResult, []byte, error) {
	if err := validateLoadOptions(opts); err != nil {
		return LoadResult{}, nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	workers := opts.Connections
	if workers > loadWorkerCap {
		workers = loadWorkerCap
	}
	result := LoadResult{
		Tool:        LoadToolName,
		Version:     LoadToolVersion,
		Limitations: append([]string(nil), LoadLimitations...),
		URL:         opts.URL,
		Connections: opts.Connections,
		Workers:     workers,
		SampleLimit: loadSampleLimit,
	}

	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
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
				req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, opts.URL, nil)
				if err != nil {
					reqCancel()
					record(0, true)
					return
				}
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
					if resp.StatusCode >= http.StatusBadRequest {
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
	elapsed := finished.Sub(start)

	mu.Lock()
	result.Requests = requests
	result.Errors = errors
	result.SampleSize = len(samples)
	sampleCopy := append([]float64(nil), samples...)
	mu.Unlock()
	result.StartedAt = start
	result.FinishedAt = finished
	result.Duration = elapsed

	if err := ctx.Err(); err != nil {
		return result, []byte(renderLoadRaw(result, nil)), fmt.Errorf("%w: load run canceled: %w", ErrInvalid, err)
	}
	if err := loopCtx.Err(); err != nil && err != context.DeadlineExceeded {
		return result, []byte(renderLoadRaw(result, nil)), fmt.Errorf("%w: load run canceled: %w", ErrInvalid, err)
	}
	if result.Requests == 0 {
		return result, []byte(renderLoadRaw(result, nil)), fmt.Errorf("%w: load run completed no requests", ErrInvalid)
	}
	if elapsed > 0 {
		result.RPS = float64(result.Requests) / elapsed.Seconds()
	}
	sort.Float64s(sampleCopy)
	result.P50MS = percentile(sampleCopy, 50)
	result.P95MS = percentile(sampleCopy, 95)
	result.P99MS = percentile(sampleCopy, 99)
	return result, []byte(renderLoadRaw(result, sampleCopy)), nil
}

func validateLoadOptions(opts LoadOptions) error {
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
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	fmt.Fprintf(&out, "url=%s\n", result.URL)
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
