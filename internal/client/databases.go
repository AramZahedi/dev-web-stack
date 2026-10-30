package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

const databasesPath = "/api/v1/services/"

func serviceDatabasePath(serviceID string) string {
	return databasesPath + url.PathEscape(serviceID) + "/databases"
}

func serviceDatabaseItemPath(serviceID, name string) string {
	return serviceDatabasePath(serviceID) + "/" + url.PathEscape(name)
}

func serviceUserPath(serviceID string) string {
	return databasesPath + url.PathEscape(serviceID) + "/users"
}

func serviceUserItemPath(serviceID, userID string) string {
	return serviceUserPath(serviceID) + "/" + url.PathEscape(userID)
}

func serviceBackupsPath(serviceID string) string {
	return databasesPath + url.PathEscape(serviceID) + "/backups"
}

func serviceBackupItemPath(serviceID, backupID string) string {
	return serviceBackupsPath(serviceID) + "/" + url.PathEscape(backupID)
}

func siteDatabasesPath(ref string) string {
	return "/api/v1/sites/" + url.PathEscape(ref) + "/databases"
}

// ListServiceDatabases returns the database inventory for one service.
func (c *Client) ListServiceDatabases(ctx context.Context, serviceID string) ([]model.Database, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodGet, serviceDatabasePath(serviceID), nil)
	if err != nil {
		return nil, err
	}
	return decodeList[model.Database](raw, "databases")
}

// CreateServiceDatabase creates one database on a service.
func (c *Client) CreateServiceDatabase(ctx context.Context, serviceID string, req databases.CreateDatabaseRequest) (model.Database, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodPost, serviceDatabasePath(serviceID), req)
	if err != nil {
		return model.Database{}, err
	}
	return decodeOne[model.Database](raw, "database")
}

// GetServiceDatabase returns one database on a service.
func (c *Client) GetServiceDatabase(ctx context.Context, serviceID, name string) (model.Database, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodGet, serviceDatabaseItemPath(serviceID, name), nil)
	if err != nil {
		return model.Database{}, err
	}
	return decodeOne[model.Database](raw, "database")
}

// RenameServiceDatabase renames a PostgreSQL database.
func (c *Client) RenameServiceDatabase(ctx context.Context, serviceID, name string, req databases.RenameDatabaseRequest) (model.Database, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodPatch, serviceDatabaseItemPath(serviceID, name), req)
	if err != nil {
		return model.Database{}, err
	}
	return decodeOne[model.Database](raw, "database")
}

// DropServiceDatabase drops one database after explicit confirmation.
func (c *Client) DropServiceDatabase(ctx context.Context, serviceID, name string, req databases.DropDatabaseRequest) error {
	return c.doJSON(ctx, http.MethodDelete, serviceDatabaseItemPath(serviceID, name), req, nil)
}

// ListServiceUsers returns the engine user inventory for one service.
func (c *Client) ListServiceUsers(ctx context.Context, serviceID string) ([]model.DatabaseUser, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodGet, serviceUserPath(serviceID), nil)
	if err != nil {
		return nil, err
	}
	return decodeList[model.DatabaseUser](raw, "users")
}

// CreateServiceUser creates one engine user.
func (c *Client) CreateServiceUser(ctx context.Context, serviceID string, req databases.CreateUserRequest) (model.DatabaseUser, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodPost, serviceUserPath(serviceID), req)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	return decodeOne[model.DatabaseUser](raw, "user")
}

// GetServiceUser returns one engine user.
func (c *Client) GetServiceUser(ctx context.Context, serviceID, userID string) (model.DatabaseUser, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodGet, serviceUserItemPath(serviceID, userID), nil)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	return decodeOne[model.DatabaseUser](raw, "user")
}

// UpdateServiceUser changes enabled state, grants, or Redis access level.
func (c *Client) UpdateServiceUser(ctx context.Context, serviceID, userID string, req databases.UpdateUserRequest) (model.DatabaseUser, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodPatch, serviceUserItemPath(serviceID, userID), req)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	return decodeOne[model.DatabaseUser](raw, "user")
}

// DeleteServiceUser deletes one engine user after explicit confirmation.
func (c *Client) DeleteServiceUser(ctx context.Context, serviceID, userID string, req databases.DeleteUserRequest) error {
	return c.doJSON(ctx, http.MethodDelete, serviceUserItemPath(serviceID, userID), req, nil)
}

// SetServiceUserPassword rotates one user's password. The agent generates the
// new secret; the CLI never sends a password on argv.
func (c *Client) SetServiceUserPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	var secret databases.Secret
	err := c.doJSON(ctx, http.MethodPost, serviceUserItemPath(serviceID, userID)+"/password", nil, &secret)
	return secret, err
}

// RevealServiceUserPassword reveals one user's stored password once.
func (c *Client) RevealServiceUserPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	var secret databases.Secret
	err := c.doJSON(ctx, http.MethodPost, serviceUserItemPath(serviceID, userID)+"/reveal", nil, &secret)
	return secret, err
}

// SetServicePassword sets or rotates the Redis default-user password.
func (c *Client) SetServicePassword(ctx context.Context, serviceID string) (databases.Secret, error) {
	var secret databases.Secret
	err := c.doJSON(ctx, http.MethodPost, databasesPath+url.PathEscape(serviceID)+"/password", nil, &secret)
	return secret, err
}

// ListServiceBackups returns the backup inventory for one service.
func (c *Client) ListServiceBackups(ctx context.Context, serviceID string) ([]model.DatabaseBackup, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodGet, serviceBackupsPath(serviceID), nil)
	if err != nil {
		return nil, err
	}
	return decodeList[model.DatabaseBackup](raw, "backups")
}

// CreateServiceBackup starts a logical backup job.
func (c *Client) CreateServiceBackup(ctx context.Context, serviceID, database string, req databases.CreateBackupRequest) (model.DatabaseJob, error) {
	raw, err := c.doJSONRawNoTimeout(ctx, http.MethodPost, serviceDatabaseItemPath(serviceID, database)+"/backups", req)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	return decodeOne[model.DatabaseJob](raw, "job")
}

// DeleteServiceBackup deletes one backup file and its metadata.
func (c *Client) DeleteServiceBackup(ctx context.Context, serviceID, backupID string) error {
	return c.doJSON(ctx, http.MethodDelete, serviceBackupItemPath(serviceID, backupID), nil, nil)
}

// RestoreServiceBackup starts a restore job.
func (c *Client) RestoreServiceBackup(ctx context.Context, serviceID, backupID string, req databases.RestoreBackupRequest) (model.DatabaseJob, error) {
	raw, err := c.doJSONRawNoTimeout(ctx, http.MethodPost, serviceBackupItemPath(serviceID, backupID)+"/restore", req)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	return decodeOne[model.DatabaseJob](raw, "job")
}

// DownloadServiceBackup streams one backup file.
func (c *Client) DownloadServiceBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error) {
	return c.doStream(ctx, http.MethodGet, serviceBackupItemPath(serviceID, backupID)+"/download")
}

// ImportServiceDatabase streams a SQL dump into an existing database.
func (c *Client) ImportServiceDatabase(ctx context.Context, serviceID, database string, file io.Reader, filename string, req databases.ImportRequest) (model.DatabaseJob, error) {
	fields := map[string]string{}
	if req.Mode != "" {
		fields["mode"] = string(req.Mode)
	}
	if req.Confirm != "" {
		fields["confirm"] = req.Confirm
	}
	if req.PreImportBackup {
		fields["pre_import_backup"] = "true"
	}
	resp, err := c.doMultipart(ctx, serviceDatabaseItemPath(serviceID, database)+"/import", fields, filename, file)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return model.DatabaseJob{}, fmt.Errorf("client: read import response: %w", err)
	}
	return decodeOne[model.DatabaseJob](raw, "job")
}

// ExportServiceDatabase streams a database dump.
func (c *Client) ExportServiceDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error) {
	return c.doStream(ctx, http.MethodGet, serviceDatabaseItemPath(serviceID, database)+"/export")
}

// GetDatabaseJob returns one backup/restore/import job.
func (c *Client) GetDatabaseJob(ctx context.Context, jobID string) (model.DatabaseJob, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodGet, "/api/v1/jobs/"+url.PathEscape(jobID), nil)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	return decodeOne[model.DatabaseJob](raw, "job")
}

// CancelDatabaseJob cancels a running job.
func (c *Client) CancelDatabaseJob(ctx context.Context, jobID string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/jobs/"+url.PathEscape(jobID), nil, nil)
}

// ListSiteDatabases returns one site's database links.
func (c *Client) ListSiteDatabases(ctx context.Context, ref string) ([]model.DatabaseLink, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodGet, siteDatabasesPath(ref), nil)
	if err != nil {
		return nil, err
	}
	return decodeList[model.DatabaseLink](raw, "links")
}

// LinkSiteDatabase links an existing database and user to a site.
func (c *Client) LinkSiteDatabase(ctx context.Context, ref string, req databases.LinkDatabaseRequest) (model.DatabaseLink, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodPost, siteDatabasesPath(ref), req)
	if err != nil {
		return model.DatabaseLink{}, err
	}
	return decodeOne[model.DatabaseLink](raw, "link")
}

// UnlinkSiteDatabase removes only a site link.
func (c *Client) UnlinkSiteDatabase(ctx context.Context, ref, linkID string) error {
	return c.doJSON(ctx, http.MethodDelete, siteDatabasesPath(ref)+"/"+url.PathEscape(linkID), nil, nil)
}

// CreateSiteDatabase provisions a database/user and links it to an existing
// site.
func (c *Client) CreateSiteDatabase(ctx context.Context, ref string, req databases.SiteDatabaseRequest) (databases.SiteDatabaseResult, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodPost, siteDatabasesPath(ref), req)
	if err != nil {
		return databases.SiteDatabaseResult{}, err
	}
	return decodeOne[databases.SiteDatabaseResult](raw, "result")
}

// SiteDatabaseEnv resolves the dynamic DB_*/DATABASE_URL environment for a
// site. The API marks the response no-store and audits it; callers must never
// log or persist the values.
func (c *Client) SiteDatabaseEnv(ctx context.Context, ref string) (map[string]string, error) {
	raw, err := c.doJSONRaw(ctx, http.MethodGet, "/api/v1/sites/"+url.PathEscape(ref)+"/database-env", nil)
	if err != nil {
		return nil, err
	}
	return decodeEnv(raw)
}

func (c *Client) doJSONRaw(ctx context.Context, method, path string, in any) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.doJSON(ctx, method, path, in, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) doJSONRawNoTimeout(ctx context.Context, method, path string, in any) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.doJSONNoTimeout(ctx, method, path, in, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// decodeOne accepts either a direct object or an object wrapped in key. The
// daemon contract returns direct objects for single resources, while a few
// transports wrap them; accepting both keeps the CLI compatible without
// weakening the list envelope checks.
func decodeOne[T any](raw json.RawMessage, key string) (T, error) {
	var zero T
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err == nil {
		if inner, ok := wrapper[key]; ok {
			if len(wrapper) != 1 {
				return zero, fmt.Errorf("client: %q envelope must contain exactly one key", key)
			}
			return decodeStrict[T](inner)
		}
	}
	value, err := decodeStrict[T](raw)
	if err != nil {
		return zero, fmt.Errorf("client: response is neither a %T object nor a %q envelope: %w", zero, key, err)
	}
	return value, nil
}

// decodeStrict only accepts non-empty fields defined by T, so an unexpected
// wrapper key, null, or an empty object cannot silently produce a zero-valued
// resource.
func decodeStrict[T any](raw json.RawMessage) (T, error) {
	var value T
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return value, fmt.Errorf("client: empty response body")
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return value, fmt.Errorf("client: null response body")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err == nil && compact.String() == "{}" {
		return value, fmt.Errorf("client: empty object response body")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return value, fmt.Errorf("client: response contains trailing JSON")
		}
		return value, err
	}
	return value, nil
}

// decodeList accepts either a bare array or an object wrapped in key.
func decodeList[T any](raw json.RawMessage, key string) ([]T, error) {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err == nil {
		if inner, ok := wrapper[key]; ok {
			var list []T
			if err := json.Unmarshal(inner, &list); err != nil {
				return nil, err
			}
			if list == nil {
				list = []T{}
			}
			return list, nil
		}
	}
	var list []T
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []T{}
	}
	return list, nil
}

func decodeEnv(raw json.RawMessage) (map[string]string, error) {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err == nil {
		if inner, ok := wrapper["env"]; ok {
			if string(inner) == "null" {
				return map[string]string{}, nil
			}
			var env map[string]string
			if err := json.Unmarshal(inner, &env); err != nil {
				return nil, err
			}
			if env == nil {
				env = map[string]string{}
			}
			return env, nil
		}
	}
	var env map[string]string
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if env == nil {
		env = map[string]string{}
	}
	return env, nil
}

// doMultipart streams a multipart form without buffering the file in memory.
func (c *Client) doMultipart(ctx context.Context, path string, fields map[string]string, filename string, file io.Reader) (*http.Response, error) {
	if file == nil {
		return nil, fmt.Errorf("client: multipart file is required")
	}
	if strings.TrimSpace(filename) == "" {
		filename = "import.sql"
	}
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	contentType := multipartWriter.FormDataContentType()
	go func() {
		err := func() error {
			for key, value := range fields {
				if err := multipartWriter.WriteField(key, value); err != nil {
					return err
				}
			}
			part, err := multipartWriter.CreateFormFile("file", filename)
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, file); err != nil {
				return err
			}
			return multipartWriter.Close()
		}()
		_ = writer.CloseWithError(err)
	}()
	resp, err := c.doRaw(ctx, http.MethodPost, path, reader, contentType)
	if err != nil {
		_ = reader.CloseWithError(err)
		return nil, err
	}
	return resp, nil
}
