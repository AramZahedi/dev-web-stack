package agentdocker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
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

func TestUpdateRedisUserCustomRulesPreservedOnDisable(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	created, err := d.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache", AccessLevel: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := d.readDatabaseSecret("redis", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner.runOutputs["ACL LIST"] = []byte("user cache on #abc ~* +get +set\n")
	runner.calls = nil
	enabled := false
	updated, err := d.UpdateRedisUser(context.Background(), "redis", created.ID, databases.UpdateUserRequest{Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.AccessLevel != "custom" {
		t.Fatalf("updated = %+v", updated)
	}
	joined := runner.joinedCalls()
	if !strings.Contains(joined, "ACL SETUSER cache off") {
		t.Fatalf("custom user disable missing: %s", joined)
	}
	for _, rewrite := range []string{"-@all", "+@read", "reset"} {
		if strings.Contains(joined, rewrite) {
			t.Fatalf("custom user rules were rewritten (%s): %s", rewrite, joined)
		}
	}
	after, err := d.readDatabaseSecret("redis", created.ID)
	if err != nil || after != before {
		t.Fatalf("password changed on custom enable/disable: %q -> %q, %v", before, after, err)
	}
}

func TestRedisCommandWithSecretKeepsEveryRule(t *testing.T) {
	command := dbengine.NewRedisACL().SetUserCommand("cache", "s3cret", "full", true)
	delivered, secret, err := redisCommandWithSecret(command)
	if err != nil {
		t.Fatal(err)
	}
	if secret != ">s3cret" {
		t.Fatalf("secret argument = %q", secret)
	}
	// redis-cli -x appends the stdin argument to the command. Simulate the
	// final argv and prove that removing the password placeholder did not
	// remove any ACL rule.
	finalArgv := append(append([]string(nil), delivered...), secret)
	for _, rule := range command[4 : len(command)-1] {
		found := false
		for _, arg := range finalArgv {
			if arg == rule {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("rule %q missing from post -x argv: %v", rule, finalArgv)
		}
	}
	if finalArgv[len(finalArgv)-1] != ">s3cret" {
		t.Fatalf("password is not the final argument: %v", finalArgv)
	}
	for _, arg := range delivered {
		if arg == ">s3cret" {
			t.Fatalf("password leaked into argv: %v", delivered)
		}
	}
}

func TestSetRedisUserDeliversEveryRule(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	user, err := d.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache", AccessLevel: "full"})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := d.readDatabaseSecret("redis", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	var setUser []string
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call.args, " "), "ACL SETUSER cache") {
			setUser = call.args
		}
	}
	if len(setUser) == 0 {
		t.Fatalf("ACL SETUSER was not called: %s", runner.joinedCalls())
	}
	for _, rule := range []string{"+@all", "-@admin", "-@dangerous", "-config", "-failover"} {
		found := false
		for _, arg := range setUser {
			if arg == rule {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("rule %q missing from argv: %v", rule, setUser)
		}
	}
	if !runner.stdinContains(">" + secret) {
		t.Fatal("password was not delivered on stdin")
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

func TestRedisACLFileMigrationRejectsForeignACLFile(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	configDir := filepath.Join(d.root, "services", "redis", "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "redis.conf")
	if err := os.WriteFile(configPath, []byte("aclfile /etc/redis/custom.acl\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.EnableRedisACLFile(context.Background(), "redis"); !errors.Is(err, databases.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if runner.contains("restart redis") {
		t.Fatalf("foreign aclfile still restarted redis: %s", runner.joinedCalls())
	}
	if _, err := os.Stat(filepath.Join(d.root, "services", "redis", "data", "users.acl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("foreign aclfile wrote the managed ACL file: %v", err)
	}
	config, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(config), "/etc/redis/custom.acl") {
		t.Fatalf("config was rewritten: %s, %v", config, err)
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
