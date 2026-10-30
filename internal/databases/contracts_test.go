package databases

import "testing"

func TestSiteDatabaseRequestShape(t *testing.T) {
	req := SiteDatabaseRequest{
		ServiceID:   "mysql",
		NewDatabase: &CreateDatabaseRequest{Name: "app"},
		NewUser:     &CreateUserRequest{Username: "app"},
	}
	if req.ServiceID != "mysql" || req.NewDatabase.Name != "app" || req.NewUser.Username != "app" {
		t.Fatalf("request = %+v", req)
	}
}
