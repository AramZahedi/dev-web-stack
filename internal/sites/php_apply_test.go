package sites

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpfpm"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

func TestApplyPHPIniChangeOnlyAffectsOneSite(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	blog := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	shop := mustCreate(t, svc, fakes, CreateRequest{Name: "shop", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	blogHash, shopHash := blog.PHPConfigHash, shop.PHPConfigHash
	blogPort, shopPort := blog.FastCGIPort, shop.FastCGIPort
	starts := fakes.backend.startCount()

	updated, err := svc.ApplyPHP(ctx, blog.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PHPConfigHash == blogHash {
		t.Fatal("ini change did not change the config hash")
	}
	if updated.FastCGIPort == blogPort {
		t.Fatal("ini change reused the running master's port")
	}
	if updated.PHPIni["memory_limit"] != "512M" {
		t.Fatalf("row ini = %v", updated.PHPIni)
	}
	ini := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, updated), "php.ini")))
	if !strings.Contains(ini, "memory_limit = 512M") {
		t.Fatalf("generated ini does not contain the override:\n%s", ini)
	}
	if active := fakes.backend.phpActiveGeneration(blog.ID); active != updated.PHPConfigHash {
		t.Fatalf("active generation = %q, want %q", active, updated.PHPConfigHash)
	}
	if fakes.backend.startCount() != starts+1 {
		t.Fatalf("starts = %d, want %d", fakes.backend.startCount(), starts+1)
	}

	storedShop, err := fakes.store.GetSite(ctx, shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedShop.PHPConfigHash != shopHash || storedShop.FastCGIPort != shopPort {
		t.Fatalf("unrelated site changed: %+v", storedShop)
	}
	if active := fakes.backend.phpActiveGeneration(shop.ID); active != phpDefaultGeneration {
		t.Fatalf("unrelated site active generation = %q", active)
	}
	if got := phpGenerationDirs(t, fakes, shop.ID); len(got) != 1 {
		t.Fatalf("unrelated site generations = %v", got)
	}
	if got := phpGenerationDirs(t, fakes, blog.ID); len(got) != 2 {
		t.Fatalf("blog generations = %v, want current and previous", got)
	}
}

func TestApplyPHPVersionSwitchPromotesAndDrains(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, "nginx")
	r1 := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	r2 := fakes.installPHPBuild("php@8.5.1-r1", "8.5.1")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
		PHPVersion: "8.4", PHPBuild: r1.ID})
	oldHash, oldPort := site.PHPConfigHash, site.FastCGIPort
	*fakes.order = nil

	updated, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Version: "8.5"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PHPVersion != "8.5" || updated.PHPBuild != r2.ID {
		t.Fatalf("row = %+v", updated)
	}
	if updated.PHPConfigHash == oldHash || updated.FastCGIPort == oldPort {
		t.Fatalf("apply did not stage a new generation: %+v", updated)
	}
	route, ok := fakes.router.Lookup("blog.test")
	if !ok {
		t.Fatal("route missing")
	}
	if route.Upstream != net.JoinHostPort("127.0.0.1", strconv.Itoa(updated.FastCGIPort)) {
		t.Fatalf("route upstream = %q", route.Upstream)
	}
	spec, ok := fakes.backend.phpSpec(site.ID, updated.PHPConfigHash)
	if !ok {
		t.Fatal("new generation was not prepared")
	}
	if spec.PHPBuild != r2.ID || spec.PHPBuildPath != r2.Path {
		t.Fatalf("staged spec = %+v", spec)
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != updated.PHPConfigHash {
		t.Fatalf("active generation = %q", active)
	}
	if fakes.backend.phpStarted(site.ID, phpDefaultGeneration) {
		t.Fatal("default generation still running after apply")
	}
	if fakes.backend.phpStarted(site.ID, oldHash) {
		t.Fatal("old hash generation still running after apply")
	}
	events := fakes.backend.phpEventList()
	stopDefault := phpEventIndex(events, "stop", site.ID, phpDefaultGeneration)
	stopOld := phpEventIndex(events, "stop", site.ID, oldHash)
	if stopDefault < 0 || stopOld < 0 {
		t.Fatalf("old generations were not stopped: %+v", events)
	}
	order := strings.Join(*fakes.order, ",")
	vhostAt := strings.Index(order, "vhost-sync")
	stopAt := strings.Index(order, "php-stop:"+site.ID+"#"+phpDefaultGeneration)
	if vhostAt < 0 || stopAt < 0 || vhostAt > stopAt {
		t.Fatalf("proxy publish did not precede draining: %q", order)
	}
	if got := reservedFastCGIPorts(t, fakes); !reflect.DeepEqual(got, []int{updated.FastCGIPort}) {
		t.Fatalf("reserved ports = %v, want only %d", got, updated.FastCGIPort)
	}
}

func TestApplyPHPVersionSwitchRollsBackOnHealthFailure(t *testing.T) {
	svc, fakes := newTestService(t)
	r1 := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.installPHPBuild("php@8.5.1-r1", "8.5.1")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
		PHPVersion: "8.4", PHPBuild: r1.ID})
	oldHash, oldPort, oldBuild := site.PHPConfigHash, site.FastCGIPort, site.PHPBuild
	fakes.backend.startErr = errors.New("health timeout")

	if _, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Version: "8.5"}); err == nil ||
		!strings.Contains(err.Error(), "health timeout") {
		t.Fatalf("err = %v", err)
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PHPBuild != oldBuild || stored.PHPVersion != "8.4" || stored.PHPConfigHash != oldHash || stored.FastCGIPort != oldPort {
		t.Fatalf("row not restored: %+v", stored)
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != phpDefaultGeneration {
		t.Fatalf("active generation = %q, want default", active)
	}
	if !fakes.backend.phpStarted(site.ID, phpDefaultGeneration) {
		t.Fatal("old master is no longer running")
	}
	if got := reservedFastCGIPorts(t, fakes); !reflect.DeepEqual(got, []int{oldPort}) {
		t.Fatalf("reserved ports = %v, want [%d]", got, oldPort)
	}
	if got := phpGenerationDirs(t, fakes, site.ID); len(got) != 1 || got[0] != oldHash {
		t.Fatalf("generations = %v, want only %s", got, oldHash)
	}
	route, _ := fakes.router.Lookup("blog.test")
	if route.Upstream != net.JoinHostPort("127.0.0.1", strconv.Itoa(oldPort)) {
		t.Fatalf("route upstream = %q", route.Upstream)
	}
	snapshot, err := fakes.store.GetSitePHPConfig(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ConfigHash != oldHash {
		t.Fatalf("snapshot hash = %q, want %q", snapshot.ConfigHash, oldHash)
	}

	fakes.backend.startErr = nil
	updated, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Version: "8.5"})
	if err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if updated.FastCGIPort == oldPort || updated.PHPConfigHash == oldHash {
		t.Fatalf("retry reused rolled-back state: %+v", updated)
	}
}

func TestApplyPHPInvalidIniLeavesOldMasterServing(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	oldHash, oldPort := site.PHPConfigHash, site.FastCGIPort
	_, fpmBefore := fakes.php.counts()
	fakes.php.lintErr = &phpini.LintError{Line: 4, Output: "syntax error"}

	_, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, phpini.ErrInvalidConfig) {
		t.Fatalf("error does not wrap ErrInvalidConfig: %v", err)
	}
	if !strings.Contains(err.Error(), "line 4") || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("error lost the lint output: %v", err)
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PHPConfigHash != oldHash || stored.FastCGIPort != oldPort {
		t.Fatalf("row changed: %+v", stored)
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != phpDefaultGeneration {
		t.Fatalf("active generation = %q", active)
	}
	if got := phpGenerationDirs(t, fakes, site.ID); len(got) != 1 || got[0] != oldHash {
		t.Fatalf("generations = %v", got)
	}
	if got := reservedFastCGIPorts(t, fakes); !reflect.DeepEqual(got, []int{oldPort}) {
		t.Fatalf("reserved ports = %v", got)
	}
	if _, fpm := fakes.php.counts(); fpm != fpmBefore {
		t.Fatalf("FPM validator ran after the ini failed: %d -> %d calls", fpmBefore, fpm)
	}
}

func TestApplyPHPFPMFailurePreservesErrorIdentity(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	oldHash := site.PHPConfigHash
	fakes.php.fpmErr = &phpfpm.ConfigError{Field: "listen", Message: "address unavailable"}

	_, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, phpfpm.ErrConfigInvalid) {
		t.Fatalf("error does not wrap ErrConfigInvalid: %v", err)
	}
	if !strings.Contains(err.Error(), "listen") || !strings.Contains(err.Error(), "address unavailable") {
		t.Fatalf("error lost the validator output: %v", err)
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != phpDefaultGeneration {
		t.Fatalf("active generation = %q", active)
	}
	if got := phpGenerationDirs(t, fakes, site.ID); len(got) != 1 || got[0] != oldHash {
		t.Fatalf("generations = %v", got)
	}
}

func TestApplyPHPRejectsInvalidRawBeforeWriting(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	oldHash := site.PHPConfigHash
	raw := "extension = evil.so\n"

	_, err := svc.ApplyPHP(ctx, site.ID, PHPChange{IniRaw: &raw})
	if err == nil || !errors.Is(err, phpini.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	if got := phpGenerationDirs(t, fakes, site.ID); len(got) != 1 || got[0] != oldHash {
		t.Fatalf("generations = %v", got)
	}
}

func TestApplyPHPVhostFailureKeepsOldMaster(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, "nginx")
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	oldHash, oldPort := site.PHPConfigHash, site.FastCGIPort
	fakes.vhosts.err = errors.New("bad config")

	_, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err == nil || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("err = %v", err)
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PHPConfigHash != oldHash || stored.FastCGIPort != oldPort {
		t.Fatalf("row changed: %+v", stored)
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != phpDefaultGeneration {
		t.Fatalf("active generation = %q", active)
	}
	route, _ := fakes.router.Lookup("blog.test")
	if route.Upstream != net.JoinHostPort("127.0.0.1", strconv.Itoa(oldPort)) {
		t.Fatalf("route upstream = %q", route.Upstream)
	}
	if got := phpGenerationDirs(t, fakes, site.ID); len(got) != 1 || got[0] != oldHash {
		t.Fatalf("generations = %v", got)
	}
	if got := reservedFastCGIPorts(t, fakes); !reflect.DeepEqual(got, []int{oldPort}) {
		t.Fatalf("reserved ports = %v", got)
	}
	snapshot, err := fakes.store.GetSitePHPConfig(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ConfigHash != oldHash {
		t.Fatalf("snapshot hash = %q", snapshot.ConfigHash)
	}

	fakes.vhosts.err = nil
	if _, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}}); err != nil {
		t.Fatalf("retry after vhost rollback: %v", err)
	}
}

func TestApplyPHPStoppedSiteNeedsNoBackend(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	oldPort := site.FastCGIPort
	svc.deps.PHPBackend = nil
	svc.deps.Backend = nil
	svc.deps.Certs = nil
	svc.deps.Router = nil
	lint, fpm := fakes.php.counts()

	updated, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.FastCGIPort != oldPort {
		t.Fatalf("stopped apply changed the port: %d -> %d", oldPort, updated.FastCGIPort)
	}
	if updated.PHPConfigHash == site.PHPConfigHash {
		t.Fatal("stopped apply did not write a new generation")
	}
	if gotLint, gotFPM := fakes.php.counts(); gotLint != lint+1 || gotFPM != fpm+1 {
		t.Fatalf("validators not run: lint %d->%d fpm %d->%d", lint, gotLint, fpm, gotFPM)
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != "" {
		t.Fatalf("stopped apply started generation %q", active)
	}
	snapshot, err := fakes.store.GetSitePHPConfig(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ConfigHash != updated.PHPConfigHash {
		t.Fatalf("snapshot hash = %q, want %q", snapshot.ConfigHash, updated.PHPConfigHash)
	}
}

func TestApplyPHPStoppedSiteAllocatesAPortWhenMissing(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldPort := stored.FastCGIPort
	stored.FastCGIPort = 0
	if err := fakes.store.UpdateSite(ctx, stored); err != nil {
		t.Fatal(err)
	}
	fakes.ports.Release(oldPort)
	svc.deps.PHPBackend = nil
	svc.deps.Backend = nil

	updated, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.FastCGIPort == 0 || updated.FastCGIPort == oldPort {
		t.Fatalf("port = %d, want a new allocation", updated.FastCGIPort)
	}
	fpm := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, updated), "php-fpm.conf")))
	if !strings.Contains(fpm, "listen = 127.0.0.1:"+strconv.Itoa(updated.FastCGIPort)) {
		t.Fatalf("fpm config does not use the allocated port:\n%s", fpm)
	}
	if got := reservedFastCGIPorts(t, fakes); !reflect.DeepEqual(got, []int{updated.FastCGIPort}) {
		t.Fatalf("reserved ports = %v", got)
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != "" {
		t.Fatalf("stopped apply started generation %q", active)
	}
}

func TestApplyPHPNoOpDoesNotStageAMaster(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	hash, port := site.PHPConfigHash, site.FastCGIPort
	starts := fakes.backend.startCount()
	lint, fpm := fakes.php.counts()

	updated, err := svc.ApplyPHP(ctx, site.ID, PHPChange{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PHPConfigHash != hash || updated.FastCGIPort != port {
		t.Fatalf("no-op apply changed the generation: %+v", updated)
	}
	if fakes.backend.startCount() != starts {
		t.Fatal("no-op apply started a master")
	}
	if gotLint, gotFPM := fakes.php.counts(); gotLint != lint || gotFPM != fpm {
		t.Fatal("no-op apply revalidated the configuration")
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != phpDefaultGeneration {
		t.Fatalf("active generation = %q", active)
	}
}

func TestApplyPHPBuildOnlyChangePersistsWithoutRestaging(t *testing.T) {
	svc, fakes := newTestService(t)
	build := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
		PHPVersion: "8.4", PHPBuild: build.ID})
	alias := build
	alias.ID = "php@8.4.4-r2"
	if err := fakes.store.UpsertRuntime(ctx, alias); err != nil {
		t.Fatal(err)
	}
	hash, port := site.PHPConfigHash, site.FastCGIPort
	starts := fakes.backend.startCount()

	updated, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Build: alias.ID})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PHPBuild != alias.ID {
		t.Fatalf("build = %q, want %q", updated.PHPBuild, alias.ID)
	}
	if updated.PHPConfigHash != hash || updated.FastCGIPort != port {
		t.Fatalf("row-only change restaged the config: %+v", updated)
	}
	if fakes.backend.startCount() != starts {
		t.Fatal("row-only change started a master")
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != phpDefaultGeneration {
		t.Fatalf("active generation = %q", active)
	}
}

func TestApplyPHPSecondApplyStopsPreviousHashGeneration(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	first, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err != nil {
		t.Fatal(err)
	}
	firstHash, firstPort := first.PHPConfigHash, first.FastCGIPort

	second, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "1G"}})
	if err != nil {
		t.Fatal(err)
	}
	if second.PHPConfigHash == firstHash || second.FastCGIPort == firstPort {
		t.Fatalf("second apply reused the first generation: %+v", second)
	}
	if fakes.backend.phpStarted(site.ID, firstHash) {
		t.Fatal("previous hash generation still running")
	}
	if fakes.backend.phpStarted(site.ID, phpDefaultGeneration) {
		t.Fatal("default generation still running")
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != second.PHPConfigHash {
		t.Fatalf("active generation = %q", active)
	}
	if got := reservedFastCGIPorts(t, fakes); !reflect.DeepEqual(got, []int{second.FastCGIPort}) {
		t.Fatalf("reserved ports = %v", got)
	}
	events := fakes.backend.phpEventList()
	if phpEventIndex(events, "stop", site.ID, firstHash) < 0 {
		t.Fatalf("previous hash generation was not stopped: %+v", events)
	}
}

func TestApplyPHPStopFailureKeepsOldPortAndSucceeds(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	first, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err != nil {
		t.Fatal(err)
	}
	firstPort := first.FastCGIPort
	fakes.backend.stopPHPErr = errors.New("stop timeout")

	second, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "1G"}})
	if err != nil {
		t.Fatalf("healthy new master must not be rolled back: %v", err)
	}
	if second.FastCGIPort == firstPort {
		t.Fatal("second apply reused the old port")
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != second.PHPConfigHash {
		t.Fatalf("active generation = %q", active)
	}
	if !fakes.backend.phpStarted(site.ID, first.PHPConfigHash) {
		t.Fatal("failed stop unexpectedly terminated the old generation")
	}
	got := reservedFastCGIPorts(t, fakes)
	want := []int{firstPort, second.FastCGIPort}
	sort.Ints(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reserved ports = %v, want old and new (%v)", got, want)
	}
	if !hasEvent(t, fakes, site.ID, "site.error") {
		t.Fatal("stop failure was not recorded as an event")
	}
}

func TestApplyPHPPrunesAllButCurrentAndPrevious(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	initial := site.PHPConfigHash
	first, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "1G"}})
	if err != nil {
		t.Fatal(err)
	}
	third, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "2G"}})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{second.PHPConfigHash, third.PHPConfigHash}
	sort.Strings(want)
	if got := phpGenerationDirs(t, fakes, site.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("generations = %v, want current and previous only (%v)", got, want)
	}
	for _, stale := range []string{initial, first.PHPConfigHash} {
		if _, err := os.Stat(filepath.Join(fakes.config.Home, "sites", site.ID, stale)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale generation %s still present: %v", stale, err)
		}
	}
	for _, name := range []string{"php.ini", "php-fpm.conf", "health.php"} {
		if _, err := os.Stat(filepath.Join(fakes.config.Home, "sites", site.ID, name)); err != nil {
			t.Fatalf("stable %s missing: %v", name, err)
		}
	}
}

func TestStartAfterApplyDoesNotStartASecondMaster(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	applied, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err != nil {
		t.Fatal(err)
	}
	starts := fakes.backend.startCount()

	if err := svc.Start(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if fakes.backend.startCount() != starts {
		t.Fatal("Start after apply started another master")
	}
	if fakes.backend.phpStarted(site.ID, phpDefaultGeneration) {
		t.Fatal("Start after apply started the default generation")
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != applied.PHPConfigHash {
		t.Fatalf("active generation = %q", active)
	}
	if got := mustGet(t, svc, site.ID); got.PHPConfigHash != applied.PHPConfigHash || got.FastCGIPort != applied.FastCGIPort {
		t.Fatalf("Start changed the applied row: %+v", got)
	}

	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if !fakes.backend.phpStarted(site.ID, phpDefaultGeneration) {
		t.Fatal("Start after Stop did not start the default generation")
	}
	if fakes.backend.startCount() != starts+1 {
		t.Fatalf("starts = %d, want %d", fakes.backend.startCount(), starts+1)
	}
	spec, ok := fakes.backend.phpSpec(site.ID, phpDefaultGeneration)
	if !ok || spec.PHPIniPath != filepath.Join(phpGenerationDir(t, fakes, applied), "php.ini") {
		t.Fatalf("restarted master spec = %+v", spec)
	}
}

func TestPHPInfoReadsCurrentGeneration(t *testing.T) {
	svc, fakes := newTestService(t)
	build := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
		PHPVersion: "8.4", PHPBuild: build.ID,
		PHPIni:        map[string]string{"memory_limit": "512M"},
		PHPIniRaw:     "custom.flag = on\n",
		PHPExtensions: map[string]bool{"redis": true}})

	info, err := svc.PHPInfo(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Site.ID != site.ID || info.Build.ID != build.ID {
		t.Fatalf("info = %+v", info)
	}
	if info.Defaults.Version != "8.4" {
		t.Fatalf("defaults = %+v", info.Defaults)
	}
	if info.Ini["memory_limit"] != "512M" || info.IniRaw != "custom.flag = on\n" {
		t.Fatalf("site inputs = %v / %q", info.Ini, info.IniRaw)
	}
	names := extensionNames(info.Extensions)
	if !containsAll(names, "redis", "igbinary") {
		t.Fatalf("resolved extensions = %v", names)
	}
	if len(info.Builtins) == 0 {
		t.Fatal("builtins missing")
	}
	available := make([]string, 0, len(info.AvailableExtensions))
	for _, extension := range info.AvailableExtensions {
		if !containsString(extension.Versions, "8.4") {
			t.Fatalf("extension %q is not available on 8.4", extension.Name)
		}
		available = append(available, extension.Name)
	}
	if !sort.StringsAreSorted(available) {
		t.Fatalf("available extensions are not sorted: %v", available)
	}
	generation := phpGenerationDir(t, fakes, site)
	if info.GeneratedIni != string(readFile(t, filepath.Join(generation, "php.ini"))) {
		t.Fatal("generated ini does not match the current generation")
	}
	if info.FPMConfig != string(readFile(t, filepath.Join(generation, "php-fpm.conf"))) {
		t.Fatal("fpm config does not match the current generation")
	}
	if info.ConfigHash != site.PHPConfigHash {
		t.Fatalf("hash = %q, want %q", info.ConfigHash, site.PHPConfigHash)
	}
}

func TestPHPInfo85TreatsOPcacheAsBuiltin(t *testing.T) {
	svc, fakes := newTestService(t)
	build := fakes.installPHPBuild("php@8.5.0-r1", "8.5.0")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "api", Path: fakes.root, Kind: "php",
		PHPVersion: "8.5", PHPBuild: build.ID})

	info, err := svc.PHPInfo(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(info.GeneratedIni, "opcache.so") {
		t.Fatalf("8.5 default ini references the removed shared opcache:\n%s", info.GeneratedIni)
	}
	if !containsString(info.Builtins, "opcache") {
		t.Fatalf("8.5 built-ins = %v, want opcache", info.Builtins)
	}
	for _, extension := range info.AvailableExtensions {
		if extension.Name == "opcache" {
			t.Fatalf("8.5 available extensions still include opcache: %+v", extension)
		}
	}
	for _, extension := range info.Extensions {
		if extension.Name == "opcache" {
			t.Fatalf("8.5 effective extensions still include opcache: %+v", extension)
		}
	}
}

func TestApplyPHP85DropsBuiltinExtensionOverrides(t *testing.T) {
	svc, fakes := newTestService(t)
	r1 := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.installPHPBuild("php@8.5.0-r1", "8.5.0")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
		PHPVersion: "8.4", PHPBuild: r1.ID,
		PHPExtensions: map[string]bool{"opcache": true, "xdebug": true}})

	updated, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Version: "8.5"})
	if err != nil {
		t.Fatalf("8.4->8.5 apply with an opcache override: %v", err)
	}
	if _, ok := updated.PHPExtensions["opcache"]; ok {
		t.Fatalf("row kept the built-in opcache override: %v", updated.PHPExtensions)
	}
	if !updated.PHPExtensions["xdebug"] {
		t.Fatalf("row lost the xdebug override: %v", updated.PHPExtensions)
	}
	ini := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, updated), "php.ini")))
	if strings.Contains(ini, "opcache.so") || strings.Contains(ini, "zend_extension=opcache") {
		t.Fatalf("generated 8.5 ini still loads shared opcache:\n%s", ini)
	}
	info, err := svc.PHPInfo(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := info.Site.PHPExtensions["opcache"]; ok {
		t.Fatalf("PHPInfo exposes the stale opcache override: %v", info.Site.PHPExtensions)
	}
}

func TestPHP85PoisonedDefaultsRowIsSanitized(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.5.0-r1", "8.5.0")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "api", Path: fakes.root, Kind: "php",
		PHPVersion: "8.5", PHPBuild: "php@8.5.0-r1"})

	// Simulate a row written before opcache became a per-version built-in.
	if err := fakes.store.SavePHPDefaults(ctx, model.PHPDefaults{
		Version:    "8.5",
		Ini:        map[string]string{"memory_limit": "256M"},
		Extensions: map[string]bool{"opcache": true, "xdebug": true},
	}); err != nil {
		t.Fatal(err)
	}
	info, err := svc.PHPInfo(ctx, site.ID)
	if err != nil {
		t.Fatalf("PHPInfo with a poisoned defaults row: %v", err)
	}
	if _, ok := info.Defaults.Extensions["opcache"]; ok {
		t.Fatalf("PHPInfo kept the built-in opcache default: %v", info.Defaults.Extensions)
	}
	if !info.Defaults.Extensions["xdebug"] {
		t.Fatalf("sanitization dropped a valid default: %v", info.Defaults.Extensions)
	}
	stored, err := fakes.store.GetPHPDefaults(ctx, "8.5")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stored.Extensions["opcache"]; ok {
		t.Fatalf("stored defaults were not healed: %v", stored.Extensions)
	}
	if strings.Contains(info.GeneratedIni, "opcache.so") {
		t.Fatalf("8.5 generated ini loads shared opcache:\n%s", info.GeneratedIni)
	}
}

func TestPHPInfoComposesInMemoryWhenFilesAreMissing(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	generation := phpGenerationDir(t, fakes, site)
	if err := os.RemoveAll(generation); err != nil {
		t.Fatal(err)
	}

	info, err := svc.PHPInfo(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generation); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("PHPInfo wrote the generation: %v", err)
	}
	if info.GeneratedIni == "" || !strings.Contains(info.GeneratedIni, "memory_limit") {
		t.Fatalf("generated ini = %q", info.GeneratedIni)
	}
	if !strings.Contains(info.FPMConfig, "listen = 127.0.0.1:"+strconv.Itoa(site.FastCGIPort)) {
		t.Fatalf("fpm config = %q", info.FPMConfig)
	}
	if info.ConfigHash != site.PHPConfigHash {
		t.Fatalf("hash = %q, want %q", info.ConfigHash, site.PHPConfigHash)
	}
}

func TestPHPInfoUsesATemporaryPortWhenStoppedWithoutOne(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldPort := stored.FastCGIPort
	stored.FastCGIPort = 0
	if err := fakes.store.UpdateSite(ctx, stored); err != nil {
		t.Fatal(err)
	}
	fakes.ports.Release(oldPort)

	info, err := svc.PHPInfo(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.GeneratedIni == "" || !strings.Contains(info.FPMConfig, "listen = 127.0.0.1:") {
		t.Fatalf("info was not rendered: %+v", info)
	}
	after, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.FastCGIPort != 0 {
		t.Fatalf("PHPInfo persisted a port: %d", after.FastCGIPort)
	}
	if got := reservedFastCGIPorts(t, fakes); len(got) != 0 {
		t.Fatalf("PHPInfo leaked a FastCGI port: %v", got)
	}
}

func TestPHPDefaultsSeedsAndPartialPayloadFillsBaseline(t *testing.T) {
	svc, _ := newTestService(t)
	defaults, err := svc.PHPDefaults(ctx, "8.4")
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Ini["memory_limit"] != "256M" || defaults.Extensions["xdebug"] != false {
		t.Fatalf("seeded defaults = %+v", defaults)
	}
	if _, err := svc.deps.Store.GetPHPDefaults(ctx, "8.4"); err != nil {
		t.Fatalf("defaults were not seeded in the store: %v", err)
	}

	result, err := svc.SavePHPDefaults(ctx, model.PHPDefaults{
		Version:    "8.4",
		Ini:        map[string]string{"memory_limit": "512M"},
		Extensions: map[string]bool{"xdebug": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Defaults.Ini["memory_limit"] != "512M" {
		t.Fatalf("explicit ini not applied: %+v", result.Defaults.Ini)
	}
	if result.Defaults.Ini["display_errors"] != "On" {
		t.Fatal("baseline ini entries were erased by a partial payload")
	}
	if !result.Defaults.Extensions["xdebug"] {
		t.Fatal("explicit xdebug=true was not applied")
	}
	if _, ok := result.Defaults.Extensions["redis"]; !ok {
		t.Fatal("baseline extension entries were erased by a partial payload")
	}
	if len(result.Sites) != 0 {
		t.Fatalf("sites = %+v", result.Sites)
	}
	stored, err := svc.deps.Store.GetPHPDefaults(ctx, "8.4")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Ini["memory_limit"] != "512M" || !stored.Extensions["xdebug"] {
		t.Fatalf("stored defaults = %+v", stored)
	}

	if _, err := svc.SavePHPDefaults(ctx, model.PHPDefaults{Version: "7.4"}); err == nil {
		t.Fatal("unsupported version accepted")
	}
	if _, err := svc.SavePHPDefaults(ctx, model.PHPDefaults{
		Version:    "8.4",
		Extensions: map[string]bool{"not_an_extension": true},
	}); err == nil {
		t.Fatal("unknown extension accepted")
	}
	if _, err := svc.SavePHPDefaults(ctx, model.PHPDefaults{
		Version: "8.4",
		Ini:     map[string]string{"memory_limit": "1G\nbad = value"},
	}); !errors.Is(err, phpini.ErrInvalidConfig) {
		t.Fatalf("invalid ini err = %v, want ErrInvalidConfig", err)
	}
}

func TestPHPDefaultsExplicitFalseSurvivesPartialPayload(t *testing.T) {
	svc, _ := newTestService(t)
	result, err := svc.SavePHPDefaults(ctx, model.PHPDefaults{
		Version:    "8.4",
		Extensions: map[string]bool{"xdebug": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Defaults.Extensions["xdebug"] {
		t.Fatal("explicit false extension was replaced by the manifest default")
	}
	if !result.Defaults.Extensions["opcache"] {
		t.Fatal("manifest defaults were not filled")
	}
}

func TestSavePHPDefaultsRollsBackPerSite(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.installPHPBuild("php@8.5.1-r1", "8.5.1")
	blog := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	shop := mustCreate(t, svc, fakes, CreateRequest{Name: "shop", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	parked := mustCreate(t, svc, fakes, CreateRequest{Name: "parked", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	if err := svc.Stop(ctx, parked.ID); err != nil {
		t.Fatal(err)
	}
	api := mustCreate(t, svc, fakes, CreateRequest{Name: "api", Path: fakes.root, Kind: "php", PHPVersion: "8.5"})
	blogHash, shopHash := blog.PHPConfigHash, shop.PHPConfigHash
	parkedHash, apiHash := parked.PHPConfigHash, api.PHPConfigHash
	fakes.php.fpmFunc = func(_ context.Context, _, configPath, _ string) error {
		if strings.Contains(configPath, string(filepath.Separator)+shop.ID+string(filepath.Separator)) {
			return errors.New("shop rejected the defaults")
		}
		return nil
	}

	result, err := svc.SavePHPDefaults(ctx, model.PHPDefaults{
		Version: "8.4",
		Ini:     map[string]string{"memory_limit": "512M"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sites) != 2 {
		t.Fatalf("results = %+v", result.Sites)
	}
	bySite := migrationResultsByName(result.Sites)
	if got := bySite[blog.Name]; !got.OK {
		t.Fatalf("blog result = %+v", got)
	}
	if got := bySite[shop.Name]; got.OK || !strings.Contains(got.Error, "shop rejected") {
		t.Fatalf("shop result = %+v", got)
	}
	storedBlog, err := fakes.store.GetSite(ctx, blog.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedBlog.PHPConfigHash == blogHash {
		t.Fatal("successful site kept the old defaults")
	}
	blogIni := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, storedBlog), "php.ini")))
	if !strings.Contains(blogIni, "memory_limit = 512M") {
		t.Fatalf("blog ini lacks the new default:\n%s", blogIni)
	}
	storedShop, err := fakes.store.GetSite(ctx, shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedShop.PHPConfigHash != shopHash {
		t.Fatalf("failed site changed its hash: %q -> %q", shopHash, storedShop.PHPConfigHash)
	}
	if active := fakes.backend.phpActiveGeneration(shop.ID); active != phpDefaultGeneration {
		t.Fatalf("failed site active generation = %q", active)
	}
	storedParked, err := fakes.store.GetSite(ctx, parked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedParked.PHPConfigHash != parkedHash {
		t.Fatalf("stopped site was re-applied: %q -> %q", parkedHash, storedParked.PHPConfigHash)
	}
	storedAPI, err := fakes.store.GetSite(ctx, api.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedAPI.PHPConfigHash != apiHash {
		t.Fatalf("site on another line was re-applied: %q -> %q", apiHash, storedAPI.PHPConfigHash)
	}
	storedDefaults, err := fakes.store.GetPHPDefaults(ctx, "8.4")
	if err != nil {
		t.Fatal(err)
	}
	if storedDefaults.Ini["memory_limit"] != "512M" {
		t.Fatalf("defaults were not saved: %+v", storedDefaults)
	}

	fakes.php.fpmFunc = nil
	blogHash, shopHash = storedBlog.PHPConfigHash, storedShop.PHPConfigHash
	result, err = svc.SavePHPDefaults(ctx, model.PHPDefaults{
		Version:    "8.4",
		Extensions: map[string]bool{"xdebug": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, siteResult := range result.Sites {
		if !siteResult.OK {
			t.Fatalf("second defaults apply failed: %+v", siteResult)
		}
	}
	for _, site := range []model.Site{storedBlog, storedShop} {
		current, err := fakes.store.GetSite(ctx, site.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.PHPConfigHash == "" {
			t.Fatalf("site %q lost its hash", site.Name)
		}
		if site.ID == blog.ID && current.PHPConfigHash == blogHash {
			t.Fatalf("blog hash did not change for xdebug")
		}
		if site.ID == shop.ID && current.PHPConfigHash == shopHash {
			t.Fatalf("shop hash did not change for xdebug")
		}
		ini := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, current), "php.ini")))
		if !strings.Contains(ini, "zend_extension=xdebug.so") {
			t.Fatalf("site %q does not load xdebug:\n%s", site.Name, ini)
		}
	}
}

func TestMigratePHPReturnsPerSiteResults(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	r2 := fakes.installPHPBuild("php@8.5.1-r1", "8.5.1")
	blog := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	shop := mustCreate(t, svc, fakes, CreateRequest{Name: "shop", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	api := mustCreate(t, svc, fakes, CreateRequest{Name: "api", Path: fakes.root, Kind: "php", PHPVersion: "8.5"})
	apiHash := api.PHPConfigHash

	results, err := svc.MigratePHP(ctx, PHPMigrationRequest{FromVersion: "8.4", ToVersion: "8.5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	for _, result := range results {
		if !result.OK {
			t.Fatalf("migration failed: %+v", result)
		}
	}
	for _, site := range []model.Site{blog, shop} {
		stored, err := fakes.store.GetSite(ctx, site.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.PHPVersion != "8.5" || stored.PHPBuild != r2.ID {
			t.Fatalf("site %q = %+v", site.Name, stored)
		}
	}
	storedAPI, err := fakes.store.GetSite(ctx, api.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedAPI.PHPConfigHash != apiHash {
		t.Fatal("migration touched a site on another line")
	}

	results, err = svc.MigratePHP(ctx, PHPMigrationRequest{FromVersion: "8.4", ToBuild: r2.ID})
	if err != nil {
		t.Fatalf("build-only migration: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("build-only migration results = %+v, want no 8.4 sites left", results)
	}
	if _, err := svc.MigratePHP(ctx, PHPMigrationRequest{FromVersion: "8.4"}); err == nil {
		t.Fatal("migration without a target succeeded")
	}
	if _, err := svc.MigratePHP(ctx, PHPMigrationRequest{FromVersion: "8.4", ToBuild: "php@8.5.9-r9"}); !errors.Is(err, ErrRuntimeMissing) {
		t.Fatalf("unknown target err = %v, want ErrRuntimeMissing", err)
	}
}

func TestMigratePHPReportsFailuresIndependently(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.installPHPBuild("php@8.5.1-r1", "8.5.1")
	blog := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	shop := mustCreate(t, svc, fakes, CreateRequest{Name: "shop", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	blogHash, shopHash := blog.PHPConfigHash, shop.PHPConfigHash
	fakes.backend.startErr = errors.New("health timeout")

	results, err := svc.MigratePHP(ctx, PHPMigrationRequest{FromVersion: "8.4", ToVersion: "8.5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	for _, result := range results {
		if result.OK || !strings.Contains(result.Error, "health timeout") {
			t.Fatalf("result = %+v", result)
		}
	}
	for _, site := range []model.Site{blog, shop} {
		stored, err := fakes.store.GetSite(ctx, site.ID)
		if err != nil {
			t.Fatal(err)
		}
		wantHash := blogHash
		if site.ID == shop.ID {
			wantHash = shopHash
		}
		if stored.PHPVersion != "8.4" || stored.PHPConfigHash != wantHash {
			t.Fatalf("failed migration changed %q: %+v", site.Name, stored)
		}
	}
}

func TestCheckRuntimeRemovalBlocksPinnedAndLegacySites(t *testing.T) {
	svc, fakes := newTestService(t)
	r1 := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.installPHPBuild("php@8.5.1-r1", "8.5.1")
	mustCreate(t, svc, fakes, CreateRequest{Name: "zulu", Path: fakes.root, Kind: "php", PHPVersion: "8.4", PHPBuild: r1.ID})
	mustCreate(t, svc, fakes, CreateRequest{Name: "alpha", Path: fakes.root, Kind: "php", PHPVersion: "8.4", PHPBuild: r1.ID})
	legacy := mustCreate(t, svc, fakes, CreateRequest{Name: "legacy", Path: fakes.root, Kind: "php", PHPVersion: "8.4", PHPBuild: r1.ID})
	storedLegacy, err := fakes.store.GetSite(ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedLegacy.PHPBuild = ""
	if err := fakes.store.UpdateSite(ctx, storedLegacy); err != nil {
		t.Fatal(err)
	}

	err = svc.CheckRuntimeRemoval(ctx, r1.ID)
	if !errors.Is(err, ErrRuntimeInUse) {
		t.Fatalf("err = %v, want ErrRuntimeInUse", err)
	}
	var inUse *RuntimeInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("err = %T, want *RuntimeInUseError", err)
	}
	want := []string{"alpha", "legacy", "zulu"}
	if !reflect.DeepEqual(inUse.Sites, want) {
		t.Fatalf("sites = %v, want %v", inUse.Sites, want)
	}
	if inUse.BuildID != r1.ID {
		t.Fatalf("build = %q", inUse.BuildID)
	}
	if !errors.As(fmt.Errorf("wrapped: %w", err), &inUse) {
		t.Fatal("wrapped RuntimeInUseError is not discoverable")
	}
	if err := svc.CheckRuntimeRemoval(ctx, "php@8.4.9-r9"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("absent build err = %v, want store.ErrNotFound", err)
	}

	results, err := svc.MigratePHP(ctx, PHPMigrationRequest{FromVersion: "8.4", ToVersion: "8.5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	for _, result := range results {
		if !result.OK {
			t.Fatalf("migration failed: %+v", result)
		}
	}
	if err := svc.CheckRuntimeRemoval(ctx, r1.ID); err != nil {
		t.Fatalf("runtime still blocked after migration: %v", err)
	}
}

func TestApplyPHPRejectsMismatchedBuildAndUnsupportedVersion(t *testing.T) {
	svc, fakes := newTestService(t)
	r1 := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	fakes.installPHPBuild("php@8.5.1-r1", "8.5.1")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4", PHPBuild: r1.ID})

	if _, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Version: "8.5", Build: r1.ID}); !errors.Is(err, ErrRuntimeMissing) {
		t.Fatalf("mismatched build err = %v, want ErrRuntimeMissing", err)
	}
	if _, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Version: "7.4"}); err == nil {
		t.Fatal("unsupported version accepted")
	}
	if _, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Build: "php@8.4.9-r9"}); !errors.Is(err, ErrRuntimeMissing) {
		t.Fatalf("absent build err = %v, want ErrRuntimeMissing", err)
	}
	if got := mustGet(t, svc, site.ID); got.PHPBuild != r1.ID || got.PHPConfigHash != site.PHPConfigHash {
		t.Fatalf("failed applies changed the row: %+v", got)
	}
}

func TestApplyPHPClearsExplicitOverrides(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4",
		PHPIni: map[string]string{"memory_limit": "512M"}, PHPExtensions: map[string]bool{"xdebug": true}})

	updated, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Extensions: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.PHPExtensions) != 0 {
		t.Fatalf("extensions = %v, want cleared", updated.PHPExtensions)
	}
	if updated.PHPIni["memory_limit"] != "512M" {
		t.Fatalf("nil ini cleared the site overrides: %v", updated.PHPIni)
	}
	ini := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, updated), "php.ini")))
	if strings.Contains(ini, "xdebug.so") {
		t.Fatalf("xdebug remained enabled:\n%s", ini)
	}

	updated, err = svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.PHPIni) != 0 {
		t.Fatalf("ini = %v, want cleared", updated.PHPIni)
	}
	ini = string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, updated), "php.ini")))
	if !strings.Contains(ini, "memory_limit = 256M") {
		t.Fatalf("default memory limit was not restored:\n%s", ini)
	}
}

func TestConcurrentApplySerializes(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	changes := []PHPChange{
		{Ini: map[string]string{"memory_limit": "512M"}},
		{Ini: map[string]string{"memory_limit": "1G"}},
	}

	var wg sync.WaitGroup
	errs := make([]error, len(changes))
	for index, change := range changes {
		wg.Add(1)
		go func(index int, change PHPChange) {
			defer wg.Done()
			_, errs[index] = svc.ApplyPHP(ctx, site.ID, change)
		}(index, change)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent apply: %v", err)
		}
	}

	final, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.PHPIni["memory_limit"] != "512M" && final.PHPIni["memory_limit"] != "1G" {
		t.Fatalf("final ini = %v", final.PHPIni)
	}
	if active := fakes.backend.phpActiveGeneration(site.ID); active != final.PHPConfigHash {
		t.Fatalf("active generation = %q, final hash = %q", active, final.PHPConfigHash)
	}
	if got := reservedFastCGIPorts(t, fakes); !reflect.DeepEqual(got, []int{final.FastCGIPort}) {
		t.Fatalf("reserved ports = %v, want [%d]", got, final.FastCGIPort)
	}
	if got := phpGenerationDirs(t, fakes, site.ID); len(got) != 2 {
		t.Fatalf("generations = %v, want current and previous", got)
	}
	if fakes.backend.startCount() != 3 {
		t.Fatalf("starts = %d, want 3 (create plus two applies)", fakes.backend.startCount())
	}
	prepares := 0
	for _, event := range fakes.backend.phpEventList() {
		if event.Op == "prepare" && event.SiteID == site.ID && event.Generation == final.PHPConfigHash {
			prepares++
		}
	}
	if prepares != 1 {
		t.Fatalf("final generation prepared %d times, want 1", prepares)
	}
}

func TestStoppedPHPStartAndExecPickUpEditedDefaults(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	oldHash := site.PHPConfigHash
	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.SavePHPDefaults(ctx, model.PHPDefaults{
		Version:    "8.4",
		Ini:        map[string]string{"memory_limit": "640M"},
		Extensions: map[string]bool{"xdebug": true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	started, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.PHPConfigHash == oldHash {
		t.Fatal("Start reused the stale generation after edited defaults")
	}
	ini := string(readFile(t, filepath.Join(phpGenerationDir(t, fakes, started), "php.ini")))
	if !strings.Contains(ini, "memory_limit = 640M") || !strings.Contains(ini, "zend_extension=xdebug.so") {
		t.Fatalf("started generation does not reflect edited defaults:\n%s", ini)
	}
	spec, ok := fakes.backend.phpSpec(site.ID, phpDefaultGeneration)
	if !ok || spec.PHPIniPath != filepath.Join(phpGenerationDir(t, fakes, started), "php.ini") {
		t.Fatalf("started spec = %+v", spec)
	}
	info, err := svc.PHPInfo(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info.GeneratedIni, "memory_limit = 640M") {
		t.Fatalf("PHPInfo uses a stale generation:\n%s", info.GeneratedIni)
	}

	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SavePHPDefaults(ctx, model.PHPDefaults{
		Version: "8.4",
		Ini:     map[string]string{"memory_limit": "768M"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PHPExec(ctx, site.ID, []string{"-r", "echo 1;"}); err != nil {
		t.Fatal(err)
	}
	exec, ok := fakes.backend.lastExec()
	if !ok || len(exec.Argv) < 3 {
		t.Fatalf("exec argv = %v", exec.Argv)
	}
	execIni := string(readFile(t, exec.Argv[2]))
	if !strings.Contains(execIni, "memory_limit = 768M") {
		t.Fatalf("PHPExec used a stale php.ini:\n%s", execIni)
	}
}

func TestApplyPHPCanceledContextStopsTheStagedMaster(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	oldHash, oldPort := site.PHPConfigHash, site.FastCGIPort

	applyCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel after the new master starts, so the row write fails and the
	// rollback stop would see an already-canceled context.
	fakes.backend.phpStartHook = cancel

	_, err := svc.ApplyPHP(applyCtx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err == nil {
		t.Fatal("canceled apply succeeded")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	stagedHash, stagedPort := lastStagedApply(t, fakes, site.ID)
	if fakes.backend.phpStarted(site.ID, stagedHash) {
		t.Fatal("staged master still runs after a canceled apply")
	}
	if _, statErr := os.Stat(filepath.Join(fakes.config.Home, "sites", site.ID, stagedHash)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("staged generation directory was not removed: %v", statErr)
	}
	if reserveErr := fakes.ports.Reserve(stagedPort); reserveErr != nil {
		t.Fatalf("staged FastCGI port %d was not released: %v", stagedPort, reserveErr)
	}
	fakes.ports.Release(stagedPort)
	if !fakes.backend.phpStarted(site.ID, phpDefaultGeneration) {
		t.Fatal("old master stopped during rollback")
	}
	stored, getErr := fakes.store.GetSite(ctx, site.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.PHPConfigHash != oldHash || stored.FastCGIPort != oldPort {
		t.Fatalf("row changed after canceled apply: %+v", stored)
	}
}

func TestApplyPHPRollbackRetainsGenerationWhenStopFails(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	// Force a publish failure after the new master is live, then make the
	// rollback stop fail as well.
	fakes.certs.err = errors.New("route publish failed")
	fakes.backend.stopPHPErr = errors.New("stop failed")

	_, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err == nil {
		t.Fatal("apply succeeded")
	}
	for _, want := range []string{"route publish failed", "stop failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
	stagedHash, stagedPort := lastStagedApply(t, fakes, site.ID)
	if !fakes.backend.phpStarted(site.ID, stagedHash) {
		t.Fatal("fake stop failure did not leave the staged master running")
	}
	if _, statErr := os.Stat(filepath.Join(fakes.config.Home, "sites", site.ID, stagedHash)); statErr != nil {
		t.Fatalf("generation removed after a failed stop: %v", statErr)
	}
	if reserveErr := fakes.ports.Reserve(stagedPort); reserveErr == nil {
		fakes.ports.Release(stagedPort)
		t.Fatalf("FastCGI port %d was released after a failed stop", stagedPort)
	}

	fakes.backend.stopPHPErr = nil
	if stopErr := fakes.backend.StopPHP(ctx, site.ID, stagedHash); stopErr != nil {
		t.Fatalf("cleanup staged master: %v", stopErr)
	}
	fakes.ports.Release(stagedPort)
}

func TestApplyPHPFailedDrainSkipsPruning(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"})
	liveHash, livePort := site.PHPConfigHash, site.FastCGIPort

	// A document-root update publishes a new row hash while the default master
	// keeps serving the generation it started with. The next apply drains
	// "default"; if that stop fails, pruning must not delete the live
	// generation just because it is neither the current nor previous row hash.
	publicTwo := filepath.Join(fakes.root, "public2")
	if err := os.MkdirAll(publicTwo, 0o755); err != nil {
		t.Fatal(err)
	}
	updated, err := svc.Update(ctx, site.ID, UpdateRequest{DocumentRoot: "public2"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PHPConfigHash == liveHash {
		t.Fatal("document-root update did not publish a new hash")
	}

	fakes.backend.phpStopErrs[phpFakeKey(site.ID, phpDefaultGeneration)] = errors.New("drain stop failed")
	applied, err := svc.ApplyPHP(ctx, site.ID, PHPChange{Ini: map[string]string{"memory_limit": "512M"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(fakes.config.Home, "sites", site.ID, liveHash)); statErr != nil {
		t.Fatalf("live default generation was pruned after a failed drain: %v", statErr)
	}
	if reserveErr := fakes.ports.Reserve(livePort); reserveErr == nil {
		fakes.ports.Release(livePort)
		t.Fatalf("old FastCGI port %d was released despite a failed drain", livePort)
	}
	if applied.FastCGIPort == livePort {
		t.Fatalf("apply did not allocate a new FastCGI port: %d", applied.FastCGIPort)
	}
	if !fakes.backend.phpStarted(site.ID, phpDefaultGeneration) {
		t.Fatal("fake drain failure should leave the default generation running")
	}
}

func lastStagedApply(t *testing.T, fakes *testFakes, siteID string) (string, int) {
	t.Helper()
	hash, port := "", 0
	for _, event := range fakes.backend.phpEventList() {
		if event.Op != "prepare" || event.SiteID != siteID || event.Generation == phpDefaultGeneration {
			continue
		}
		hash, port = event.Generation, event.Port
	}
	if hash == "" {
		t.Fatal("no staged apply generation was prepared")
	}
	return hash, port
}

func phpEventIndex(events []fakePHPOperation, op, siteID, generation string) int {
	for index, event := range events {
		if event.Op == op && event.SiteID == siteID && event.Generation == generation {
			return index
		}
	}
	return -1
}

func migrationResultsByName(results []PHPMigrationResult) map[string]PHPMigrationResult {
	byName := make(map[string]PHPMigrationResult, len(results))
	for _, result := range results {
		byName[result.Site] = result
	}
	return byName
}

func extensionNames(extensions []catalog.ResolvedExtension) []string {
	names := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		names = append(names, extension.Name)
	}
	return names
}

func containsAll(values []string, wants ...string) bool {
	for _, want := range wants {
		if !containsString(values, want) {
			return false
		}
	}
	return true
}
