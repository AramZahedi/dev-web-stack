//go:build !windows

package helper_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func TestServiceRotateCredentialsEndpoint(t *testing.T) {
	var got string
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID: os.Getuid(),
		Service: helper.Service{
			ServiceRotateCredentials: func(_ context.Context, id string) error {
				got = id
				return nil
			},
		},
	})
	client := dialClient(t, socketPath)

	if err := client.ServiceRotateCredentials(context.Background(), "mysql"); err != nil {
		t.Fatalf("ServiceRotateCredentials() = %v", err)
	}
	if got != "mysql" {
		t.Fatalf("helper received id %q, want mysql", got)
	}

	if err := client.ServiceRotateCredentials(context.Background(), "../mysql"); !errors.Is(err, helperproto.ErrInvalidRequest) {
		t.Fatalf("invalid id error = %v, want ErrInvalidRequest", err)
	}
}
