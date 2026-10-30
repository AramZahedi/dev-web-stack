package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// databaseUnavailable writes the stable 503 response used when the daemon was
// started without a database manager.
func (s *Server) databaseUnavailable(w http.ResponseWriter) bool {
	if s.databases == nil {
		writeError(w, http.StatusServiceUnavailable, codeServiceUnavailable, "database service is not available", nil)
		return true
	}
	return false
}

func (s *Server) handleListServiceDatabases(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	list, err := s.databases.ListDatabases(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if list == nil {
		list = []model.Database{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": list})
}

func (s *Server) handleCreateServiceDatabase(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	var req databases.CreateDatabaseRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	db, err := s.databases.CreateDatabase(r.Context(), r.PathValue("id"), req)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, db)
}

func (s *Server) handleGetServiceDatabase(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	name := r.PathValue("name")
	list, err := s.databases.ListDatabases(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	for _, db := range list {
		if db.Name == name {
			writeJSON(w, http.StatusOK, db)
			return
		}
	}
	s.writeServiceError(w, r, fmt.Errorf("%w: database %q", databases.ErrNotFound, name))
}

func (s *Server) handleRenameServiceDatabase(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	var req databases.RenameDatabaseRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	db, err := s.databases.RenameDatabase(r.Context(), r.PathValue("id"), r.PathValue("name"), req)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, db)
}

func (s *Server) handleDropServiceDatabase(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	var req databases.DropDatabaseRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if err := s.databases.DropDatabase(r.Context(), r.PathValue("id"), r.PathValue("name"), req); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListServiceUsers(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	list, err := s.databases.ListUsers(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if list == nil {
		list = []model.DatabaseUser{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": list})
}

func (s *Server) handleCreateServiceUser(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	var req databases.CreateUserRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	user, err := s.databases.CreateUser(r.Context(), r.PathValue("id"), req)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) handleGetServiceUser(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	userID := r.PathValue("userID")
	list, err := s.databases.ListUsers(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	for _, user := range list {
		if user.ID == userID {
			writeJSON(w, http.StatusOK, user)
			return
		}
	}
	s.writeServiceError(w, r, fmt.Errorf("%w: user %q", databases.ErrNotFound, userID))
}

func (s *Server) handleUpdateServiceUser(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	var req databases.UpdateUserRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	user, err := s.databases.UpdateUser(r.Context(), r.PathValue("id"), r.PathValue("userID"), req)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleDeleteServiceUser(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	var req databases.DeleteUserRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if err := s.databases.DeleteUser(r.Context(), r.PathValue("id"), r.PathValue("userID"), req); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetServiceUserPassword(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	secret, err := s.databases.SetUserPassword(r.Context(), r.PathValue("id"), r.PathValue("userID"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, secret)
}

func (s *Server) handleRevealServiceUserPassword(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	secret, err := s.databases.RevealUserPassword(r.Context(), r.PathValue("id"), r.PathValue("userID"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, secret)
}

func (s *Server) handleSetServiceDefaultPassword(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	secret, err := s.databases.SetUserPassword(r.Context(), r.PathValue("id"), "default")
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, secret)
}

func (s *Server) handleListServiceBackups(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	list, err := s.databases.ListBackups(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if list == nil {
		list = []model.DatabaseBackup{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": list})
}

func (s *Server) handleCreateServiceBackup(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	var req databases.CreateBackupRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSONBody(w, r, &req) {
			return
		}
	}
	job, err := s.databases.CreateBackup(r.Context(), r.PathValue("id"), r.PathValue("name"), req)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleDeleteServiceBackup(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	if err := s.databases.DeleteBackup(r.Context(), r.PathValue("id"), r.PathValue("backupID")); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDownloadServiceBackup(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	reader, err := s.databases.DownloadBackup(r.Context(), r.PathValue("id"), r.PathValue("backupID"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	streamDownload(w, reader, r.PathValue("backupID")+".sql.gz", "application/gzip")
}

func (s *Server) handleRestoreServiceBackup(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	var req databases.RestoreBackupRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	job, err := s.databases.RestoreBackup(r.Context(), r.PathValue("id"), r.PathValue("backupID"), req)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleImportServiceDatabase(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, codePayloadTooLarge, "import file is too large", nil)
			return
		}
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "invalid multipart form: "+err.Error(), nil)
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "file is required", nil)
		return
	}
	defer func() { _ = file.Close() }()
	req := databases.ImportRequest{
		Mode:            databases.ImportMode(strings.TrimSpace(r.FormValue("mode"))),
		Confirm:         strings.TrimSpace(r.FormValue("confirm")),
		PreImportBackup: strings.EqualFold(r.FormValue("pre_import_backup"), "true"),
	}
	job, err := s.databases.ImportDatabase(r.Context(), r.PathValue("id"), r.PathValue("name"), file, header.Filename, req)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleExportServiceDatabase(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	database := r.PathValue("name")
	reader, err := s.databases.ExportDatabase(r.Context(), r.PathValue("id"), database)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	streamDownload(w, reader, database+".sql.gz", "application/gzip")
}

func (s *Server) handleGetDatabaseJob(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	job, err := s.databases.GetJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleCancelDatabaseJob(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	if err := s.databases.CancelJob(r.Context(), r.PathValue("jobID")); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListSiteDatabases(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	list, err := s.databases.ListSiteLinks(r.Context(), r.PathValue("ref"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if list == nil {
		list = []model.DatabaseLink{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": list})
}

func (s *Server) handleLinkSiteDatabase(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	var req databases.SiteDatabaseRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.NewDatabase != nil || req.NewUser != nil || strings.TrimSpace(req.Password) != "" || req.Grant != "" {
		result, err := s.databases.CreateSiteDatabase(r.Context(), r.PathValue("ref"), req)
		if err != nil {
			s.writeServiceError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
		return
	}
	link, err := s.databases.LinkDatabase(r.Context(), r.PathValue("ref"), databases.LinkDatabaseRequest{
		ServiceID: req.ServiceID,
		Database:  req.Database,
		UserID:    req.UserID,
		Password:  req.Password,
		Primary:   req.Primary,
	})
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, link)
}

func (s *Server) handleUnlinkSiteDatabase(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	if err := s.databases.UnlinkDatabase(r.Context(), r.PathValue("ref"), r.PathValue("linkID")); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSiteDatabaseEnv(w http.ResponseWriter, r *http.Request) {
	if s.databaseUnavailable(w) {
		return
	}
	env, err := s.databases.SiteDatabaseEnv(r.Context(), r.PathValue("ref"))
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	if env == nil {
		env = map[string]string{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"env": env})
}

// streamDownload writes a database export or backup with attachment headers.
func streamDownload(w http.ResponseWriter, reader io.ReadCloser, filename, contentType string) {
	if reader == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "download stream is unavailable", nil)
		return
	}
	defer func() { _ = reader.Close() }()
	if strings.TrimSpace(filename) == "" {
		filename = "database.sql.gz"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(filename)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}
