package performance

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBenchmarkPlanDefaultsUseHighPorts(t *testing.T) {
	v := mustCompute(t, testResources(), Balanced, "site")
	plan, err := BuildBenchmarkPlan(v, BenchmarkOptions{})
	if err != nil {
		t.Fatalf("BuildBenchmarkPlan: %v", err)
	}
	if plan.Tool != BenchmarkWrk {
		t.Fatalf("Tool = %q, want wrk", plan.Tool)
	}
	if plan.HTTPPort != DefaultBenchmarkHTTPPort || plan.HTTPSPort != DefaultBenchmarkHTTPSPort {
		t.Fatalf("ports = %d/%d, want %d/%d", plan.HTTPPort, plan.HTTPSPort, DefaultBenchmarkHTTPPort, DefaultBenchmarkHTTPSPort)
	}
	if plan.HTTPPort < 1024 || plan.HTTPSPort < 1024 {
		t.Fatalf("benchmark ports must be unprivileged high ports, got %d/%d", plan.HTTPPort, plan.HTTPSPort)
	}
	if len(plan.Workloads) != 12 {
		t.Fatalf("workloads = %d, want 12", len(plan.Workloads))
	}

	names := map[string]bool{}
	for _, workload := range plan.Workloads {
		names[workload.Name] = true
		if !strings.Contains(workload.URL, ":18080") && !strings.Contains(workload.URL, ":18443") {
			t.Fatalf("workload %q URL %q does not use a high benchmark port", workload.Name, workload.URL)
		}
		if strings.Contains(workload.URL, ":80/") || strings.Contains(workload.URL, ":443/") {
			t.Fatalf("workload %q URL %q uses a privileged port", workload.Name, workload.URL)
		}
		if len(workload.Command) == 0 || workload.Command[0] != "wrk" {
			t.Fatalf("workload %q command = %v, want wrk", workload.Name, workload.Command)
		}
		if strings.Contains(strings.Join(workload.Command, " "), "requests/sec") {
			t.Fatalf("workload %q invents an RPS result", workload.Name)
		}
	}
	for _, want := range []string{
		"nginx-static-http",
		"nginx-static-http-nokeepalive",
		"nginx-php-http",
		"nginx-node-http",
		"nginx-static-https",
		"nginx-php-https",
		"apache-static-http",
		"apache-static-http-nokeepalive",
		"apache-php-http",
		"apache-static-https",
		"apache-php-https",
	} {
		if !names[want] {
			t.Fatalf("missing workload %q; got %v", want, names)
		}
	}

	again, err := BuildBenchmarkPlan(v, BenchmarkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, again) {
		t.Fatal("benchmark plan is not deterministic")
	}
}

func TestBenchmarkPlanKeepAliveAndHostHeader(t *testing.T) {
	v := mustCompute(t, testResources(), Balanced, "site")
	plan, err := BuildBenchmarkPlan(v, BenchmarkOptions{HostHeader: "bench.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, workload := range plan.Workloads {
		joined := strings.Join(workload.Command, " ")
		if workload.Name == "nginx-static-http-nokeepalive" || workload.Name == "apache-static-http-nokeepalive" {
			if workload.KeepAlive {
				t.Fatalf("workload %q should disable keepalive", workload.Name)
			}
			if !strings.Contains(joined, "Connection: close") {
				t.Fatalf("workload %q missing Connection: close: %v", workload.Name, workload.Command)
			}
		} else {
			if !workload.KeepAlive {
				t.Fatalf("workload %q should keep keepalive enabled", workload.Name)
			}
			if strings.Contains(joined, "Connection: close") {
				t.Fatalf("workload %q unexpectedly disables keepalive: %v", workload.Name, workload.Command)
			}
		}
		if !strings.Contains(joined, "Host: bench.test") {
			t.Fatalf("workload %q missing Host header: %v", workload.Name, workload.Command)
		}
	}
}

func TestBenchmarkPlanHeyTool(t *testing.T) {
	v := mustCompute(t, testResources(), Balanced, "site")
	plan, err := BuildBenchmarkPlan(v, BenchmarkOptions{
		Tool:       BenchmarkHey,
		Servers:    []string{"nginx"},
		Stacks:     []string{"static"},
		Schemes:    []string{"http", "https"},
		HostHeader: "bench.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Workloads) != 3 {
		t.Fatalf("workloads = %d, want 3", len(plan.Workloads))
	}
	for _, workload := range plan.Workloads {
		joined := strings.Join(workload.Command, " ")
		if workload.Command[0] != "hey" || !strings.Contains(joined, "-z 30s") || !strings.Contains(joined, "-host bench.test") {
			t.Fatalf("hey workload = %v", workload.Command)
		}
		if workload.Scheme == "https" && !strings.Contains(joined, "-disable-verify") {
			t.Fatalf("https hey workload missing -disable-verify: %v", workload.Command)
		}
	}
	// The HTTPS static workload plus the HTTP keepalive and no-keepalive
	// variants share the static stack.
	if plan.Workloads[0].Name != "nginx-static-http" || plan.Workloads[2].Name != "nginx-static-https" {
		t.Fatalf("workloads are not in canonical order: %+v", plan.Workloads)
	}
}

func TestBenchmarkPlanFilters(t *testing.T) {
	v := mustCompute(t, testResources(), Balanced, "site")
	plan, err := BuildBenchmarkPlan(v, BenchmarkOptions{
		Servers: []string{"apache"},
		Stacks:  []string{"static"},
		Schemes: []string{"http"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Workloads) != 2 {
		t.Fatalf("workloads = %d, want keepalive and no-keepalive static HTTP", len(plan.Workloads))
	}
	for _, workload := range plan.Workloads {
		if workload.Server != "apache" || workload.Stack != "static" || workload.Scheme != "http" {
			t.Fatalf("unexpected filtered workload %+v", workload)
		}
	}
}

func TestBenchmarkPlanValidation(t *testing.T) {
	v := mustCompute(t, testResources(), Balanced, "site")
	tests := []struct {
		name string
		opts BenchmarkOptions
	}{
		{"tool", BenchmarkOptions{Tool: "ab"}},
		{"http privileged", BenchmarkOptions{HTTPPort: 80}},
		{"https privileged", BenchmarkOptions{HTTPSPort: 443}},
		{"same ports", BenchmarkOptions{HTTPPort: 18080, HTTPSPort: 18080}},
		{"port too large", BenchmarkOptions{HTTPPort: 70000}},
		{"bad server", BenchmarkOptions{Servers: []string{"caddy"}}},
		{"bad stack", BenchmarkOptions{Stacks: []string{"perl"}}},
		{"bad scheme", BenchmarkOptions{Schemes: []string{"ftp"}}},
		{"negative duration", BenchmarkOptions{DurationSeconds: -1}},
		{"zero connections", BenchmarkOptions{Connections: -1}},
		{"zero threads", BenchmarkOptions{Threads: -1}},
		{"host header newline", BenchmarkOptions{HostHeader: "bench.test\r\nX: y"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildBenchmarkPlan(v, tt.opts); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}
