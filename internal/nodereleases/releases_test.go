package nodereleases

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseIndexFiltersStableLinuxX64Releases(t *testing.T) {
	releases := loadIndexFixture(t)

	wantVersions := []string{
		"22.11.0",
		"22.10.0",
		"22.9.1",
		"20.18.0",
		"20.17.0",
		"18.20.4",
		"18.20.3",
	}
	if len(releases) != len(wantVersions) {
		t.Fatalf("ParseIndex returned %d releases, want %d: %+v", len(releases), len(wantVersions), releases)
	}
	for i, release := range releases {
		if release.Version != wantVersions[i] {
			t.Fatalf("release %d version = %q, want %q", i, release.Version, wantVersions[i])
		}
		wantFile := "node-v" + release.Version + "-linux-x64.tar.gz"
		if release.File != wantFile {
			t.Errorf("%s file = %q, want %q", release.Version, release.File, wantFile)
		}
		if release.Date == "" || release.NPM == "" || release.Modules == "" {
			t.Errorf("%s metadata = date %q npm %q modules %q, want all populated",
				release.Version, release.Date, release.NPM, release.Modules)
		}
	}

	latest := releases[0]
	if latest.Date != "2024-10-29" {
		t.Errorf("v22.11.0 date = %q, want 2024-10-29", latest.Date)
	}
	if latest.LTS != "Jod" {
		t.Errorf("v22.11.0 lts = %q, want Jod", latest.LTS)
	}
	if latest.Security {
		t.Error("v22.11.0 security = true, want false")
	}
	if latest.NPM != "10.9.0" {
		t.Errorf("v22.11.0 npm = %q, want 10.9.0", latest.NPM)
	}
	if latest.Modules != "127" {
		t.Errorf("v22.11.0 modules = %q, want 127", latest.Modules)
	}

	if got := releases[2].LTS; got != "" {
		t.Errorf("v22.9.1 lts = %q, want empty when the index says false", got)
	}
	security := releases[3]
	if !security.Security {
		t.Error("v20.18.0 security = false, want true")
	}
	if security.LTS != "Iron" {
		t.Errorf("v20.18.0 lts = %q, want Iron", security.LTS)
	}
}

func TestParseIndexSkipsPrereleaseAndBuildMetadataVersions(t *testing.T) {
	index := `[
		{"version":"v23.0.0-rc.1","files":["linux-x64"]},
		{"version":"v22.12.0+build.1","files":["linux-x64"]},
		{"version":"v22.11.0","files":["linux-x64"]}
	]`

	releases, err := ParseIndex([]byte(index))
	if err != nil {
		t.Fatalf("ParseIndex: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("ParseIndex returned %d releases, want 1: %+v", len(releases), releases)
	}
	if releases[0].Version != "22.11.0" {
		t.Fatalf("release version = %q, want 22.11.0", releases[0].Version)
	}
}

func TestParseIndexRejectsMalformedIndex(t *testing.T) {
	tests := []struct {
		name    string
		index   string
		wantErr string
	}{
		{name: "malformed json", index: `[{"version":`, wantErr: "decode"},
		{name: "not an array", index: `{"version":"v22.11.0"}`, wantErr: "decode"},
		{name: "missing v prefix", index: `[{"version":"22.11.0","files":["linux-x64"]}]`, wantErr: "version"},
		{name: "malformed version", index: `[{"version":"v22.11","files":["linux-x64"]}]`, wantErr: "version"},
		{name: "leading zero", index: `[{"version":"v022.11.0","files":["linux-x64"]}]`, wantErr: "version"},
		{name: "invalid lts", index: `[{"version":"v22.11.0","files":["linux-x64"],"lts":true}]`, wantErr: "lts"},
		{
			name: "duplicate version",
			index: `[
				{"version":"v22.11.0","files":["linux-x64"]},
				{"version":"v22.11.0","files":["linux-x64"]}
			]`,
			wantErr: "duplicate",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseIndex([]byte(test.index))
			if err == nil {
				t.Fatalf("ParseIndex accepted a %s index", test.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), test.wantErr) {
				t.Fatalf("ParseIndex error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestGroupByMajorPutsNewestPatchFirst(t *testing.T) {
	releases := loadIndexFixture(t)
	slices.Reverse(releases) // GroupByMajor must sort rather than trust index order.

	majors := GroupByMajor(releases)
	want := []struct {
		major    int
		versions []string
	}{
		{major: 22, versions: []string{"22.11.0", "22.10.0", "22.9.1"}},
		{major: 20, versions: []string{"20.18.0", "20.17.0"}},
		{major: 18, versions: []string{"18.20.4", "18.20.3"}},
	}
	if len(majors) != len(want) {
		t.Fatalf("GroupByMajor returned %d majors, want %d: %+v", len(majors), len(want), majors)
	}
	for i, expected := range want {
		group := majors[i]
		if group.Major != expected.major {
			t.Fatalf("major %d = %d, want %d", i, group.Major, expected.major)
		}
		if len(group.Releases) != len(expected.versions) {
			t.Fatalf("major %d has %d releases, want %d: %+v",
				group.Major, len(group.Releases), len(expected.versions), group.Releases)
		}
		for j, version := range expected.versions {
			if group.Releases[j].Version != version {
				t.Errorf("major %d release %d = %q, want %q",
					group.Major, j, group.Releases[j].Version, version)
			}
		}
		if group.Latest.Version != expected.versions[0] {
			t.Errorf("major %d latest = %q, want %q",
				group.Major, group.Latest.Version, expected.versions[0])
		}
		if group.Latest != group.Releases[0] {
			t.Errorf("major %d latest = %+v, want first release %+v",
				group.Major, group.Latest, group.Releases[0])
		}
	}
}

func TestSelectResolvesMajorMinorAndExact(t *testing.T) {
	releases := loadIndexFixture(t)
	slices.Reverse(releases) // Select must choose the newest match, not the first.

	valid := []struct {
		selector string
		want     string
	}{
		{selector: "22", want: "22.11.0"},
		{selector: "20", want: "20.18.0"},
		{selector: "18", want: "18.20.4"},
		{selector: "22.10", want: "22.10.0"},
		{selector: "18.20", want: "18.20.4"},
		{selector: "22.9.1", want: "22.9.1"},
		{selector: "20.17.0", want: "20.17.0"},
	}
	for _, test := range valid {
		release, err := Select(releases, test.selector)
		if err != nil {
			t.Fatalf("Select(%q): %v", test.selector, err)
		}
		if release.Version != test.want {
			t.Errorf("Select(%q) = %q, want %q", test.selector, release.Version, test.want)
		}
	}

	invalid := []string{
		"",
		"v22",
		"node@22",
		"22.11.0.1",
		"22..1",
		"22.x",
		" 22",
		"22 ",
		"022",
		"22.011",
		"22.11.0+meta",
		"21.7",
		"23",
		"22.12",
		"22.9.2",
	}
	for _, selector := range invalid {
		release, err := Select(releases, selector)
		if !errors.Is(err, ErrReleaseNotFound) {
			t.Errorf("Select(%q) = %+v, %v; want ErrReleaseNotFound", selector, release, err)
		}
		if release != (Release{}) {
			t.Errorf("Select(%q) = %+v, want a zero release", selector, release)
		}
	}
}

func loadIndexFixture(t *testing.T) []Release {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "index.json"))
	if err != nil {
		t.Fatalf("read testdata/index.json: %v", err)
	}
	releases, err := ParseIndex(data)
	if err != nil {
		t.Fatalf("ParseIndex: %v", err)
	}
	return releases
}
