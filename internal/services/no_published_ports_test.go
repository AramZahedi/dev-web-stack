package services_test

import (
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestWithoutPublishedPortsKeepsMetadataButOmitsComposePorts(t *testing.T) {
	catalog := services.WithoutPublishedPorts(services.DefaultCatalog())
	mysql, ok := catalog.Get(services.IDMySQL)
	if !ok {
		t.Fatal("catalog missing mysql")
	}
	if len(mysql.Ports) == 0 {
		t.Fatal("WithoutPublishedPorts removed host-port metadata")
	}
	if !mysql.SuppressPublishedPorts {
		t.Fatal("WithoutPublishedPorts did not set the render flag")
	}
	data, err := services.RenderCompose(catalog, "/data/devstack")
	if err != nil {
		t.Fatalf("RenderCompose: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "127.0.0.1:3306:3306") || strings.Contains(text, "127.0.0.1:8082:80") {
		t.Fatalf("rendered Compose still publishes fixed service ports:\n%s", text)
	}
}
