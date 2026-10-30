package agentdocker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

const redisShellWrapper = `set -eu
IFS= read -r pw || true
if [ -n "$pw" ]; then REDISCLI_AUTH="$pw"; export REDISCLI_AUTH; fi
exec "$@"`

func (d *Docker) ListRedisUsers(ctx context.Context, serviceID string) ([]model.DatabaseUser, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	if engine.ID() != model.DatabaseRedis {
		return nil, databases.ErrUnsupported
	}
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return nil, err
	}
	acl := dbengine.NewRedisACL()
	output, err := d.runRedisCommand(ctx, serviceID, acl.ListUsersCommand(), "")
	if err != nil {
		return nil, err
	}
	users := parseRedisACLUsers(output)
	for i := range users {
		users[i].ServiceID = string(service.ID)
		users[i].Engine = model.DatabaseRedis
		users[i].ID = databaseUserID(string(service.ID), users[i].Username, "")
		users[i].PasswordManaged = d.databaseSecretExists(serviceID, users[i].ID)
	}
	return users, nil
}

func (d *Docker) SetRedisUser(ctx context.Context, serviceID string, req databases.CreateUserRequest) (model.DatabaseUser, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	if engine.ID() != model.DatabaseRedis {
		return model.DatabaseUser{}, databases.ErrUnsupported
	}
	if err := engine.ValidateUsername(req.Username); err != nil {
		return model.DatabaseUser{}, mapEngineError(err)
	}
	if req.Username == "default" || engine.ProtectedUser(req.Username, "") {
		return model.DatabaseUser{}, databases.ErrProtected
	}
	accessLevel, err := normalizeRedisAccessLevel(req.AccessLevel)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	password, err := credentials.NewSecret()
	if err != nil {
		return model.DatabaseUser{}, fmt.Errorf("agentdocker: generate redis password: %w", err)
	}
	acl := dbengine.NewRedisACL()
	command, secretArg, err := redisCommandWithSecret(acl.SetUserCommand(req.Username, password, accessLevel, true))
	if err != nil {
		return model.DatabaseUser{}, err
	}
	userID := databaseUserID(string(service.ID), req.Username, "")
	user := model.DatabaseUser{
		ID:              userID,
		ServiceID:       string(service.ID),
		Engine:          model.DatabaseRedis,
		Username:        req.Username,
		Enabled:         true,
		Managed:         true,
		PasswordManaged: true,
		AccessLevel:     accessLevel,
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return model.DatabaseUser{}, err
	}
	if _, err := d.runRedisCommand(ctx, serviceID, command, secretArg); err != nil {
		return model.DatabaseUser{}, err
	}
	if err := d.writeDatabaseSecret(serviceID, userID, password); err != nil {
		user.PasswordManaged = false
		return user, fmt.Errorf("agentdocker: redis user created but its password could not be stored: %w", err)
	}
	if _, err := d.runRedisCommand(ctx, serviceID, acl.SaveCommand(), ""); err != nil {
		return user, err
	}
	return user, nil
}

func (d *Docker) DeleteRedisUser(ctx context.Context, serviceID, userID string) error {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return err
	}
	if engine.ID() != model.DatabaseRedis {
		return databases.ErrUnsupported
	}
	defaultID := databaseUserID(string(service.ID), "default", "")
	if userID == defaultID || d.isProtectedUserID(engine, string(service.ID), userID) {
		return databases.ErrProtected
	}
	users, err := d.ListRedisUsers(ctx, serviceID)
	if err != nil {
		return err
	}
	var username string
	for _, user := range users {
		if user.ID == userID {
			username = user.Username
			break
		}
	}
	if username == "" {
		return databases.ErrNotFound
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return err
	}
	acl := dbengine.NewRedisACL()
	if _, err := d.runRedisCommand(ctx, serviceID, acl.DeleteUserCommand(username), ""); err != nil {
		return err
	}
	if _, err := d.runRedisCommand(ctx, serviceID, acl.SaveCommand(), ""); err != nil {
		return err
	}
	return d.deleteDatabaseSecret(serviceID, userID)
}

func (d *Docker) SetRedisPassword(ctx context.Context, serviceID string) (databases.Secret, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return databases.Secret{}, err
	}
	if engine.ID() != model.DatabaseRedis {
		return databases.Secret{}, databases.ErrUnsupported
	}
	password, err := credentials.NewSecret()
	if err != nil {
		return databases.Secret{}, fmt.Errorf("agentdocker: generate redis password: %w", err)
	}
	acl := dbengine.NewRedisACL()
	command, secretArg, err := redisCommandWithSecret(acl.SetUserCommand("default", password, "full", true))
	if err != nil {
		return databases.Secret{}, err
	}
	defaultID := databaseUserID(string(service.ID), "default", "")
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return databases.Secret{}, err
	}
	if _, err := d.runRedisCommand(ctx, serviceID, command, secretArg); err != nil {
		return databases.Secret{}, err
	}
	if err := d.writeDatabaseSecret(serviceID, defaultID, password); err != nil {
		return databases.Secret{}, fmt.Errorf("agentdocker: redis password changed but the new secret could not be stored: %w", err)
	}
	// Persist after the new secret exists so the follow-up ACL SAVE can
	// authenticate with the password that is now active.
	if _, err := d.runRedisCommand(ctx, serviceID, acl.SaveCommand(), ""); err != nil {
		return databases.Secret{Value: password}, err
	}
	return databases.Secret{Value: password}, nil
}

func (d *Docker) RevealRedisPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	if _, _, err := d.databaseEngine(serviceID); err != nil {
		return databases.Secret{}, err
	}
	value, err := d.readDatabaseSecret(serviceID, userID)
	if err != nil {
		return databases.Secret{}, err
	}
	return databases.Secret{Value: value}, nil
}

// EnableRedisACLFile makes ACL state durable. It is idempotent: a config that
// already contains an aclfile directive is left alone and Redis is not
// restarted a second time.
func (d *Docker) EnableRedisACLFile(ctx context.Context, serviceID string) error {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return err
	}
	if engine.ID() != model.DatabaseRedis {
		return databases.ErrUnsupported
	}
	lock := d.databaseLock(string(service.ID))
	lock.Lock()
	defer lock.Unlock()
	if err := d.requireDatabaseActive(ctx, serviceID); err != nil {
		return err
	}
	serviceRoot := filepath.Join(d.root, "services", string(service.ID))
	configDir := filepath.Join(serviceRoot, "config")
	configPath := filepath.Join(configDir, "redis.conf")
	original, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		original = []byte("# Generated by devstack.\n")
	} else if err != nil {
		return fmt.Errorf("agentdocker: read redis config: %w", err)
	}
	if redisConfigHasACLFile(original) {
		return nil
	}
	if err := ensureRealDirectory(configDir, 0o755); err != nil {
		return fmt.Errorf("agentdocker: prepare redis config: %w", err)
	}
	backupPath := filepath.Join(configDir, "redis.conf.bak."+backupTimestamp())
	if err := writeDatabaseFileAtomic(backupPath, original, 0o600); err != nil {
		return fmt.Errorf("agentdocker: back up redis config: %w", err)
	}
	updated := append(append([]byte{}, original...), []byte("\naclfile /data/users.acl\n")...)
	if err := writeDatabaseFileAtomic(configPath, updated, 0o644); err != nil {
		return fmt.Errorf("agentdocker: write redis config: %w", err)
	}
	acl := dbengine.NewRedisACL()
	output, err := d.runRedisCommand(ctx, serviceID, acl.ListUsersCommand(), "")
	if err != nil {
		_ = writeDatabaseFileAtomic(configPath, original, 0o644)
		return fmt.Errorf("agentdocker: seed redis ACL file: %w", err)
	}
	dataDir := filepath.Join(serviceRoot, "data")
	if err := ensureRealDirectory(dataDir, 0o755); err != nil {
		_ = writeDatabaseFileAtomic(configPath, original, 0o644)
		return fmt.Errorf("agentdocker: prepare redis data: %w", err)
	}
	if len(output) == 0 || output[len(output)-1] != '\n' {
		output = append(output, '\n')
	}
	if err := writeDatabaseFileAtomic(filepath.Join(dataDir, "users.acl"), output, 0o600); err != nil {
		_ = writeDatabaseFileAtomic(configPath, original, 0o644)
		return fmt.Errorf("agentdocker: write redis ACL file: %w", err)
	}
	if _, err := d.compose(ctx, "restart", string(service.ID)); err != nil {
		_ = writeDatabaseFileAtomic(configPath, original, 0o644)
		return fmt.Errorf("agentdocker: restart redis with ACL file: %w", err)
	}
	return nil
}

func (d *Docker) runRedisCommand(ctx context.Context, serviceID string, command []string, secretArg string) ([]byte, error) {
	engine, service, err := d.databaseEngine(serviceID)
	if err != nil {
		return nil, err
	}
	if engine.ID() != model.DatabaseRedis {
		return nil, databases.ErrUnsupported
	}
	auth, err := d.redisAuthPassword(serviceID)
	if err != nil {
		return nil, err
	}
	input := auth + "\n" + secretArg
	runner, ok := d.runner.(InputRunner)
	if !ok {
		return nil, errors.New("agentdocker: runner does not support stdin")
	}
	args := []string{
		"compose", "--project-name", projectName, "--file", d.composePath,
		"exec", "-T", string(service.ID), "sh", "-c", redisShellWrapper, "sh",
	}
	args = append(args, command...)
	return runner.RunInput(ctx, []byte(input), "docker", args...)
}

func (d *Docker) redisAuthPassword(serviceID string) (string, error) {
	service, err := d.service(serviceID)
	if err != nil {
		return "", err
	}
	value, err := d.readDatabaseSecret(serviceID, databaseUserID(string(service.ID), "default", ""))
	if errors.Is(err, databases.ErrPasswordMissing) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

func redisCommandWithSecret(command []string) ([]string, string, error) {
	if len(command) < 2 {
		return nil, "", errors.New("agentdocker: invalid redis command")
	}
	secret := command[len(command)-1]
	if !strings.HasPrefix(secret, ">") {
		return nil, "", errors.New("agentdocker: redis password must be the last argument")
	}
	out := []string{command[0], "-x"}
	out = append(out, command[1:len(command)-1]...)
	return out, secret, nil
}

func parseRedisACLUsers(output []byte) []model.DatabaseUser {
	var users []model.DatabaseUser
	for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "user" {
			continue
		}
		user := model.DatabaseUser{Username: fields[1], AccessLevel: "custom"}
		for _, field := range fields[2:] {
			switch field {
			case "on":
				user.Enabled = true
			case "off":
				user.Enabled = false
			case "+@all":
				user.AccessLevel = "full"
			case "+@read":
				if user.AccessLevel != "full" {
					user.AccessLevel = "read-only"
				}
			}
		}
		users = append(users, user)
	}
	return users
}

func normalizeRedisAccessLevel(accessLevel string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(accessLevel)) {
	case "", "read-only", "read_only", "readonly":
		return "read-only", nil
	case "full":
		return "full", nil
	default:
		return "", fmt.Errorf("%w: unknown redis access level %q", databases.ErrInvalidRequest, accessLevel)
	}
}

func redisConfigHasACLFile(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "aclfile ") {
			return true
		}
	}
	return false
}

func writeDatabaseFileAtomic(name string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), ".database-file-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(name)
	}
	return os.Rename(tmpName, name)
}
