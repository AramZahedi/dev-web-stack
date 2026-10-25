package services

import (
	"strings"
	"testing"
	"time"
)

const (
	nginxImageDigest  = "nginx:1.30.6@sha256:a7675fcee220f59b4a99f7098c06a0d5d999c2c0ee37c0211efb780536e5cb20"
	apacheImageDigest = "httpd:2.4.69@sha256:a30cd4cd57f3f3c3be9b2d16384d2488839996bb14c67df2b51f1ecbb3e584a1"
)

func TestWebServicesPinnedImagesAndShape(t *testing.T) {
	services := webServices()
	if len(services) != 2 {
		t.Fatalf("webServices() length = %d, want 2", len(services))
	}

	byID := map[ID]Service{}
	for _, svc := range services {
		byID[svc.ID] = svc
	}
	if svc, ok := byID[IDNginx]; !ok || svc.Image != nginxImageDigest {
		t.Fatalf("nginx image = %q, want %q", byID[IDNginx].Image, nginxImageDigest)
	}
	if svc, ok := byID[IDApache]; !ok || svc.Image != apacheImageDigest {
		t.Fatalf("apache image = %q, want %q", byID[IDApache].Image, apacheImageDigest)
	}

	for _, svc := range services {
		if svc.Group != GroupWeb {
			t.Errorf("%s group = %q, want web", svc.ID, svc.Group)
		}
		if svc.NetworkMode != "host" {
			t.Errorf("%s network mode = %q, want host", svc.ID, svc.NetworkMode)
		}
		if len(svc.Ports) != 2 || svc.Ports[0].Container != 80 || svc.Ports[1].Container != 443 {
			t.Errorf("%s ports = %+v, want status ports 80 and 443", svc.ID, svc.Ports)
		}
		if svc.Healthcheck == nil || len(svc.Healthcheck.Test) == 0 {
			t.Errorf("%s has no healthcheck", svc.ID)
		}
		if len(svc.ValidateCommand) == 0 || len(svc.ReloadCommand) == 0 {
			t.Errorf("%s missing validate/reload commands", svc.ID)
		}
	}
}

func TestWebServicesExactCommandsAndMounts(t *testing.T) {
	byID := map[ID]Service{}
	for _, svc := range webServices() {
		byID[svc.ID] = svc
	}

	nginx := byID[IDNginx]
	if got := strings.Join(nginx.ValidateCommand, " "); got != "nginx -t" {
		t.Errorf("nginx validate command = %q", got)
	}
	if got := strings.Join(nginx.ReloadCommand, " "); got != "nginx -s reload" {
		t.Errorf("nginx reload command = %q", got)
	}
	if mount, ok := findMount(nginx, "/etc/nginx/nginx.conf"); !ok || !mount.ReadOnly || mount.HostSubdir != "config/nginx.conf" || mount.Area != MountService {
		t.Errorf("nginx nginx.conf mount = %+v, ok=%v", mount, ok)
	}
	if mount, ok := findMount(nginx, "/etc/nginx/conf.d"); !ok || !mount.ReadOnly || mount.HostSubdir != "config/conf.d" || mount.Area != MountService {
		t.Errorf("nginx conf.d mount = %+v, ok=%v", mount, ok)
	}
	if mount, ok := findMount(nginx, "/var/log/nginx"); !ok || mount.ReadOnly || mount.HostSubdir != "logs" {
		t.Errorf("nginx logs mount = %+v, ok=%v", mount, ok)
	}
	if mount, ok := findMount(nginx, "/sites"); !ok || !mount.ReadOnly || mount.Area != MountProjects {
		t.Errorf("nginx sites mount = %+v, ok=%v", mount, ok)
	}
	if mount, ok := findMount(nginx, "/certs"); !ok || !mount.ReadOnly || mount.Area != MountCerts {
		t.Errorf("nginx certs mount = %+v, ok=%v", mount, ok)
	}
	if nginx.Healthcheck == nil || strings.Join(nginx.Healthcheck.Test, " ") != "CMD-SHELL nginx -t" {
		t.Errorf("nginx healthcheck = %+v", nginx.Healthcheck)
	}
	if nginx.Healthcheck != nil {
		if nginx.Healthcheck.Interval != 30*time.Second || nginx.Healthcheck.Timeout != 5*time.Second || nginx.Healthcheck.Retries != 3 {
			t.Errorf("nginx healthcheck timing = %+v", nginx.Healthcheck)
		}
	}

	apache := byID[IDApache]
	if got := strings.Join(apache.ValidateCommand, " "); got != "apachectl configtest" {
		t.Errorf("apache validate command = %q", got)
	}
	if got := strings.Join(apache.ReloadCommand, " "); got != "apachectl graceful" {
		t.Errorf("apache reload command = %q", got)
	}
	if mount, ok := findMount(apache, "/usr/local/apache2/conf/httpd.conf"); !ok || !mount.ReadOnly || mount.HostSubdir != "config/httpd.conf" || mount.Area != MountService {
		t.Errorf("apache httpd.conf mount = %+v, ok=%v", mount, ok)
	}
	if mount, ok := findMount(apache, "/usr/local/apache2/conf/conf.d"); !ok || !mount.ReadOnly || mount.HostSubdir != "config/conf.d" || mount.Area != MountService {
		t.Errorf("apache conf.d mount = %+v, ok=%v", mount, ok)
	}
	if mount, ok := findMount(apache, "/usr/local/apache2/logs"); !ok || mount.ReadOnly || mount.HostSubdir != "logs" {
		t.Errorf("apache logs mount = %+v, ok=%v", mount, ok)
	}
	if apache.Healthcheck == nil || strings.Join(apache.Healthcheck.Test, " ") != "CMD-SHELL apachectl configtest" {
		t.Errorf("apache healthcheck = %+v", apache.Healthcheck)
	}
}

func findMount(svc Service, containerPath string) (Mount, bool) {
	for _, mount := range append(append([]Mount{}, svc.Config...), svc.Data...) {
		if mount.ContainerPath == containerPath {
			return mount, true
		}
	}
	return Mount{}, false
}

func TestDefaultCatalogIncludesWebServices(t *testing.T) {
	c := DefaultCatalog()
	if len(c.Services) != 2 {
		t.Fatalf("DefaultCatalog().Services length = %d, want 2", len(c.Services))
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("DefaultCatalog().Validate() err = %v", err)
	}
	if _, ok := c.Get(IDNginx); !ok {
		t.Fatal("catalog is missing nginx")
	}
	if _, ok := c.Get(IDApache); !ok {
		t.Fatal("catalog is missing apache")
	}
}

func TestRenderComposeWebServicesUsesHostNetworkAndNoPublishedPorts(t *testing.T) {
	data, err := RenderCompose(DefaultCatalog(), "/data/devstack")
	if err != nil {
		t.Fatalf("RenderCompose: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"network_mode: host",
		"/data/devstack/projects:/sites:ro",
		"/data/devstack/certs:/certs:ro",
		"/data/devstack/services/nginx/config/nginx.conf:/etc/nginx/nginx.conf:ro",
		"/data/devstack/services/apache/config/httpd.conf:/usr/local/apache2/conf/httpd.conf:ro",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("compose missing %q\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"127.0.0.1:80:80", "127.0.0.1:443:443", "/var/run/docker.sock"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("compose must not contain %q", unwanted)
		}
	}
}
