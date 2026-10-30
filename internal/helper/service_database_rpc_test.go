//go:build !windows

package helper_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type fakeDatabasePlatform struct {
	databases.Platform

	mu             sync.Mutex
	listErr        error
	createErr      error
	jobErr         error
	databases      []model.Database
	created        databases.CreateDatabaseRequest
	env            map[string]string
	exportReader   io.ReadCloser
	downloadReader io.ReadCloser
}

func (f *fakeDatabasePlatform) ExportDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error) {
	if f.exportReader != nil {
		return f.exportReader, nil
	}
	return io.NopCloser(strings.NewReader("dump")), nil
}

func (f *fakeDatabasePlatform) DownloadBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error) {
	if f.downloadReader != nil {
		return f.downloadReader, nil
	}
	return io.NopCloser(strings.NewReader("backup")), nil
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("stream failed") }
func (failingReadCloser) Close() error             { return nil }

func (f *fakeDatabasePlatform) ListDatabases(ctx context.Context, serviceID string) ([]model.Database, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.databases, nil
}

func (f *fakeDatabasePlatform) CreateDatabase(ctx context.Context, serviceID string, req databases.CreateDatabaseRequest) (model.Database, error) {
	if f.createErr != nil {
		return model.Database{}, f.createErr
	}
	f.mu.Lock()
	f.created = req
	f.mu.Unlock()
	return model.Database{Name: req.Name, ServiceID: serviceID, Engine: model.DatabaseMySQL}, nil
}

func (f *fakeDatabasePlatform) GetJob(ctx context.Context, jobID string) (model.DatabaseJob, error) {
	if f.jobErr != nil {
		return model.DatabaseJob{}, f.jobErr
	}
	return model.DatabaseJob{ID: jobID, Status: model.JobSucceeded}, nil
}

func (f *fakeDatabasePlatform) ResolveSiteEnv(ctx context.Context, serviceID string, links []model.DatabaseLink) (map[string]string, error) {
	if f.env != nil {
		return f.env, nil
	}
	return map[string]string{"DB_HOST": "127.0.0.1"}, nil
}

func databaseRPCServer(t *testing.T, platform *fakeDatabasePlatform) *helperclient.Client {
	t.Helper()
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID: os.Getuid(),
		Service:  helper.Service{DatabasePlatform: platform},
	})
	return dialClient(t, socketPath)
}

func TestServiceDatabaseListEndpoint(t *testing.T) {
	platform := &fakeDatabasePlatform{databases: []model.Database{{Name: "app"}}}
	client := databaseRPCServer(t, platform)
	got, err := client.ListDatabases(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "app" {
		t.Fatalf("got=%v", got)
	}
}

func TestServiceDatabaseCreateEndpoint(t *testing.T) {
	platform := &fakeDatabasePlatform{}
	client := databaseRPCServer(t, platform)
	created, err := client.CreateDatabase(context.Background(), "mysql", dbproto.DatabaseCreateRequest{Name: "blog", Charset: "utf8mb4"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "blog" {
		t.Fatalf("created=%+v", created)
	}
	platform.mu.Lock()
	defer platform.mu.Unlock()
	if platform.created.Name != "blog" || platform.created.Charset != "utf8mb4" {
		t.Fatalf("request=%+v", platform.created)
	}
}

func TestServiceDatabaseErrorMapping(t *testing.T) {
	t.Run("inactive", func(t *testing.T) {
		platform := &fakeDatabasePlatform{listErr: fmt.Errorf("%w: mysql", databases.ErrServiceInactive)}
		client := databaseRPCServer(t, platform)
		_, err := client.ListDatabases(context.Background(), "mysql")
		if !errors.Is(err, databases.ErrServiceInactive) {
			t.Fatalf("err=%v", err)
		}
		var helperErr *helperclient.Error
		if !errors.As(err, &helperErr) || helperErr.Status != http.StatusConflict || helperErr.Code != helperproto.CodeServiceInactive {
			t.Fatalf("helper error = %+v", helperErr)
		}
	})

	t.Run("protected", func(t *testing.T) {
		platform := &fakeDatabasePlatform{createErr: fmt.Errorf("%w: devstack", databases.ErrProtected)}
		client := databaseRPCServer(t, platform)
		_, err := client.CreateDatabase(context.Background(), "mysql", dbproto.DatabaseCreateRequest{Name: "devstack"})
		if !errors.Is(err, databases.ErrProtected) {
			t.Fatalf("err=%v", err)
		}
		var helperErr *helperclient.Error
		if !errors.As(err, &helperErr) || helperErr.Status != http.StatusForbidden || helperErr.Code != helperproto.CodeProtected {
			t.Fatalf("helper error = %+v", helperErr)
		}
	})

	t.Run("job not found", func(t *testing.T) {
		platform := &fakeDatabasePlatform{jobErr: fmt.Errorf("%w: job_1", databases.ErrJobNotFound)}
		client := databaseRPCServer(t, platform)
		_, err := client.GetJob(context.Background(), "job_1")
		if !errors.Is(err, databases.ErrJobNotFound) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("invalid service id", func(t *testing.T) {
		platform := &fakeDatabasePlatform{}
		client := databaseRPCServer(t, platform)
		_, err := client.ListDatabases(context.Background(), "../escape")
		if !errors.Is(err, helperproto.ErrInvalidRequest) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestServiceDatabaseStreamErrorBeforeHeaders(t *testing.T) {
	t.Run("export", func(t *testing.T) {
		platform := &fakeDatabasePlatform{exportReader: failingReadCloser{}}
		client := databaseRPCServer(t, platform)
		body, err := client.ExportDatabase(context.Background(), "mysql", "app")
		if err == nil {
			_ = body.Close()
			t.Fatal("export stream failure was reported as success")
		}
		var helperErr *helperclient.Error
		if !errors.As(err, &helperErr) || helperErr.Status != http.StatusInternalServerError {
			t.Fatalf("helper error = %+v", helperErr)
		}
	})

	t.Run("download", func(t *testing.T) {
		platform := &fakeDatabasePlatform{downloadReader: failingReadCloser{}}
		client := databaseRPCServer(t, platform)
		body, err := client.DownloadBackup(context.Background(), "mysql", "app--b.sql.gz")
		if err == nil {
			_ = body.Close()
			t.Fatal("download stream failure was reported as success")
		}
		var helperErr *helperclient.Error
		if !errors.As(err, &helperErr) || helperErr.Status != http.StatusInternalServerError {
			t.Fatalf("helper error = %+v", helperErr)
		}
	})
}

func TestServiceDatabaseEnvEndpoint(t *testing.T) {
	platform := &fakeDatabasePlatform{env: map[string]string{
		"DB_HOST":     "127.0.0.1",
		"DB_DATABASE": "app",
	}}
	client := databaseRPCServer(t, platform)
	env, err := client.ResolveSiteEnv(context.Background(), "mysql", dbproto.SiteEnvRequest{
		Links: []dbproto.LinkInfo{{ServiceID: "mysql", Database: "app", Username: "app", Primary: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_DATABASE"] != "app" {
		t.Fatalf("env=%v", env)
	}
}
