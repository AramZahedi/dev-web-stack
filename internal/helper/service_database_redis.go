//go:build !windows

package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

const redisACLFileLine = "aclfile /data/users.acl"

func redisUserID(username string) string {
	sum := sha256.Sum256([]byte(username))
	return "redis_" + hex.EncodeToString(sum[:])[:16]
}

// redisExec runs a redis-cli command. When a managed default password exists
// it is sent on stdin as an AUTH command; it never appears in argv.
func (c *DockerServiceController) redisExec(ctx context.Context, serviceID string, args ...string) ([]byte, error) {
	password, hasPassword, err := c.redisDefaultPassword(serviceID)
	if err != nil {
		return nil, err
	}
	if !hasPassword {
		return c.composeDatabase(ctx, serviceID, append([]string{"redis-cli"}, args...)...)
	}
	return c.redisExecInput(ctx, serviceID, []string{strings.Join(args, " ")}, password)
}

// redisExecInput sends one or more command lines to redis-cli on stdin. The
// optional default password is sent as an AUTH line first.
func (c *DockerServiceController) redisExecInput(ctx context.Context, serviceID string, commands []string, password string) ([]byte, error) {
	if password == "" {
		current, hasPassword, err := c.redisDefaultPassword(serviceID)
		if err != nil {
			return nil, err
		}
		if hasPassword {
			password = current
		}
	}
	var input strings.Builder
	if password != "" {
		fmt.Fprintf(&input, "AUTH default %s\n", password)
	}
	for _, command := range commands {
		input.WriteString(command)
		input.WriteByte('\n')
	}
	out, err := c.runComposeInput(ctx, []byte(input.String()), "exec", "-T", serviceID, "redis-cli")
	if err != nil {
		return out, err
	}
	if err := redisOutputError(out); err != nil {
		return out, err
	}
	return out, nil
}

func redisOutputError(out []byte) error {
	for _, line := range strings.Split(strings.ToLower(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "(error)") || strings.HasPrefix(line, "err ") ||
			strings.Contains(line, "noauth") || strings.Contains(line, "wrongpass") {
			return fmt.Errorf("%w: redis rejected the command", databases.ErrInvalidRequest)
		}
	}
	return nil
}

func (c *DockerServiceController) redisDefaultPassword(serviceID string) (string, bool, error) {
	value, err := c.readDatabaseSecret(serviceID, redisUserID("default"))
	if errors.Is(err, databases.ErrPasswordMissing) {
		// Compatibility with early lane builds that stored the default user's
		// password under the literal user name.
		value, err = c.readDatabaseSecret(serviceID, "default")
	}
	if errors.Is(err, databases.ErrPasswordMissing) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if strings.ContainsAny(value, "\r\n\t ") {
		return "", false, fmt.Errorf("%w: Redis default password contains whitespace", databases.ErrPasswordMissing)
	}
	return value, true, nil
}

// ListRedisUsers parses ACL LIST into the shared user view.
func (c *DockerServiceController) listRedisUsers(ctx context.Context, serviceID string) ([]model.DatabaseUser, error) {
	if err := c.requireActive(ctx, serviceID); err != nil {
		return nil, err
	}
	out, err := c.redisExec(ctx, serviceID, "ACL", "LIST")
	if err != nil {
		return nil, err
	}
	users := make([]model.DatabaseUser, 0)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "user ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		username := fields[1]
		if err := dbengine.ValidateIdentifier(username); err != nil {
			// ACL LIST is external input; never return or manage an unsafe
			// username. A user with spaces cannot be addressed unambiguously.
			continue
		}
		enabled := false
		accessLevel := "custom"
		for _, field := range fields[2:] {
			switch {
			case field == "on":
				enabled = true
			case field == "off":
				enabled = false
			case field == "+@all":
				accessLevel = "full"
			case field == "+@read":
				accessLevel = "read-only"
			}
		}
		id := redisUserID(username)
		stored := c.databaseSecretExists(serviceID, id)
		users = append(users, model.DatabaseUser{
			ID:              id,
			ServiceID:       serviceID,
			Engine:          model.DatabaseRedis,
			Username:        username,
			Enabled:         enabled,
			Managed:         stored,
			PasswordManaged: stored,
			AccessLevel:     accessLevel,
		})
	}
	return users, nil
}

// ListRedisUsers returns the Redis ACL inventory.
func (c *DockerServiceController) ListRedisUsers(ctx context.Context, serviceID string) ([]model.DatabaseUser, error) {
	return c.listRedisUsers(ctx, serviceID)
}

// SetRedisUser creates or replaces a Redis ACL user.
func (c *DockerServiceController) SetRedisUser(ctx context.Context, serviceID string, req databases.CreateUserRequest) (model.DatabaseUser, error) {
	return c.setRedisUser(ctx, serviceID, req)
}

// DeleteRedisUser deletes a Redis ACL user.
func (c *DockerServiceController) DeleteRedisUser(ctx context.Context, serviceID, userID string, req databases.DeleteUserRequest) error {
	return c.deleteRedisUser(ctx, serviceID, userID, req)
}

// SetRedisPassword rotates a Redis user's password.
func (c *DockerServiceController) SetRedisPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	return c.setRedisPassword(ctx, serviceID, userID)
}

// RevealRedisPassword returns a stored Redis user password.
func (c *DockerServiceController) RevealRedisPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	return c.RevealUserPassword(ctx, serviceID, userID)
}

func (c *DockerServiceController) findRedisUser(ctx context.Context, serviceID, userID string) (model.DatabaseUser, error) {
	users, err := c.listRedisUsers(ctx, serviceID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	for _, user := range users {
		if user.ID == userID {
			return user, nil
		}
	}
	return model.DatabaseUser{}, fmt.Errorf("%w: Redis user %q", databases.ErrNotFound, userID)
}

func (c *DockerServiceController) findRedisUserByName(ctx context.Context, serviceID, username string) (model.DatabaseUser, error) {
	users, err := c.listRedisUsers(ctx, serviceID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	for _, user := range users {
		if user.Username == username {
			return user, nil
		}
	}
	return model.DatabaseUser{}, fmt.Errorf("%w: Redis user %q", databases.ErrNotFound, username)
}

// SetRedisUser creates or replaces a Redis ACL user. The password is sent on
// stdin; only non-secret ACL rules appear in argv.
func (c *DockerServiceController) setRedisUser(ctx context.Context, serviceID string, req databases.CreateUserRequest) (model.DatabaseUser, error) {
	if err := c.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseUser{}, err
	}
	if err := dbengine.ValidateIdentifier(req.Username); err != nil {
		return model.DatabaseUser{}, invalidIdentifier(err)
	}
	if dbengine.SystemUsers[model.DatabaseRedis][req.Username] {
		return model.DatabaseUser{}, fmt.Errorf("%w: user %q", databases.ErrProtected, req.Username)
	}
	if len(req.Grants) > 0 {
		return model.DatabaseUser{}, fmt.Errorf("%w: Redis users do not have database grants", databases.ErrUnsupported)
	}
	accessLevel := req.AccessLevel
	if accessLevel == "" {
		accessLevel = "read-only"
	}
	if accessLevel != "read-only" && accessLevel != "full" {
		return model.DatabaseUser{}, fmt.Errorf("%w: unsupported Redis access level %q", databases.ErrInvalidRequest, accessLevel)
	}
	if err := c.EnableRedisACLFile(ctx); err != nil {
		return model.DatabaseUser{}, err
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	password, err := credentials.NewSecret()
	if err != nil {
		return model.DatabaseUser{}, err
	}
	userID := redisUserID(req.Username)
	if _, err := c.redisExecInput(ctx, serviceID, []string{redisSetUserPasswordLine(req.Username, password, true)}, ""); err != nil {
		return model.DatabaseUser{}, err
	}
	if err := c.writeDatabaseSecret(serviceID, userID, password); err != nil {
		_ = c.deleteDatabaseSecret(serviceID, userID)
		return model.DatabaseUser{
			ID:              userID,
			ServiceID:       serviceID,
			Engine:          model.DatabaseRedis,
			Username:        req.Username,
			Enabled:         true,
			Managed:         false,
			PasswordManaged: false,
			AccessLevel:     accessLevel,
		}, fmt.Errorf("store generated password: %w", err)
	}
	if err := c.redisApplyUserRules(ctx, serviceID, req.Username, accessLevel, true); err != nil {
		return model.DatabaseUser{}, err
	}
	return c.findRedisUserByName(ctx, serviceID, req.Username)
}

func redisSetUserPasswordLine(username, password string, enabled bool) string {
	state := "off"
	if enabled {
		state = "on"
	}
	return fmt.Sprintf("ACL SETUSER %s reset %s >%s", username, state, password)
}

// redisResetPasswordLine changes only the password. It is used for resets so
// existing rules and the default user's admin/ACL grants survive.
func redisResetPasswordLine(username, password string) string {
	return fmt.Sprintf("ACL SETUSER %s resetpass >%s", username, password)
}

func (c *DockerServiceController) redisApplyUserRules(ctx context.Context, serviceID, username, accessLevel string, enabled bool) error {
	// -@all/resetkeys/resetchannels make this a replacement, not an
	// accumulation, while preserving the password set by the caller.
	args := []string{"ACL", "SETUSER", username, "-@all", "resetkeys", "resetchannels", "~*"}
	if accessLevel == "full" {
		args = append(args, "+@all", "-@admin", "-@dangerous")
	} else {
		args = append(args, "+@read", "-@admin", "-@dangerous")
	}
	args = append(args,
		"-config", "-debug", "-shutdown", "-flushall", "-flushdb", "-acl",
		"-module", "-replicaof", "-slaveof", "-migrate", "-save", "-bgsave",
		"-bgrewriteaof", "-swapdb", "-cluster", "-failover",
	)
	if !enabled {
		args = append(args, "off")
	}
	if _, err := c.redisExec(ctx, serviceID, args...); err != nil {
		return err
	}
	_, err := c.redisExec(ctx, serviceID, "ACL", "SAVE")
	return err
}

func (c *DockerServiceController) updateRedisUser(ctx context.Context, serviceID, userID string, req databases.UpdateUserRequest) (model.DatabaseUser, error) {
	if len(req.Grants) > 0 {
		return model.DatabaseUser{}, fmt.Errorf("%w: Redis users do not have database grants", databases.ErrUnsupported)
	}
	user, err := c.findRedisUser(ctx, serviceID, userID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	if dbengine.SystemUsers[model.DatabaseRedis][user.Username] {
		return model.DatabaseUser{}, fmt.Errorf("%w: user %q", databases.ErrProtected, user.Username)
	}
	if req.Enabled == nil && req.AccessLevel == "" {
		return user, nil
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return model.DatabaseUser{}, err
	}
	enabled := user.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	accessLevel := user.AccessLevel
	if req.AccessLevel != "" {
		accessLevel = req.AccessLevel
	}
	if accessLevel != "read-only" && accessLevel != "full" {
		return model.DatabaseUser{}, fmt.Errorf("%w: unsupported Redis access level %q", databases.ErrInvalidRequest, accessLevel)
	}
	if err := c.EnableRedisACLFile(ctx); err != nil {
		return model.DatabaseUser{}, err
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	if err := c.redisApplyUserRules(ctx, serviceID, user.Username, accessLevel, enabled); err != nil {
		return model.DatabaseUser{}, err
	}
	return c.findRedisUser(ctx, serviceID, userID)
}

func (c *DockerServiceController) deleteRedisUser(ctx context.Context, serviceID, userID string, req databases.DeleteUserRequest) error {
	if req.ReassignOwned {
		return fmt.Errorf("%w: Redis users do not own database objects", databases.ErrUnsupported)
	}
	user, err := c.findRedisUser(ctx, serviceID, userID)
	if err != nil {
		return err
	}
	if req.Confirm != user.Username {
		return fmt.Errorf("%w: confirmation does not match user %q", databases.ErrInvalidRequest, user.Username)
	}
	if dbengine.SystemUsers[model.DatabaseRedis][user.Username] {
		return fmt.Errorf("%w: user %q", databases.ErrProtected, user.Username)
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return err
	}
	if err := c.EnableRedisACLFile(ctx); err != nil {
		return err
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	if _, err := c.redisExec(ctx, serviceID, "ACL", "DELUSER", user.Username); err != nil {
		return err
	}
	if _, err := c.redisExec(ctx, serviceID, "ACL", "SAVE"); err != nil {
		return err
	}
	return c.deleteDatabaseSecret(serviceID, user.ID)
}

// setRedisPassword rotates a Redis user's password, including the default user.
func (c *DockerServiceController) setRedisPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	user, err := c.findRedisUser(ctx, serviceID, userID)
	if err != nil {
		return databases.Secret{}, err
	}
	if err := c.requireActive(ctx, serviceID); err != nil {
		return databases.Secret{}, err
	}
	if err := c.EnableRedisACLFile(ctx); err != nil {
		return databases.Secret{}, err
	}
	lock := c.databaseLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	password, err := credentials.NewSecret()
	if err != nil {
		return databases.Secret{}, err
	}
	if _, err := c.redisExecInput(ctx, serviceID, []string{redisResetPasswordLine(user.Username, password)}, ""); err != nil {
		return databases.Secret{}, err
	}
	if err := c.writeDatabaseSecret(serviceID, user.ID, password); err != nil {
		_ = c.deleteDatabaseSecret(serviceID, user.ID)
		return databases.Secret{}, fmt.Errorf("store generated password: %w", err)
	}
	// resetpass changes only the password. Existing rules, including the
	// default user's admin/ACL grants and any custom ACL, are preserved.
	if _, err := c.redisExec(ctx, serviceID, "ACL", "SAVE"); err != nil {
		return databases.Secret{}, err
	}
	return databases.Secret{Value: password}, nil
}

// EnableRedisACLFile makes Redis persist ACL changes in /data/users.acl. It is
// the unconfirmed entry point: if the migration would change redis.conf or
// restart Redis, it fails closed with ErrConfirmationRequired.
func (c *DockerServiceController) EnableRedisACLFile(ctx context.Context) error {
	return c.enableRedisACLFile(ctx, false)
}

// EnableRedisACLFileConfirmed migrates Redis to ACL-file persistence. The
// caller must pass confirm=true after the user approves the one-time restart.
func (c *DockerServiceController) EnableRedisACLFileConfirmed(ctx context.Context, confirm bool) error {
	return c.enableRedisACLFile(ctx, confirm)
}

// enableRedisACLFile backs up redis.conf, seeds users.acl from ACL LIST, and
// restarts Redis once. Repeated calls are idempotent. No config write, ACL
// seed, or restart happens until the caller confirms the migration.
func (c *DockerServiceController) enableRedisACLFile(ctx context.Context, confirm bool) error {
	lock := c.databaseLock(string(services.IDRedis))
	lock.Lock()
	defer lock.Unlock()

	configPath := filepath.Join(c.root, "services", string(services.IDRedis), "config", "redis.conf")
	aclPath := filepath.Join(c.root, "services", string(services.IDRedis), "data", "users.acl")
	markerPath := filepath.Join(c.root, "services", string(services.IDRedis), "config", ".aclfile-restart-pending")
	for _, target := range []string{configPath, aclPath, markerPath} {
		if err := rejectSymlinkPath(c.root, target); err != nil {
			return fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
		}
	}

	config, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: Redis is not installed", databases.ErrInvalidRequest)
		}
		return fmt.Errorf("read redis.conf: %w", err)
	}
	configHasACL := hasRedisACLFileLine(string(config))
	aclExists, aclInfo, err := inspectRedisACLFile(aclPath)
	if err != nil {
		return err
	}
	markerExists := databaseFileExists(markerPath)
	needsMigration := !configHasACL || !aclExists || markerExists
	if !needsMigration {
		return ensureRedisACLFileMode(aclPath, aclInfo)
	}
	if !confirm {
		return fmt.Errorf("%w: Redis ACL file migration requires confirmation", databases.ErrConfirmationRequired)
	}
	if err := c.requireActive(ctx, string(services.IDRedis)); err != nil {
		return err
	}
	changed := false
	if !configHasACL {
		if err := writeAtomicFile(configPath+".bak", config, 0o600); err != nil {
			return fmt.Errorf("backup redis.conf: %w", err)
		}
		updated := string(config)
		if updated != "" && !strings.HasSuffix(updated, "\n") {
			updated += "\n"
		}
		updated += redisACLFileLine + "\n"
		if err := writeFileInPlace(configPath, []byte(updated), 0o644); err != nil {
			return fmt.Errorf("update redis.conf: %w", err)
		}
		changed = true
	}
	if !aclExists {
		out, err := c.redisExec(ctx, string(services.IDRedis), "ACL", "LIST")
		if err != nil {
			return err
		}
		var lines []string
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "user ") {
				lines = append(lines, line)
			}
		}
		if len(lines) == 0 {
			return fmt.Errorf("%w: Redis ACL LIST returned no users", databases.ErrInvalidRequest)
		}
		if err := os.MkdirAll(filepath.Dir(aclPath), 0o700); err != nil {
			return fmt.Errorf("create Redis data directory: %w", err)
		}
		if err := writeAtomicFile(aclPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			return fmt.Errorf("seed users.acl: %w", err)
		}
		changed = true
	} else if err := ensureRedisACLFileMode(aclPath, aclInfo); err != nil {
		return err
	}
	if !changed && !markerExists {
		return nil
	}
	if err := writeAtomicFile(markerPath, []byte("restart pending\n"), 0o600); err != nil {
		return fmt.Errorf("write ACL restart marker: %w", err)
	}
	if err := c.Restart(ctx, services.IDRedis); err != nil {
		return fmt.Errorf("restart Redis to load users.acl: %w", err)
	}
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove ACL restart marker: %w", err)
	}
	return nil
}

func inspectRedisACLFile(path string) (bool, os.FileInfo, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return false, nil, nil
	case err != nil:
		return false, nil, fmt.Errorf("inspect Redis ACL file: %w", err)
	case info.Mode()&os.ModeSymlink != 0:
		return false, nil, fmt.Errorf("%w: Redis ACL file is a symlink", databases.ErrInvalidRequest)
	case !info.Mode().IsRegular():
		return false, nil, fmt.Errorf("%w: Redis ACL file is not a regular file", databases.ErrInvalidRequest)
	}
	return true, info, nil
}

func ensureRedisACLFileMode(path string, info os.FileInfo) error {
	if info != nil && info.Mode().Perm() == 0o600 {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set users.acl mode: %w", err)
	}
	return nil
}

func databaseFileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0
}

func hasRedisACLFileLine(config string) bool {
	for _, line := range strings.Split(config, "\n") {
		if strings.TrimSpace(line) == redisACLFileLine {
			return true
		}
	}
	return false
}
