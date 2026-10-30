package agentdocker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestRedisFullDoesNotGrantAdmin(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	user, err := d.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache", AccessLevel: "full"})
	if err != nil {
		t.Fatal(err)
	}
	if !user.PasswordManaged || user.AccessLevel != "full" || !user.Enabled {
		t.Fatalf("user = %+v", user)
	}
	joined := runner.joinedCalls()
	if strings.Contains(joined, "+@admin") || strings.Contains(joined, "+@dangerous") {
		t.Fatalf("admin grant: %s", joined)
	}
	if !strings.Contains(joined, "ACL SETUSER") || !strings.Contains(joined, "ACL SAVE") {
		t.Fatalf("calls=%s", joined)
	}
	secret, err := d.readDatabaseSecret("redis", user.ID)
	if err != nil || secret == "" {
		t.Fatalf("secret = %q, %v", secret, err)
	}
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call.args, " "), secret) {
			t.Fatalf("redis password appeared in argv: %v", call.args)
		}
	}
	if !runner.stdinContains(">" + secret) {
		t.Fatal("redis password was not sent on stdin")
	}
}

func TestRedisReadOnlyDoesNotGrantAll(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	user, err := d.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache", AccessLevel: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	if user.AccessLevel != "read-only" {
		t.Fatalf("access level = %q", user.AccessLevel)
	}
	joined := strings.ToLower(runner.joinedCalls())
	if strings.Contains(joined, "+@all") || !strings.Contains(joined, "+@read") {
		t.Fatalf("read-only ACL = %s", joined)
	}
}

func TestRedisACLFileIsEnabledOnce(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	configDir := filepath.Join(d.root, "services", "redis", "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "redis.conf")
	if err := os.WriteFile(configPath, []byte("appendonly yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner.runOutputs["ACL LIST"] = []byte("user default on nopass ~* &* +@all\n")
	if err := d.EnableRedisACLFile(context.Background(), "redis"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "aclfile /data/users.acl") {
		t.Fatalf("config = %s", data)
	}
	aclData, err := os.ReadFile(filepath.Join(d.root, "services", "redis", "data", "users.acl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(aclData), "user default on nopass") {
		t.Fatalf("ACL file = %s", aclData)
	}
	if !runner.contains("restart redis") {
		t.Fatalf("calls = %s", runner.joinedCalls())
	}
	runner.calls = nil
	if err := d.EnableRedisACLFile(context.Background(), "redis"); err != nil {
		t.Fatal(err)
	}
	if runner.contains("restart redis") {
		t.Fatalf("second enable restarted Redis: %s", runner.joinedCalls())
	}
}

func TestRedisDeleteUserProtectsDefault(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	defaultID := databaseUserID("redis", "default", "")
	if err := d.DeleteRedisUser(context.Background(), "redis", defaultID, "default"); !errors.Is(err, databases.ErrProtected) {
		t.Fatalf("err = %v, want ErrProtected", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("protected delete invoked Docker: %v", runner.joinedCalls())
	}
}

func TestRedisListUsersParses(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["ACL LIST"] = []byte("user default on nopass ~* &* +@all\nuser cache on #abc ~* +@read -@admin -@dangerous\n")
	users, err := d.ListRedisUsers(context.Background(), "redis")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %+v", users)
	}
	byName := map[string]bool{}
	for _, user := range users {
		byName[user.Username] = true
		if user.Username == "default" && user.AccessLevel != "full" {
			t.Fatalf("default = %+v", user)
		}
		if user.Username == "cache" && user.AccessLevel != "read-only" {
			t.Fatalf("cache = %+v", user)
		}
	}
	if !byName["default"] || !byName["cache"] {
		t.Fatalf("users = %+v", users)
	}
}

func TestSetRedisPasswordStoresSecret(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	secret, err := d.SetRedisPassword(context.Background(), "redis")
	if err != nil {
		t.Fatal(err)
	}
	defaultID := databaseUserID("redis", "default", "")
	stored, err := d.readDatabaseSecret("redis", defaultID)
	if err != nil || stored != secret.Value {
		t.Fatalf("stored = %q, %v", stored, err)
	}
	if !runner.contains("ACL SETUSER") || !runner.contains("ACL SAVE") {
		t.Fatalf("calls = %s", runner.joinedCalls())
	}
}

func TestRevealRedisPassword(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	defaultID := databaseUserID("redis", "default", "")
	if err := d.writeDatabaseSecret("redis", defaultID, "s3cret"); err != nil {
		t.Fatal(err)
	}
	secret, err := d.RevealRedisPassword(context.Background(), "redis", defaultID)
	if err != nil || secret.Value != "s3cret" {
		t.Fatalf("secret = %+v, %v", secret, err)
	}
	if _, err := d.RevealRedisPassword(context.Background(), "redis", databaseUserID("redis", "cache", "")); !errors.Is(err, databases.ErrPasswordMissing) {
		t.Fatalf("missing secret err = %v", err)
	}
}

func TestSetRedisPasswordDeletesStaleSecretOnWriteFailure(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	defaultID := databaseUserID("redis", "default", "")
	if err := d.writeDatabaseSecret("redis", defaultID, "old-secret"); err != nil {
		t.Fatal(err)
	}
	d.secretWriter = func(string, string, string) error { return errors.New("disk full") }
	if _, err := d.SetRedisPassword(context.Background(), "redis"); err == nil {
		t.Fatal("SetRedisPassword ignored a secret write failure")
	}
	if _, err := d.readDatabaseSecret("redis", defaultID); !errors.Is(err, databases.ErrPasswordMissing) {
		t.Fatalf("stale redis secret is still revealable: %v", err)
	}
}

func TestSetRedisUserDeletesStaleSecretOnWriteFailure(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	d.secretWriter = func(string, string, string) error { return errors.New("disk full") }
	if _, err := d.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache", AccessLevel: "read-only"}); err == nil {
		t.Fatal("SetRedisUser ignored a secret write failure")
	}
	if _, err := d.readDatabaseSecret("redis", databaseUserID("redis", "cache", "")); !errors.Is(err, databases.ErrPasswordMissing) {
		t.Fatalf("failed redis create left a secret: %v", err)
	}
}

func TestUpdateRedisUserTogglesWithoutRotatingPassword(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	created, err := d.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache", AccessLevel: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := d.readDatabaseSecret("redis", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner.runOutputs["ACL LIST"] = []byte("user cache on #abc ~* +@read\n")
	runner.calls = nil
	enabled := false
	updated, err := d.UpdateRedisUser(context.Background(), "redis", created.ID, databases.UpdateUserRequest{Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Fatalf("updated = %+v, want disabled", updated)
	}
	joined := runner.joinedCalls()
	if !strings.Contains(joined, "ACL SETUSER cache off") {
		t.Fatalf("ACL update missing off: %s", joined)
	}
	if strings.Contains(joined, "reset") || strings.Contains(joined, ">") {
		t.Fatalf("enable/disable rotated the password: %s", joined)
	}
	after, err := d.readDatabaseSecret("redis", created.ID)
	if err != nil || after != before {
		t.Fatalf("password changed on enable/disable: %q -> %q, %v", before, after, err)
	}
}

func TestUpdateRedisUserChangesAccessLevelWithoutRotatingPassword(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	created, err := d.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache", AccessLevel: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := d.readDatabaseSecret("redis", created.ID)
	runner.runOutputs["ACL LIST"] = []byte("user cache on #abc ~* +@read\n")
	runner.calls = nil
	updated, err := d.UpdateRedisUser(context.Background(), "redis", created.ID, databases.UpdateUserRequest{AccessLevel: "full"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AccessLevel != "full" {
		t.Fatalf("updated = %+v", updated)
	}
	joined := strings.ToLower(runner.joinedCalls())
	if !strings.Contains(joined, "acl setuser cache") || !strings.Contains(joined, "+@all") {
		t.Fatalf("access level update missing: %s", joined)
	}
	if strings.Contains(joined, "reset") || strings.Contains(joined, ">") {
		t.Fatalf("access level update rotated the password: %s", joined)
	}
	after, _ := d.readDatabaseSecret("redis", created.ID)
	if after != before {
		t.Fatalf("password changed on access level update")
	}
}

func TestUpdateRedisUserRejectsGrants(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	created, err := d.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpdateRedisUser(context.Background(), "redis", created.ID, databases.UpdateUserRequest{Grants: []model.DatabaseGrant{{Database: "app"}}}); !errors.Is(err, databases.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestDeleteRedisUserRequiresConfirmation(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	created, err := d.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache"})
	if err != nil {
		t.Fatal(err)
	}
	runner.runOutputs["ACL LIST"] = []byte("user cache on #abc ~* +@read\n")
	if err := d.DeleteRedisUser(context.Background(), "redis", created.ID, "wrong"); !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("wrong confirm err = %v", err)
	}
	if err := d.DeleteRedisUser(context.Background(), "redis", created.ID, "cache"); err != nil {
		t.Fatal(err)
	}
	if !runner.contains("ACL DELUSER cache") {
		t.Fatalf("delete did not remove the redis user: %s", runner.joinedCalls())
	}
}

func TestRedisMutationMigratesACLFileBeforeSetUser(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	configDir := filepath.Join(d.root, "services", "redis", "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "redis.conf"), []byte("appendonly yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner.runOutputs["ACL LIST"] = []byte("user default on nopass ~* &* +@all\n")
	if _, err := d.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache"}); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(configDir, "redis.conf"))
	if err != nil || !strings.Contains(string(config), "aclfile /data/users.acl") {
		t.Fatalf("config = %s, %v", config, err)
	}
	if _, err := os.Stat(filepath.Join(d.root, "services", "redis", "data", "users.acl")); err != nil {
		t.Fatalf("users.acl missing: %v", err)
	}
	restart := strings.Index(runner.joinedCalls(), "restart redis")
	setuser := strings.Index(runner.joinedCalls(), "ACL SETUSER")
	if restart < 0 || setuser < 0 || restart > setuser {
		t.Fatalf("ACL mutation ran before migration:\n%s", runner.joinedCalls())
	}
}

func TestRedisACLFileMigrationBacksUpExistingACL(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	configDir := filepath.Join(d.root, "services", "redis", "config")
	dataDir := filepath.Join(d.root, "services", "redis", "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "redis.conf"), []byte("appendonly yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "users.acl"), []byte("user old on #hash ~* +@read\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner.runOutputs["ACL LIST"] = []byte("user default on nopass ~* &* +@all\n")
	if err := d.EnableRedisACLFile(context.Background(), "redis"); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dataDir, "users.acl.bak.*"))
	if len(matches) != 1 {
		t.Fatalf("ACL backups = %v", matches)
	}
	backup, err := os.ReadFile(matches[0])
	if err != nil || !strings.Contains(string(backup), "user old") {
		t.Fatalf("ACL backup = %s, %v", backup, err)
	}
}
