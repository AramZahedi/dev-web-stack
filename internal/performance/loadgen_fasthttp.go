package performance

import (
	"context"
	"crypto/tls"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/valyala/fasthttp"
)

// runLoadFastHTTP performs a measured run with fasthttp's HTTP/1.1 client. It
// keeps the same Host-header and loopback rules as the net/http engine.
func runLoadFastHTTP(ctx context.Context, opts LoadOptions) (LoadResult, []byte, error) {
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
		Tool:        FastHTTPToolName,
		Version:     LoadToolVersion,
		Engine:      EngineFastHTTP,
		Limitations: append([]string(nil), FastHTTPLimitations...),
		URL:         opts.URL,
		Method:      method,
		BodyBytes:   len(opts.Body),
		Connections: opts.Connections,
		Workers:     workers,
		SampleLimit: loadSampleLimit,
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	tlsConfig := &tls.Config{
		InsecureSkipVerify: opts.InsecureTLS, //nolint:gosec // loopback benchmark only
		ServerName:         benchmarkTLSServerName(opts),
	}
	client := &fasthttp.Client{
		MaxConnsPerHost:               opts.Connections,
		MaxIdleConnDuration:           30 * time.Second,
		MaxConnWaitTimeout:            5 * time.Second,
		ReadTimeout:                   loadRequestTimeout,
		WriteTimeout:                  loadRequestTimeout,
		MaxResponseBodySize:           maxBenchmarkResponse,
		MaxIdemponentCallAttempts:     1,
		DisableHeaderNamesNormalizing: true,
		TLSConfig:                     tlsConfig,
		Dial: func(addr string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(addr)
			if splitErr == nil && isTestHost(host) {
				// Match the net/http engine: never let hosts-file or DNS
				// entries redirect a local .test benchmark off loopback.
				addr = net.JoinHostPort("127.0.0.1", port)
			}
			return dialer.Dial("tcp", addr)
		},
	}
	defer client.CloseIdleConnections()

	runDeadline := time.Now().Add(opts.Duration)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(runDeadline) {
		runDeadline = deadline
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
		seed = seed*6364136223846793005 + 1442695040888963407
		samples[int(seed%uint64(loadSampleLimit))] = latencyMS
	}

	start := time.Now().UTC()
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := fasthttp.AcquireRequest()
			resp := fasthttp.AcquireResponse()
			defer fasthttp.ReleaseRequest(req)
			defer fasthttp.ReleaseResponse(resp)

			req.SetRequestURI(opts.URL)
			req.Header.SetMethod(method)
			for name, value := range headers {
				req.Header.Set(name, value)
			}
			if opts.HostHeader != "" {
				req.Header.SetHost(opts.HostHeader)
				req.UseHostHeader = true
			}
			if _, ok := headers["Accept-Encoding"]; !ok {
				req.Header.Set("Accept-Encoding", "identity")
			}
			if len(opts.Body) > 0 {
				req.SetBodyRaw(opts.Body)
			}

			for {
				if ctx.Err() != nil {
					return
				}
				now := time.Now()
				if !now.Before(runDeadline) {
					return
				}
				deadline := now.Add(loadRequestTimeout)
				if deadline.After(runDeadline) {
					deadline = runDeadline
				}
				if !deadline.After(now) {
					return
				}
				requested := time.Now()
				err := client.DoDeadline(req, resp, deadline)
				latency := float64(time.Since(requested).Microseconds()) / 1000
				if err != nil {
					resp.Reset()
					if ctx.Err() != nil || !time.Now().Before(runDeadline) {
						return
					}
					record(latency, true)
					continue
				}
				failed := resp.StatusCode() >= 300
				resp.Reset()
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
	return finishLoadResult(ctx, result, finalRequests, finalErrors, finalSamples, start, finished, ctx.Err())
}

// benchmarkTLSServerName prefers the explicit Host header as SNI so a loopback
// dial still presents the site's domain and certificate.
func benchmarkTLSServerName(opts LoadOptions) string {
	if host := strings.TrimSpace(opts.HostHeader); host != "" && net.ParseIP(host) == nil {
		return host
	}
	parsed, err := url.Parse(opts.URL)
	if err != nil {
		return ""
	}
	host := parsed.Hostname()
	if net.ParseIP(host) != nil {
		return ""
	}
	return host
}
