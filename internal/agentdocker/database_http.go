package agentdocker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func (d *Docker) handleDatabaseList(w http.ResponseWriter, r *http.Request) {
	list, err := d.ListDatabases(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.DatabaseListResponse{Databases: list})
}

func (d *Docker) handleDatabaseCreate(w http.ResponseWriter, r *http.Request) {
	var request dbproto.DatabaseCreateRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	if request.ID != r.PathValue("id") {
		writeDatabaseError(w, fmt.Errorf("%w: service ID does not match URL", databases.ErrInvalidRequest))
		return
	}
	created, err := d.CreateDatabase(r.Context(), request.ID, databases.CreateDatabaseRequest{
		Name: request.Name, Charset: request.Charset, Collation: request.Collation,
	})
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.DatabaseResponse{Database: created})
}

func (d *Docker) handleDatabaseRename(w http.ResponseWriter, r *http.Request) {
	var request dbproto.DatabaseRenameRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	if request.ID != r.PathValue("id") || request.Name != r.PathValue("name") {
		writeDatabaseError(w, fmt.Errorf("%w: database does not match URL", databases.ErrInvalidRequest))
		return
	}
	renamed, err := d.RenameDatabase(r.Context(), request.ID, request.Name, databases.RenameDatabaseRequest{Name: request.NewName})
	if err != nil {
		if errors.Is(err, dbengine.ErrUnsupported) {
			writeJSON(w, http.StatusBadRequest, agentproto.ErrorResponse{Code: agentproto.CodeUnsupported, Message: err.Error()})
			return
		}
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.DatabaseResponse{Database: renamed})
}

func (d *Docker) handleDatabaseDrop(w http.ResponseWriter, r *http.Request) {
	var request dbproto.DatabaseDropRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	if request.ID != r.PathValue("id") || request.Name != r.PathValue("name") {
		writeDatabaseError(w, fmt.Errorf("%w: database does not match URL", databases.ErrInvalidRequest))
		return
	}
	if err := d.DropDatabase(r.Context(), request.ID, request.Name, databases.DropDatabaseRequest{Confirm: request.Confirm}); err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.DeleteResponse{Deleted: true})
}

func (d *Docker) handleUserList(w http.ResponseWriter, r *http.Request) {
	users, err := d.ListUsers(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.UserListResponse{Users: users})
}

func (d *Docker) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	var request dbproto.UserCreateRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	if request.ID != r.PathValue("id") {
		writeDatabaseError(w, fmt.Errorf("%w: service ID does not match URL", databases.ErrInvalidRequest))
		return
	}
	user, err := d.CreateUser(r.Context(), request.ID, databases.CreateUserRequest{
		Username: request.Username, Host: request.Host, Grants: request.Grants, AccessLevel: request.AccessLevel,
	})
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.UserResponse{User: user})
}

func (d *Docker) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	var request dbproto.UserUpdateRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	if request.ID != r.PathValue("id") || request.UserID != r.PathValue("userID") {
		writeDatabaseError(w, fmt.Errorf("%w: user does not match URL", databases.ErrInvalidRequest))
		return
	}
	user, err := d.UpdateUser(r.Context(), request.ID, request.UserID, databases.UpdateUserRequest{
		Enabled: request.Enabled, Grants: request.Grants, AccessLevel: request.AccessLevel,
	})
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.UserResponse{User: user})
}

func (d *Docker) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	var request dbproto.UserDeleteRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	if request.ID != r.PathValue("id") || request.UserID != r.PathValue("userID") {
		writeDatabaseError(w, fmt.Errorf("%w: user does not match URL", databases.ErrInvalidRequest))
		return
	}
	if err := d.DeleteUser(r.Context(), request.ID, request.UserID, databases.DeleteUserRequest{
		Confirm: request.Confirm, ReassignOwned: request.ReassignOwned,
	}); err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.DeleteResponse{Deleted: true})
}

func (d *Docker) handleUserPassword(w http.ResponseWriter, r *http.Request) {
	var request dbproto.UserPasswordRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	if request.ID != r.PathValue("id") || request.UserID != r.PathValue("userID") {
		writeDatabaseError(w, fmt.Errorf("%w: user does not match URL", databases.ErrInvalidRequest))
		return
	}
	secret, err := d.SetUserPassword(r.Context(), request.ID, request.UserID)
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, dbproto.SecretResponse{Value: secret.Value})
}

func (d *Docker) handleRedisPassword(w http.ResponseWriter, r *http.Request) {
	var request dbproto.UserPasswordRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil && !errors.Is(err, io.EOF) {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	if request.ID != "" && request.ID != r.PathValue("id") {
		writeDatabaseError(w, fmt.Errorf("%w: service ID does not match URL", databases.ErrInvalidRequest))
		return
	}
	secret, err := d.SetRedisPassword(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, dbproto.SecretResponse{Value: secret.Value})
}

func (d *Docker) handleRedisEnableACLFile(w http.ResponseWriter, r *http.Request) {
	if err := d.EnableRedisACLFile(r.Context(), r.PathValue("id")); err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": true})
}

func (d *Docker) handleUserReveal(w http.ResponseWriter, r *http.Request) {
	var request dbproto.UserRevealRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	if request.ID != r.PathValue("id") || request.UserID != r.PathValue("userID") {
		writeDatabaseError(w, fmt.Errorf("%w: user does not match URL", databases.ErrInvalidRequest))
		return
	}
	secret, err := d.RevealUserPassword(r.Context(), request.ID, request.UserID)
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, dbproto.SecretResponse{Value: secret.Value})
}

func (d *Docker) handleBackupList(w http.ResponseWriter, r *http.Request) {
	backups, err := d.ListBackups(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.BackupListResponse{Backups: backups})
}

func (d *Docker) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	var request dbproto.BackupCreateRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	if request.ID != r.PathValue("id") || request.Database != r.PathValue("name") {
		writeDatabaseError(w, fmt.Errorf("%w: database does not match URL", databases.ErrInvalidRequest))
		return
	}
	job, err := d.CreateBackup(r.Context(), request.ID, request.Database, databases.CreateBackupRequest{
		Kind: model.DatabaseBackupKind(request.Kind),
	})
	writeDatabaseJob(w, job, err)
}

func (d *Docker) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	body, err := d.DownloadBackup(r.Context(), r.PathValue("id"), r.PathValue("backupID"))
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	filename := path.Base(r.PathValue("backupID"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	streamDatabaseBody(w, body)
}

func (d *Docker) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	if err := d.DeleteBackup(r.Context(), r.PathValue("id"), r.PathValue("backupID")); err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.DeleteResponse{Deleted: true})
}

func (d *Docker) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	var request dbproto.BackupRestoreRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	if request.ID != r.PathValue("id") || request.BackupID != r.PathValue("backupID") {
		writeDatabaseError(w, fmt.Errorf("%w: backup does not match URL", databases.ErrInvalidRequest))
		return
	}
	job, err := d.RestoreBackup(r.Context(), request.ID, request.BackupID, databases.RestoreBackupRequest{
		Target: request.Target, Overwrite: request.Overwrite, Confirm: request.Confirm, PreRestoreBackup: request.PreRestoreBackup,
	})
	writeDatabaseJob(w, job, err)
}

func (d *Docker) handleDatabaseImport(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filename := r.Header.Get("X-Devstack-Filename")
	if strings.TrimSpace(filename) == "" {
		filename = r.PathValue("name") + ".sql"
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDatabaseImportBytes+1)
	job, err := d.ImportDatabase(r.Context(), r.PathValue("id"), r.PathValue("name"), r.Body, filename, databases.ImportRequest{
		Mode:            databases.ImportMode(query.Get(dbproto.QueryMode)),
		Confirm:         query.Get(dbproto.QueryConfirm),
		PreImportBackup: queryBool(query.Get(dbproto.QueryPreImportBackup)),
	})
	writeDatabaseJob(w, job, err)
}

func (d *Docker) handleDatabaseExport(w http.ResponseWriter, r *http.Request) {
	body, err := d.ExportDatabase(r.Context(), r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/sql")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.PathValue("name")+`.sql"`)
	w.WriteHeader(http.StatusOK)
	streamDatabaseBody(w, body)
}

func (d *Docker) handleDatabaseEnv(w http.ResponseWriter, r *http.Request) {
	var request dbproto.SiteEnvRequest
	if err := decodeDatabaseRequest(w, r, &request); err != nil {
		writeDatabaseError(w, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err))
		return
	}
	serviceID := r.PathValue("id")
	if request.ServiceID != "" && !sameDatabaseService(request.ServiceID, serviceID) {
		writeDatabaseError(w, fmt.Errorf("%w: service ID does not match URL", databases.ErrInvalidRequest))
		return
	}
	env, err := d.resolveSiteEnv(r.Context(), serviceID, request.Links)
	if err != nil {
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.SiteEnvResponse{Env: env})
}

func (d *Docker) resolveSiteEnv(ctx context.Context, serviceID string, links []dbproto.LinkInfo) (map[string]string, error) {
	env := make(map[string]string)
	for index, link := range links {
		if strings.TrimSpace(link.Database) == "" {
			continue
		}
		if err := dbengine.ValidateIdentifier(link.Database); err != nil {
			return nil, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err)
		}
		linkService := strings.TrimSpace(link.ServiceID)
		if linkService != "" && !sameDatabaseService(linkService, serviceID) {
			return nil, fmt.Errorf("%w: link service %q does not match %q", databases.ErrInvalidRequest, linkService, serviceID)
		}
		expectedEngine := databaseEngineForService(serviceID)
		engine := link.Engine
		if engine == "" {
			engine = expectedEngine
		}
		if expectedEngine == "" || engine != expectedEngine {
			return nil, fmt.Errorf("%w: link engine %q does not match service %q", databases.ErrInvalidRequest, engine, serviceID)
		}
		if err := dbengine.ValidateExistingUsername(engine, link.Username); err != nil {
			return nil, fmt.Errorf("%w: %w", databases.ErrInvalidRequest, err)
		}
		port := link.Connection.Port
		if port <= 0 || port > 65535 {
			port = defaultDatabasePort(engine)
		}
		prefix := "DB_"
		if !link.Primary {
			slug := databaseEnvSlug(link.Database)
			if slug == "" {
				slug = "LINK" + strconv.Itoa(index+1)
			}
			prefix = "DB_" + slug + "_"
		}
		env[prefix+"CONNECTION"] = databaseConnectionName(engine)
		env[prefix+"HOST"] = "127.0.0.1"
		env[prefix+"PORT"] = strconv.Itoa(port)
		env[prefix+"DATABASE"] = link.Database
		env[prefix+"USERNAME"] = link.Username
		if link.PasswordManaged {
			if strings.TrimSpace(link.UserID) == "" {
				return nil, fmt.Errorf("%w: managed link has no user ID", databases.ErrInvalidRequest)
			}
			secret, err := d.readDatabaseSecret(serviceID, link.UserID)
			if err != nil {
				return nil, err
			}
			env[prefix+"PASSWORD"] = secret
			connectionURL := databaseConnectionURL(engine, link.Username, secret, port, link.Database)
			if link.Primary {
				env["DATABASE_URL"] = connectionURL
			} else {
				env[prefix+"URL"] = connectionURL
			}
		} else {
			delete(env, prefix+"PASSWORD")
			delete(env, prefix+"URL")
			if link.Primary {
				delete(env, "DATABASE_URL")
			}
		}
	}
	return env, nil
}

func sameDatabaseService(left, right string) bool {
	return canonicalDatabaseService(left) == canonicalDatabaseService(right)
}

func canonicalDatabaseService(id string) string {
	if id == "postgresql" {
		return "postgres"
	}
	return id
}

// databaseConnectionName is the framework-facing DB_CONNECTION value. The
// engine/catalog ID remains "postgres".
func databaseConnectionName(engine model.DatabaseEngine) string {
	if engine == model.DatabasePostgreSQL {
		return "pgsql"
	}
	return string(engine)
}

func databaseEngineForService(serviceID string) model.DatabaseEngine {
	switch canonicalDatabaseService(serviceID) {
	case "mysql":
		return model.DatabaseMySQL
	case "mariadb":
		return model.DatabaseMariaDB
	case "postgres":
		return model.DatabasePostgreSQL
	case "redis":
		return model.DatabaseRedis
	default:
		return ""
	}
}

func defaultDatabasePort(engine model.DatabaseEngine) int {
	switch engine {
	case model.DatabaseMySQL, model.DatabaseMariaDB:
		return 3306
	case model.DatabasePostgreSQL:
		return 5432
	case model.DatabaseRedis:
		return 6379
	default:
		return 0
	}
}

func databaseEnvSlug(database string) string {
	var builder strings.Builder
	for _, r := range strings.ToUpper(database) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func databaseConnectionURL(engine model.DatabaseEngine, username, password string, port int, database string) string {
	scheme := "mysql"
	switch engine {
	case model.DatabasePostgreSQL:
		scheme = "postgresql"
	case model.DatabaseRedis:
		scheme = "redis"
	}
	user := url.UserPassword(username, password)
	return scheme + "://" + user.String() + "@" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/" + url.PathEscape(database)
}

// decodeDatabaseRequest is strict: unknown fields are rejected and a
// caller-supplied password fails closed. External-password storage is deferred
// to v1.1, so silently dropping a password would be worse than rejecting it.
func decodeDatabaseRequest(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err == nil && containsPasswordField(payload) {
		return fmt.Errorf("%w: caller-supplied database passwords are not supported", databases.ErrUnsupported)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("request contains trailing JSON")
	}
	return nil
}

func containsPasswordField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.EqualFold(key, "password") || containsPasswordField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsPasswordField(child) {
				return true
			}
		}
	}
	return false
}

// streamDatabaseBody copies a dump to the response and aborts the connection
// if the child process fails or the reader is truncated. A clean 200 with a
// short body would let a client mistake a failed dump for success.
func streamDatabaseBody(w http.ResponseWriter, body io.ReadCloser) {
	_, copyErr := io.Copy(w, body)
	closeErr := body.Close()
	if copyErr != nil || closeErr != nil {
		panic(http.ErrAbortHandler)
	}
}

// writeDatabaseJob returns a failed job as a normal response when it carries a
// pre-operation backup ID, so the daemon can offer one-click recovery instead
// of losing the recovery pointer in a plain error.
func writeDatabaseJob(w http.ResponseWriter, job model.DatabaseJob, err error) {
	if err != nil {
		if job.Status == model.JobFailed && job.BackupID != "" {
			writeJSON(w, http.StatusOK, dbproto.JobResponse{Job: job})
			return
		}
		writeDatabaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dbproto.JobResponse{Job: job})
}

func queryBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func writeDatabaseError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := agentproto.CodeInvalidRequest
	switch {
	case errors.Is(err, databases.ErrNotFound), errors.Is(err, databases.ErrJobNotFound), errors.Is(err, databases.ErrPasswordMissing):
		status = http.StatusNotFound
		code = agentproto.CodeNotFound
	case errors.Is(err, databases.ErrConflict), errors.Is(err, databases.ErrLinkedSites), errors.Is(err, databases.ErrServiceInactive):
		status = http.StatusConflict
		code = agentproto.CodeConflict
	case errors.Is(err, databases.ErrProtected):
		status = http.StatusUnprocessableEntity
		code = agentproto.CodeForbidden
	case errors.Is(err, databases.ErrUnsupported), errors.Is(err, dbengine.ErrUnsupported):
		status = http.StatusUnprocessableEntity
		code = agentproto.CodeUnsupported
	}
	writeJSON(w, status, agentproto.ErrorResponse{Code: code, Message: err.Error()})
}
