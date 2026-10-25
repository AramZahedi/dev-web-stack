package performance

import (
	"errors"
	"testing"
	"time"
)

func TestValuesValidateRejectsUnsafeTuning(t *testing.T) {
	base := mustCompute(t, testResources(), Balanced, "a", "b")
	tests := []struct {
		name   string
		mutate func(*Values)
	}{
		{"zero workers", func(v *Values) { v.Nginx.Workers = 0 }},
		{"too many workers", func(v *Values) { v.Nginx.Workers = maxNginxWorkers + 1 }},
		{"zero connections", func(v *Values) { v.Nginx.WorkerConnections = 0 }},
		{"zero rlimit", func(v *Values) { v.Nginx.WorkerRLimitNoFile = 0 }},
		{"rlimit below connections", func(v *Values) { v.Nginx.WorkerRLimitNoFile = uint64(v.Nginx.WorkerConnections) - 1 }},
		{"bad gzip level", func(v *Values) { v.Nginx.GzipCompLevel = 10 }},
		{"apache non-multiple", func(v *Values) { v.Apache.MaxRequestWorkers++ }},
		{"apache server limit mismatch", func(v *Values) { v.Apache.ServerLimit++ }},
		{"apache start beyond server limit", func(v *Values) { v.Apache.StartServers = v.Apache.ServerLimit + 1 }},
		{"apache spare inversion", func(v *Values) { v.Apache.MinSpareThreads = v.Apache.MaxSpareThreads + 1 }},
		{"apache spare gap", func(v *Values) {
			v.Apache.MaxSpareThreads = v.Apache.MinSpareThreads + v.Apache.ThreadsPerChild - 1
		}},
		{"php budget mismatch", func(v *Values) {
			v.PHP.GlobalChildren++
		}},
		{"php duplicate site", func(v *Values) { v.PHP.Sites[1].SiteID = v.PHP.Sites[0].SiteID }},
		{"php dynamic spares", func(v *Values) { v.PHP.Sites[0].StartServers = v.PHP.Sites[0].MinSpareServers - 1 }},
		{"php listen backlog", func(v *Values) { v.PHP.ListenBacklog++ }},
		{"apache listen backlog", func(v *Values) { v.Apache.ListenBacklog++ }},
		{"host file max", func(v *Values) { v.Host.FileMax = 1024 }},
		{"host nofile hard", func(v *Values) {
			v.Host.NoFileHard = v.Nginx.WorkerRLimitNoFile - 1
			if v.Host.NoFileSoft > v.Host.NoFileHard {
				v.Host.NoFileSoft = v.Host.NoFileHard
			}
		}},
		{"host port range unset", func(v *Values) { v.Host.IPLocalPortRange = PortRange{} }},
		{"custom profile with empty values", func(v *Values) { *v = Values{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := base
			v.PHP.Sites = append([]PHPSite(nil), base.PHP.Sites...)
			tt.mutate(&v)
			if err := v.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate() error = %v, want ErrInvalid", err)
			}
		})
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("unmodified values invalid: %v", err)
	}
}

func TestApplyPlanRejectsFilesystemRoot(t *testing.T) {
	v := mustCompute(t, testResources(), Balanced)
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	if _, _, err := BuildPlans("/", v, at, PriorHost{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("BuildPlans(/) error = %v, want ErrInvalid", err)
	}
}
