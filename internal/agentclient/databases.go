package agentclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
)

// DatabaseAPI is the typed database surface the daemon lane consumes.
type DatabaseAPI interface {
	ListDatabases(ctx context.Context, serviceID string) ([]dbproto.DatabaseInfo, error)
	CreateDatabase(ctx context.Context, serviceID string, req dbproto.DatabaseCreateRequest) (dbproto.DatabaseInfo, error)
	RenameDatabase(ctx context.Context, serviceID, name string, req dbproto.DatabaseRenameRequest) (dbproto.DatabaseInfo, error)
	DropDatabase(ctx context.Context, serviceID, name string, req dbproto.DatabaseDropRequest) error
	ListUsers(ctx context.Context, serviceID string) ([]dbproto.UserInfo, error)
	CreateUser(ctx context.Context, serviceID string, req dbproto.UserCreateRequest) (dbproto.UserInfo, error)
	UpdateUser(ctx context.Context, serviceID, userID string, req dbproto.UserUpdateRequest) (dbproto.UserInfo, error)
	DeleteUser(ctx context.Context, serviceID, userID string, req dbproto.UserDeleteRequest) error
	SetUserPassword(ctx context.Context, serviceID, userID string) (dbproto.SecretValue, error)
	RevealUserPassword(ctx context.Context, serviceID, userID string) (dbproto.SecretValue, error)
	SetRedisPassword(ctx context.Context, serviceID string) (dbproto.SecretValue, error)
	EnableRedisACLFile(ctx context.Context, serviceID string) error
	ListBackups(ctx context.Context, serviceID string) ([]dbproto.BackupInfo, error)
	CreateBackup(ctx context.Context, serviceID, database string, req dbproto.BackupCreateRequest) (dbproto.JobInfo, error)
	DeleteBackup(ctx context.Context, serviceID, backupID string) error
	RestoreBackup(ctx context.Context, serviceID, backupID string, req dbproto.BackupRestoreRequest) (dbproto.JobInfo, error)
	ImportDatabase(ctx context.Context, serviceID, database string, file io.Reader, filename string, req dbproto.DatabaseImportRequest) (dbproto.JobInfo, error)
	ExportDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error)
	DownloadBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error)
	GetJob(ctx context.Context, jobID string) (dbproto.JobInfo, error)
	CancelJob(ctx context.Context, jobID string) error
	ResolveSiteEnv(ctx context.Context, serviceID string, req dbproto.SiteEnvRequest) (map[string]string, error)
}

var _ DatabaseAPI = (*Client)(nil)

func (c *Client) ListDatabases(ctx context.Context, serviceID string) ([]dbproto.DatabaseInfo, error) {
	var out dbproto.DatabaseListResponse
	if err := c.do(ctx, http.MethodGet, dbproto.ServiceDatabases(serviceID), nil, &out); err != nil {
		return nil, err
	}
	return out.Databases, nil
}

func (c *Client) CreateDatabase(ctx context.Context, serviceID string, req dbproto.DatabaseCreateRequest) (dbproto.DatabaseInfo, error) {
	var out dbproto.DatabaseResponse
	if err := c.do(ctx, http.MethodPost, dbproto.ServiceDatabases(serviceID), req, &out); err != nil {
		return dbproto.DatabaseInfo{}, err
	}
	return out.Database, nil
}

func (c *Client) RenameDatabase(ctx context.Context, serviceID, name string, req dbproto.DatabaseRenameRequest) (dbproto.DatabaseInfo, error) {
	var out dbproto.DatabaseResponse
	if err := c.do(ctx, http.MethodPatch, dbproto.ServiceDatabase(serviceID, name), req, &out); err != nil {
		return dbproto.DatabaseInfo{}, err
	}
	return out.Database, nil
}

func (c *Client) DropDatabase(ctx context.Context, serviceID, name string, req dbproto.DatabaseDropRequest) error {
	return c.do(ctx, http.MethodDelete, dbproto.ServiceDatabase(serviceID, name), req, nil)
}

func (c *Client) ListUsers(ctx context.Context, serviceID string) ([]dbproto.UserInfo, error) {
	var out dbproto.UserListResponse
	if err := c.do(ctx, http.MethodGet, dbproto.ServiceUsers(serviceID), nil, &out); err != nil {
		return nil, err
	}
	return out.Users, nil
}

func (c *Client) CreateUser(ctx context.Context, serviceID string, req dbproto.UserCreateRequest) (dbproto.UserInfo, error) {
	var out dbproto.UserResponse
	if err := c.do(ctx, http.MethodPost, dbproto.ServiceUsers(serviceID), req, &out); err != nil {
		return dbproto.UserInfo{}, err
	}
	return out.User, nil
}

func (c *Client) UpdateUser(ctx context.Context, serviceID, userID string, req dbproto.UserUpdateRequest) (dbproto.UserInfo, error) {
	var out dbproto.UserResponse
	if err := c.do(ctx, http.MethodPatch, dbproto.ServiceUser(serviceID, userID), req, &out); err != nil {
		return dbproto.UserInfo{}, err
	}
	return out.User, nil
}

func (c *Client) DeleteUser(ctx context.Context, serviceID, userID string, req dbproto.UserDeleteRequest) error {
	return c.do(ctx, http.MethodDelete, dbproto.ServiceUser(serviceID, userID), req, nil)
}

func (c *Client) SetUserPassword(ctx context.Context, serviceID, userID string) (dbproto.SecretValue, error) {
	var out dbproto.SecretResponse
	err := c.do(ctx, http.MethodPost, dbproto.ServiceUserPassword(serviceID, userID), dbproto.UserPasswordRequest{ID: serviceID, UserID: userID}, &out)
	return dbproto.SecretValue{Value: out.Value}, err
}

func (c *Client) RevealUserPassword(ctx context.Context, serviceID, userID string) (dbproto.SecretValue, error) {
	var out dbproto.SecretResponse
	err := c.do(ctx, http.MethodPost, dbproto.ServiceUserReveal(serviceID, userID), dbproto.UserRevealRequest{ID: serviceID, UserID: userID}, &out)
	return dbproto.SecretValue{Value: out.Value}, err
}

// SetRedisPassword rotates the Redis default user's password. The generic
// SetUserPassword also dispatches the default user here.
func (c *Client) SetRedisPassword(ctx context.Context, serviceID string) (dbproto.SecretValue, error) {
	var out dbproto.SecretResponse
	err := c.do(ctx, http.MethodPost, serviceRedisPasswordPath(serviceID), dbproto.UserPasswordRequest{ID: serviceID}, &out)
	return dbproto.SecretValue{Value: out.Value}, err
}

// EnableRedisACLFile runs the one-time Redis ACL file migration.
func (c *Client) EnableRedisACLFile(ctx context.Context, serviceID string) error {
	return c.do(ctx, http.MethodPost, serviceRedisACLFilePath(serviceID), nil, nil)
}

func (c *Client) ListBackups(ctx context.Context, serviceID string) ([]dbproto.BackupInfo, error) {
	var out dbproto.BackupListResponse
	if err := c.do(ctx, http.MethodGet, serviceBackupsPath(serviceID), nil, &out); err != nil {
		return nil, err
	}
	return out.Backups, nil
}

func (c *Client) CreateBackup(ctx context.Context, serviceID, database string, req dbproto.BackupCreateRequest) (dbproto.JobInfo, error) {
	var out dbproto.JobResponse
	if err := c.doLong(ctx, http.MethodPost, dbproto.ServiceDatabaseBackups(serviceID, database), req, &out); err != nil {
		return dbproto.JobInfo{}, err
	}
	return out.Job, nil
}

func (c *Client) DeleteBackup(ctx context.Context, serviceID, backupID string) error {
	return c.do(ctx, http.MethodDelete, dbproto.ServiceBackup(serviceID, backupID), nil, nil)
}

func (c *Client) RestoreBackup(ctx context.Context, serviceID, backupID string, req dbproto.BackupRestoreRequest) (dbproto.JobInfo, error) {
	var out dbproto.JobResponse
	if err := c.doLong(ctx, http.MethodPost, dbproto.ServiceBackupRestore(serviceID, backupID), req, &out); err != nil {
		return dbproto.JobInfo{}, err
	}
	return out.Job, nil
}

func (c *Client) ImportDatabase(ctx context.Context, serviceID, database string, file io.Reader, filename string, req dbproto.DatabaseImportRequest) (dbproto.JobInfo, error) {
	query := url.Values{}
	if req.Mode != "" {
		query.Set(dbproto.QueryMode, req.Mode)
	}
	if req.Confirm != "" {
		query.Set(dbproto.QueryConfirm, req.Confirm)
	}
	if req.PreImportBackup {
		query.Set(dbproto.QueryPreImportBackup, "true")
	}
	path := dbproto.ServiceDatabaseImport(serviceID, database)
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	contentType := "text/plain"
	if strings.HasSuffix(strings.ToLower(filename), ".gz") {
		contentType = "application/gzip"
	}
	var out dbproto.JobResponse
	if err := c.doRaw(ctx, http.MethodPost, path, file, contentType, filename, &out); err != nil {
		return dbproto.JobInfo{}, err
	}
	return out.Job, nil
}

func (c *Client) ExportDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error) {
	return c.open(ctx, http.MethodGet, dbproto.ServiceDatabaseExport(serviceID, database), nil)
}

func (c *Client) DownloadBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error) {
	return c.open(ctx, http.MethodGet, dbproto.ServiceBackupDownload(serviceID, backupID), nil)
}

// GetJob and CancelJob are daemon-owned in this architecture. The agent runs
// backup/restore/import synchronously and returns a completed job, so a job
// lookup against the agent is always missing.
func (c *Client) GetJob(context.Context, string) (dbproto.JobInfo, error) {
	return dbproto.JobInfo{}, fmt.Errorf("agentclient: jobs are owned by the daemon: %w", databases.ErrJobNotFound)
}

func (c *Client) CancelJob(context.Context, string) error {
	return fmt.Errorf("agentclient: jobs are owned by the daemon: %w", databases.ErrJobNotFound)
}

func (c *Client) ResolveSiteEnv(ctx context.Context, serviceID string, req dbproto.SiteEnvRequest) (map[string]string, error) {
	var out dbproto.SiteEnvResponse
	if err := c.do(ctx, http.MethodPost, dbproto.ServiceDatabaseEnv(serviceID), req, &out); err != nil {
		return nil, err
	}
	return out.Env, nil
}

func (c *Client) doRaw(ctx context.Context, method, path string, body io.Reader, contentType, filename string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("agentclient: request %s: %w", path, err)
	}
	req.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	req.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if filename != "" {
		req.Header.Set("X-Devstack-Filename", filename)
	}
	resp, err := c.long.Do(req)
	if err != nil {
		if isTimeout(err) {
			return fmt.Errorf("%w: %v", agentproto.ErrTimeout, err)
		}
		return fmt.Errorf("agentclient: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("agentclient: read %s: %w", path, err)
	}
	if len(data) > maxBody {
		return fmt.Errorf("agentclient: %s response exceeds %d bytes", path, maxBody)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return wireError(resp.StatusCode, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("agentclient: decode %s: %w", path, err)
		}
	}
	return nil
}

func serviceBackupsPath(serviceID string) string {
	return "/v1/services/" + url.PathEscape(serviceID) + "/backups"
}

func serviceRedisPasswordPath(serviceID string) string {
	return "/v1/services/" + url.PathEscape(serviceID) + "/password"
}

func serviceRedisACLFilePath(serviceID string) string {
	return "/v1/services/" + url.PathEscape(serviceID) + "/redis/acl-file"
}
