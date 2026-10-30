package sites

import (
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
)

func TestCreateRequestCarriesOptionalDatabase(t *testing.T) {
	req := CreateRequest{
		Name:     "blog",
		Database: &databases.SiteDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"},
	}
	if req.Database == nil || req.Database.ServiceID != "mysql" {
		t.Fatalf("database = %+v", req.Database)
	}
}
