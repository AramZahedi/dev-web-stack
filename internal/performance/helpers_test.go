package performance

import (
	"testing"
)

func testResources() Resources {
	return Resources{
		Hostname:             "devbox",
		Arch:                 "amd64",
		CPUCount:             8,
		CPUQuotaCPUs:         0,
		MemoryTotalBytes:     16 << 30,
		MemoryAvailableBytes: 8 << 30,
		PageSizeBytes:        4096,
		NoFileSoft:           1024,
		NoFileHard:           1 << 20,
		Sysctl: Sysctl{
			FileMax:          1 << 20,
			Somaxconn:        4096,
			NetdevMaxBacklog: 4096,
			IPLocalPortRange: PortRange{Min: 32768, Max: 60999},
		},
	}
}

func lowResources() Resources {
	return Resources{
		Hostname:             "small",
		Arch:                 "amd64",
		CPUCount:             1,
		MemoryTotalBytes:     512 << 20,
		MemoryAvailableBytes: 256 << 20,
		PageSizeBytes:        4096,
		NoFileSoft:           1024,
		NoFileHard:           4096,
		Sysctl: Sysctl{
			FileMax:          16384,
			Somaxconn:        1024,
			NetdevMaxBacklog: 1024,
			IPLocalPortRange: PortRange{Min: 32768, Max: 60999},
		},
	}
}

func highResources() Resources {
	return Resources{
		Hostname:             "big",
		Arch:                 "arm64",
		CPUCount:             32,
		CPUQuotaCPUs:         8.5,
		MemoryTotalBytes:     64 << 30,
		MemoryAvailableBytes: 48 << 30,
		PageSizeBytes:        4096,
		NoFileSoft:           1024,
		NoFileHard:           1 << 20,
		Sysctl: Sysctl{
			FileMax:          2 << 20,
			Somaxconn:        8192,
			NetdevMaxBacklog: 8192,
			IPLocalPortRange: PortRange{Min: 32768, Max: 60999},
		},
	}
}

func mustCompute(t *testing.T, res Resources, profile Profile, sites ...string) Values {
	t.Helper()
	v, err := Compute(res, Options{Profile: profile, PHPSites: sites})
	if err != nil {
		t.Fatalf("Compute(%s): %v", profile, err)
	}
	return v
}

func phpSiteIDs(v Values) []string {
	ids := make([]string, 0, len(v.PHP.Sites))
	for _, site := range v.PHP.Sites {
		ids = append(ids, site.SiteID)
	}
	return ids
}
