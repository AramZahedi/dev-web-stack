package helperclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

// DatabaseAPI is the typed privileged database surface exposed by the helper.
// It mirrors agentclient.DatabaseAPI so the daemon can share mapping code.
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
	var resp dbproto.DatabaseListResponse
	err := c.do(ctx, http.MethodGet, dbproto.ServiceDatabases(serviceID), nil, &resp)
	return resp.Databases, err
}

func (c *Client) CreateDatabase(ctx context.Context, serviceID string, req dbproto.DatabaseCreateRequest) (dbproto.DatabaseInfo, error) {
	req.ID = serviceID
	var resp dbproto.DatabaseResponse
	err := c.do(ctx, http.MethodPost, dbproto.ServiceDatabases(serviceID), req, &resp)
	return resp.Database, err
}

// GetDatabase returns one database by name. It is a concrete helper method,
// not part of DatabaseAPI, so the agent/helper interfaces stay identical.
func (c *Client) GetDatabase(ctx context.Context, serviceID, name string) (dbproto.DatabaseInfo, error) {
	var resp dbproto.DatabaseResponse
	err := c.do(ctx, http.MethodGet, dbproto.ServiceDatabase(serviceID, name), nil, &resp)
	return resp.Database, err
}

// SetServicePassword rotates the Redis default-user password.
func (c *Client) SetServicePassword(ctx context.Context, serviceID string) (dbproto.SecretValue, error) {
	var resp dbproto.SecretResponse
	err := c.do(ctx, http.MethodPost, helperproto.ServicePassword(serviceID), nil, &resp)
	return dbproto.SecretValue{Value: resp.Value}, err
}

func (c *Client) RenameDatabase(ctx context.Context, serviceID, name string, req dbproto.DatabaseRenameRequest) (dbproto.DatabaseInfo, error) {
	req.ID = serviceID
	req.Name = name
	var resp dbproto.DatabaseResponse
	err := c.do(ctx, http.MethodPatch, dbproto.ServiceDatabase(serviceID, name), req, &resp)
	return resp.Database, err
}

func (c *Client) DropDatabase(ctx context.Context, serviceID, name string, req dbproto.DatabaseDropRequest) error {
	req.ID = serviceID
	req.Name = name
	return c.do(ctx, http.MethodDelete, dbproto.ServiceDatabase(serviceID, name), req, nil)
}

func (c *Client) ListUsers(ctx context.Context, serviceID string) ([]dbproto.UserInfo, error) {
	var resp dbproto.UserListResponse
	err := c.do(ctx, http.MethodGet, dbproto.ServiceUsers(serviceID), nil, &resp)
	return resp.Users, err
}

func (c *Client) CreateUser(ctx context.Context, serviceID string, req dbproto.UserCreateRequest) (dbproto.UserInfo, error) {
	req.ID = serviceID
	var resp dbproto.UserResponse
	err := c.do(ctx, http.MethodPost, dbproto.ServiceUsers(serviceID), req, &resp)
	return resp.User, err
}

func (c *Client) UpdateUser(ctx context.Context, serviceID, userID string, req dbproto.UserUpdateRequest) (dbproto.UserInfo, error) {
	req.ID = serviceID
	req.UserID = userID
	var resp dbproto.UserResponse
	err := c.do(ctx, http.MethodPatch, dbproto.ServiceUser(serviceID, userID), req, &resp)
	return resp.User, err
}

func (c *Client) DeleteUser(ctx context.Context, serviceID, userID string, req dbproto.UserDeleteRequest) error {
	req.ID = serviceID
	req.UserID = userID
	return c.do(ctx, http.MethodDelete, dbproto.ServiceUser(serviceID, userID), req, nil)
}

func (c *Client) SetUserPassword(ctx context.Context, serviceID, userID string) (dbproto.SecretValue, error) {
	req := dbproto.UserPasswordRequest{ID: serviceID, UserID: userID}
	var resp dbproto.SecretResponse
	err := c.do(ctx, http.MethodPost, dbproto.ServiceUserPassword(serviceID, userID), req, &resp)
	return dbproto.SecretValue{Value: resp.Value}, err
}

func (c *Client) RevealUserPassword(ctx context.Context, serviceID, userID string) (dbproto.SecretValue, error) {
	req := dbproto.UserRevealRequest{ID: serviceID, UserID: userID}
	var resp dbproto.SecretResponse
	err := c.do(ctx, http.MethodPost, dbproto.ServiceUserReveal(serviceID, userID), req, &resp)
	return dbproto.SecretValue{Value: resp.Value}, err
}

func (c *Client) ListBackups(ctx context.Context, serviceID string) ([]dbproto.BackupInfo, error) {
	var resp dbproto.BackupListResponse
	err := c.do(ctx, http.MethodGet, helperproto.ServiceBackups(serviceID), nil, &resp)
	return resp.Backups, err
}

func (c *Client) CreateBackup(ctx context.Context, serviceID, database string, req dbproto.BackupCreateRequest) (dbproto.JobInfo, error) {
	req.ID = serviceID
	req.Database = database
	var resp dbproto.JobResponse
	err := c.do(ctx, http.MethodPost, dbproto.ServiceDatabaseBackups(serviceID, database), req, &resp)
	return resp.Job, err
}

func (c *Client) DeleteBackup(ctx context.Context, serviceID, backupID string) error {
	req := dbproto.BackupDeleteRequest{ID: serviceID, BackupID: backupID}
	return c.do(ctx, http.MethodDelete, dbproto.ServiceBackup(serviceID, backupID), req, nil)
}

func (c *Client) RestoreBackup(ctx context.Context, serviceID, backupID string, req dbproto.BackupRestoreRequest) (dbproto.JobInfo, error) {
	req.ID = serviceID
	req.BackupID = backupID
	var resp dbproto.JobResponse
	err := c.do(ctx, http.MethodPost, dbproto.ServiceBackupRestore(serviceID, backupID), req, &resp)
	return resp.Job, err
}

func (c *Client) ImportDatabase(ctx context.Context, serviceID, database string, file io.Reader, filename string, req dbproto.DatabaseImportRequest) (dbproto.JobInfo, error) {
	req.ID = serviceID
	req.Database = database
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
	headers := map[string]string{"X-Devstack-Filename": filename}
	var resp dbproto.JobResponse
	err := c.doRaw(ctx, http.MethodPost, path, file, "application/octet-stream", headers, &resp)
	return resp.Job, err
}

func (c *Client) ExportDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error) {
	return c.openStream(ctx, dbproto.ServiceDatabaseExport(serviceID, database))
}

func (c *Client) DownloadBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error) {
	return c.openStream(ctx, dbproto.ServiceBackupDownload(serviceID, backupID))
}

// GetJob reports that the helper has no asynchronous job. Helper database
// operations complete synchronously and return their final job in the mutation
// response; the daemon only polls jobs on the agent transport.
func (c *Client) GetJob(ctx context.Context, jobID string) (dbproto.JobInfo, error) {
	return dbproto.JobInfo{}, fmt.Errorf("%w: %s", databases.ErrJobNotFound, jobID)
}

// CancelJob reports that the helper has no asynchronous job to cancel.
func (c *Client) CancelJob(ctx context.Context, jobID string) error {
	return fmt.Errorf("%w: %s", databases.ErrJobNotFound, jobID)
}

func (c *Client) ResolveSiteEnv(ctx context.Context, serviceID string, req dbproto.SiteEnvRequest) (map[string]string, error) {
	req.ServiceID = serviceID
	var resp dbproto.SiteEnvResponse
	err := c.do(ctx, http.MethodPost, dbproto.ServiceDatabaseEnv(serviceID), req, &resp)
	return resp.Env, err
}

func (c *Client) doRaw(ctx context.Context, method, path string, body io.Reader, contentType string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return fmt.Errorf("helperclient: build %s request: %w", path, err)
	}
	req.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("helperclient: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return decodeError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		return fmt.Errorf("helperclient: decode %s response: %w", path, err)
	}
	return nil
}

func (c *Client) openStream(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("helperclient: build %s request: %w", path, err)
	}
	req.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("helperclient: GET %s: %w", path, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer func() { _ = resp.Body.Close() }()
		return nil, decodeError(resp)
	}
	return resp.Body, nil
}
