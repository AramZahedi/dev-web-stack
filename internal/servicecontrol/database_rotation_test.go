package servicecontrol

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type fakeCredentialRotator struct {
	id     string
	calls  int
	err    error
	rotate func() error
}

func (r *fakeCredentialRotator) RotateServiceCredentials(_ context.Context, id string) error {
	r.calls++
	r.id = id
	if r.rotate != nil {
		if err := r.rotate(); err != nil {
			return err
		}
	}
	return r.err
}

func TestDatabaseRotateCredentialsUsesHelperAndReadsNewSecret(t *testing.T) {
	prep, root := testDatabasePreparer(t)
	if err := prep.PrepareInstall(context.Background(), testDatabaseService(t, services.IDMySQL)); err != nil {
		t.Fatal(err)
	}
	oldSecret := strings.TrimSpace(readTestFile(t, filepath.Join(root, "secrets", "mysql", "devstack_password")))

	var newSecret string
	rotator := &fakeCredentialRotator{rotate: func() error {
		store := &credentials.Store{Root: root}
		value, err := store.Rotate("mysql", "devstack_password")
		newSecret = value
		return err
	}}
	prep.Rotator = rotator

	got, err := prep.RotateCredentials(context.Background(), services.IDMySQL)
	if err != nil {
		t.Fatalf("RotateCredentials() = %v", err)
	}
	if rotator.calls != 1 || rotator.id != "mysql" {
		t.Fatalf("rotator calls = %d id = %q", rotator.calls, rotator.id)
	}
	if newSecret == "" || newSecret == oldSecret {
		t.Fatalf("rotated secret = %q, old = %q", newSecret, oldSecret)
	}
	if got.Password != newSecret || got.Engine != "mysql" || got.Username != "devstack" {
		t.Fatalf("credentials = %+v, want the rotated devstack secret", got)
	}
	if strings.Contains(got.Password, oldSecret) {
		t.Fatal("rotated credentials contain the old secret")
	}
}

func TestDatabaseRotateCredentialsKeepsSecretOnHelperFailure(t *testing.T) {
	prep, root := testDatabasePreparer(t)
	if err := prep.PrepareInstall(context.Background(), testDatabaseService(t, services.IDPostgreSQL)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "secrets", "postgresql", "devstack_password")
	oldSecret := strings.TrimSpace(readTestFile(t, path))
	prep.Rotator = &fakeCredentialRotator{err: errors.New("database account update failed")}

	_, err := prep.RotateCredentials(context.Background(), services.IDPostgreSQL)
	if err == nil || !strings.Contains(err.Error(), "database account update failed") {
		t.Fatalf("RotateCredentials() error = %v", err)
	}
	if got := strings.TrimSpace(readTestFile(t, path)); got != oldSecret {
		t.Fatal("helper failure changed the stored secret")
	}
	if strings.Contains(err.Error(), oldSecret) {
		t.Fatal("rotation error leaks the old secret")
	}
}

func TestDatabaseRotateCredentialsRedisUnsupported(t *testing.T) {
	prep, _ := testDatabasePreparer(t)
	rotator := &fakeCredentialRotator{}
	prep.Rotator = rotator
	if _, err := prep.RotateCredentials(context.Background(), services.IDRedis); !errors.Is(err, ErrCredentialsUnsupported) {
		t.Fatalf("RotateCredentials(redis) error = %v, want ErrCredentialsUnsupported", err)
	}
	if rotator.calls != 0 {
		t.Fatal("redis rotation called the helper")
	}
}

func TestDatabaseRotateCredentialsWithoutHelperIsUnavailable(t *testing.T) {
	prep, _ := testDatabasePreparer(t)
	if err := prep.PrepareInstall(context.Background(), testDatabaseService(t, services.IDMySQL)); err != nil {
		t.Fatal(err)
	}
	_, err := prep.RotateCredentials(context.Background(), services.IDMySQL)
	if !errors.Is(err, ErrCredentialsRotationUnavailable) {
		t.Fatalf("RotateCredentials() error = %v, want ErrCredentialsRotationUnavailable", err)
	}
}

func TestNewDatabasePreparersWiresRotator(t *testing.T) {
	rotator := &fakeCredentialRotator{}
	preparers := NewDatabasePreparers(t.TempDir(), rotator)
	for _, id := range []services.ID{services.IDMySQL, services.IDMariaDB, services.IDPostgreSQL, services.IDRedis} {
		prep, ok := preparers[id].(*DatabasePreparer)
		if !ok {
			t.Fatalf("preparer %q type = %T", id, preparers[id])
		}
		if prep.Rotator != rotator {
			t.Fatalf("preparer %q rotator = %T, want injected rotator", id, prep.Rotator)
		}
	}
}

func TestDatabaseRotateCredentialsHonorsContext(t *testing.T) {
	prep, _ := testDatabasePreparer(t)
	rotator := &fakeCredentialRotator{}
	prep.Rotator = rotator
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prep.RotateCredentials(ctx, services.IDMySQL); !errors.Is(err, context.Canceled) {
		t.Fatalf("RotateCredentials() error = %v, want context.Canceled", err)
	}
	if rotator.calls != 0 {
		t.Fatal("canceled rotation called the helper")
	}
}
