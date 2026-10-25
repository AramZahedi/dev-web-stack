package helper

import (
	"os"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestSecretStoreUsesControllerOwnerUID(t *testing.T) {
	controller := NewDockerServiceController(t.TempDir(), services.Catalog{}, nil)
	controller.OwnerUID = os.Getuid()

	store, ok := controller.secretStore().(*credentials.Store)
	if !ok {
		t.Fatalf("secretStore() type = %T, want *credentials.Store", controller.secretStore())
	}
	if store.OwnerUID != os.Getuid() {
		t.Fatalf("secret store OwnerUID = %d, want %d", store.OwnerUID, os.Getuid())
	}
}
