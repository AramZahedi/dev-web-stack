package performance

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Default high ports reserved for benchmark listeners. The harness must not
// run benchmarks against privileged 80/443 listeners.
const (
	DefaultBenchmarkHTTPPort  = 18080
	DefaultBenchmarkHTTPSPort = 18443
)

// BenchmarkTool identifies the load generator command.
type BenchmarkTool string

const (
	// BenchmarkWrk uses wrk.
	BenchmarkWrk BenchmarkTool = "wrk"
	// BenchmarkHey uses hey.
	BenchmarkHey BenchmarkTool = "hey"
)

// BenchmarkOptions selects workloads and load-generator parameters. Empty
// Servers/Stacks/Schemes mean all supported values. Zero numeric values use
// the defaults.
type BenchmarkOptions struct {
	Tool            BenchmarkTool
	Host            string
	HTTPPort        int
	HTTPSPort       int
	HostHeader      string
	DurationSeconds int
	Connections     int
	Threads         int
	Servers         []string
	Stacks          []string
	Schemes         []string
}

// BenchmarkWorkload is one command plan. It intentionally carries no expected
// requests-per-second or latency claim; results only come from a real run.
type BenchmarkWorkload struct {
	Name      string   `json:"name"`
	Server    string   `json:"server"`
	Stack     string   `json:"stack"`
	Scheme    string   `json:"scheme"`
	URL       string   `json:"url"`
	KeepAlive bool     `json:"keep_alive"`
	Command   []string `json:"command"`
}

// BenchmarkPlan is a reproducible set of benchmark commands.
type BenchmarkPlan struct {
	Tool      BenchmarkTool       `json:"tool"`
	Host      string              `json:"host"`
	HTTPPort  int                 `json:"http_port"`
	HTTPSPort int                 `json:"https_port"`
	Workloads []BenchmarkWorkload `json:"workloads"`
}

// BuildBenchmarkPlan renders command plans for the design's static, PHP, and
// Node workloads over HTTP and HTTPS. It never runs the commands.
func BuildBenchmarkPlan(v Values, opts BenchmarkOptions) (BenchmarkPlan, error) {
	tool := opts.Tool
	if tool == "" {
		tool = BenchmarkWrk
	}
	if tool != BenchmarkWrk && tool != BenchmarkHey {
		return BenchmarkPlan{}, invalid("benchmark tool", "%q must be wrk or hey", tool)
	}

	host := opts.Host
	if host == "" {
		host = "127.0.0.1"
	}
	if err := validateBenchmarkHost(host); err != nil {
		return BenchmarkPlan{}, err
	}
	httpPort, err := benchmarkPort("http port", opts.HTTPPort, DefaultBenchmarkHTTPPort)
	if err != nil {
		return BenchmarkPlan{}, err
	}
	httpsPort, err := benchmarkPort("https port", opts.HTTPSPort, DefaultBenchmarkHTTPSPort)
	if err != nil {
		return BenchmarkPlan{}, err
	}
	if httpPort == httpsPort {
		return BenchmarkPlan{}, invalid("benchmark ports", "HTTP and HTTPS ports must differ, both are %d", httpPort)
	}
	if strings.ContainsAny(opts.HostHeader, "\x00\r\n \t/") {
		return BenchmarkPlan{}, invalid("benchmark host header", "contains whitespace, a slash, or a control character")
	}

	duration := opts.DurationSeconds
	if duration == 0 {
		duration = 30
	}
	if duration < 1 || duration > 3600 {
		return BenchmarkPlan{}, invalid("benchmark duration", "%d is outside 1-3600 seconds", duration)
	}
	connections := opts.Connections
	if connections == 0 {
		connections = 100
	}
	if connections < 1 || connections > 100000 {
		return BenchmarkPlan{}, invalid("benchmark connections", "%d is outside 1-100000", connections)
	}
	threads := opts.Threads
	if threads == 0 {
		threads = clampInt(v.Nginx.Workers, 1, 8)
	}
	if threads < 1 || threads > 1024 {
		return BenchmarkPlan{}, invalid("benchmark threads", "%d is outside 1-1024", threads)
	}

	servers, err := benchmarkSelection("servers", opts.Servers, []string{"nginx", "apache"})
	if err != nil {
		return BenchmarkPlan{}, err
	}
	stacks, err := benchmarkSelection("stacks", opts.Stacks, []string{"static", "php", "node"})
	if err != nil {
		return BenchmarkPlan{}, err
	}
	schemes, err := benchmarkSelection("schemes", opts.Schemes, []string{"http", "https"})
	if err != nil {
		return BenchmarkPlan{}, err
	}

	plan := BenchmarkPlan{
		Tool:      tool,
		Host:      host,
		HTTPPort:  httpPort,
		HTTPSPort: httpsPort,
	}
	for _, server := range servers {
		for _, scheme := range schemes {
			port := httpPort
			if scheme == "https" {
				port = httpsPort
			}
			url := scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/"
			for _, stack := range stacks {
				if server == "apache" && stack == "node" {
					continue
				}
				name := server + "-" + stack + "-" + scheme
				plan.Workloads = append(plan.Workloads, benchmarkWorkload(
					name, server, stack, scheme, url, true, tool, opts.HostHeader, duration, connections, threads,
				))
				if scheme == "http" && stack == "static" {
					plan.Workloads = append(plan.Workloads, benchmarkWorkload(
						name+"-nokeepalive", server, stack, scheme, url, false, tool, opts.HostHeader, duration, connections, threads,
					))
				}
			}
		}
	}
	if len(plan.Workloads) == 0 {
		return BenchmarkPlan{}, invalid("benchmark workloads", "the selected servers, stacks, and schemes produce none")
	}
	return plan, nil
}

func benchmarkWorkload(name, server, stack, scheme, url string, keepAlive bool, tool BenchmarkTool, hostHeader string, duration, connections, threads int) BenchmarkWorkload {
	command := make([]string, 0, 12)
	switch tool {
	case BenchmarkHey:
		command = append(command, "hey", "-z", fmt.Sprintf("%ds", duration), "-c", strconv.Itoa(connections), "-q", "0")
		if scheme == "https" {
			command = append(command, "-disable-verify")
		}
		if hostHeader != "" {
			command = append(command, "-host", hostHeader)
		}
		if !keepAlive {
			command = append(command, "-disable-keepalive")
		}
	default:
		command = append(command, "wrk", fmt.Sprintf("-t%d", threads), fmt.Sprintf("-c%d", connections), fmt.Sprintf("-d%ds", duration), "--latency")
		if hostHeader != "" {
			command = append(command, "-H", "Host: "+hostHeader)
		}
		if !keepAlive {
			command = append(command, "-H", "Connection: close")
		}
	}
	command = append(command, url)
	return BenchmarkWorkload{
		Name:      name,
		Server:    server,
		Stack:     stack,
		Scheme:    scheme,
		URL:       url,
		KeepAlive: keepAlive,
		Command:   command,
	}
}

func benchmarkPort(field string, value, fallback int) (int, error) {
	if value == 0 {
		value = fallback
	}
	if value < 1024 || value > 65535 {
		return 0, invalid("benchmark "+field, "%d must be an unprivileged port in 1024-65535", value)
	}
	return value, nil
}

func benchmarkSelection(field string, requested, allowed []string) ([]string, error) {
	if len(requested) == 0 {
		return append([]string(nil), allowed...), nil
	}
	wanted := make(map[string]bool, len(requested))
	for _, value := range requested {
		found := false
		for _, candidate := range allowed {
			if value == candidate {
				found = true
				break
			}
		}
		if !found {
			return nil, invalid("benchmark "+field, "%q is not one of %s", value, strings.Join(allowed, ", "))
		}
		wanted[value] = true
	}
	result := make([]string, 0, len(allowed))
	for _, candidate := range allowed {
		if wanted[candidate] {
			result = append(result, candidate)
		}
	}
	return result, nil
}

func validateBenchmarkHost(host string) error {
	if host == "" {
		return invalid("benchmark host", "is required")
	}
	if len(host) > 255 {
		return invalid("benchmark host", "is longer than 255 bytes")
	}
	if strings.ContainsAny(host, "\x00\r\n \t/") {
		return invalid("benchmark host", "%q contains whitespace, a slash, or a control character", host)
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '-', r == ':':
		default:
			return invalid("benchmark host", "%q contains an invalid character", host)
		}
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return invalid("benchmark host", "%q is not a valid IPv6 address", host)
	}
	return nil
}
