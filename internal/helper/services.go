//go:build !windows

package helper

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

var serviceIDRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func validateServiceID(id string) error {
	if !serviceIDRegex.MatchString(id) {
		return fmt.Errorf("%w: invalid service ID %q", helperproto.ErrInvalidRequest, id)
	}
	return nil
}

func (s *Server) handleServicesStatus(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceStatus == nil {
		s.notImplemented(w, "ServiceStatus")
		return
	}
	resp, err := s.cfg.Service.ServiceStatus(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleServicesInstall(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceInstall == nil {
		s.notImplemented(w, "ServiceInstall")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceInstall(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesActivate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceActivate == nil {
		s.notImplemented(w, "ServiceActivate")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceActivate(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesDeactivate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceDeactivate == nil {
		s.notImplemented(w, "ServiceDeactivate")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceDeactivate(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesUninstall(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceUninstall == nil {
		s.notImplemented(w, "ServiceUninstall")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceUninstall(r.Context(), req.ID, req.DeleteData); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesRestart(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceRestart == nil {
		s.notImplemented(w, "ServiceRestart")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceRestart(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesRotate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceRotateCredentials == nil {
		s.notImplemented(w, "ServiceRotateCredentials")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceRotateCredentials(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesValidate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceValidate == nil {
		s.notImplemented(w, "ServiceValidate")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceValidate(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleServicesReload(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceReload == nil {
		s.notImplemented(w, "ServiceReload")
		return
	}
	var req helperproto.ServiceActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateServiceID(req.ID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.ServiceReload(r.Context(), req.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) databasePlatform(w http.ResponseWriter) (databases.Platform, bool) {
	if s.cfg.Service.DatabasePlatform == nil {
		s.notImplemented(w, "DatabasePlatform")
		return nil, false
	}
	return s.cfg.Service.DatabasePlatform, true
}

func (s *Server) handleDatabaseList(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	list, err := platform.ListDatabases(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.DatabaseListResponse{Databases: list})
}

func (s *Server) handleDatabaseCreate(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var req dbproto.DatabaseCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if req.ID != "" && req.ID != id {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "service ID does not match the request path")
		return
	}
	created, err := platform.CreateDatabase(r.Context(), id, databases.CreateDatabaseRequest{
		Name:      req.Name,
		Charset:   req.Charset,
		Collation: req.Collation,
	})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.DatabaseResponse{Database: created})
}

func (s *Server) handleDatabaseGet(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	name := r.PathValue("name")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateDatabasePathValue(name); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	list, err := platform.ListDatabases(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	for _, database := range list {
		if database.Name == name {
			s.writeJSON(w, http.StatusOK, dbproto.DatabaseResponse{Database: database})
			return
		}
	}
	s.writeServiceError(w, fmt.Errorf("%w: database %q", databases.ErrNotFound, name))
}

func (s *Server) handleDatabaseRename(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	name := r.PathValue("name")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateDatabasePathValue(name); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var req dbproto.DatabaseRenameRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if req.ID != "" && req.ID != id {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "service ID does not match the request path")
		return
	}
	if req.Name != "" && req.Name != name {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "database name does not match the request path")
		return
	}
	renamed, err := platform.RenameDatabase(r.Context(), id, name, databases.RenameDatabaseRequest{Name: req.NewName})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.DatabaseResponse{Database: renamed})
}

func (s *Server) handleDatabaseDrop(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	name := r.PathValue("name")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateDatabasePathValue(name); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var req dbproto.DatabaseDropRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if req.ID != "" && req.ID != id {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "service ID does not match the request path")
		return
	}
	if req.Name != "" && req.Name != name {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "database name does not match the request path")
		return
	}
	if err := platform.DropDatabase(r.Context(), id, name, databases.DropDatabaseRequest{Confirm: req.Confirm}); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.DeleteResponse{Deleted: true})
}

func (s *Server) handleUserList(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	users, err := platform.ListUsers(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.UserListResponse{Users: users})
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var req dbproto.UserCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if req.ID != "" && req.ID != id {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "service ID does not match the request path")
		return
	}
	created, err := platform.CreateUser(r.Context(), id, databases.CreateUserRequest{
		Username:    req.Username,
		Host:        req.Host,
		Grants:      req.Grants,
		AccessLevel: req.AccessLevel,
	})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.UserResponse{User: created})
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	userID := r.PathValue("userID")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateUserPathValue(userID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var req dbproto.UserUpdateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if req.ID != "" && req.ID != id {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "service ID does not match the request path")
		return
	}
	if req.UserID != "" && req.UserID != userID {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "user ID does not match the request path")
		return
	}
	updated, err := platform.UpdateUser(r.Context(), id, userID, databases.UpdateUserRequest{
		Enabled:     req.Enabled,
		Grants:      req.Grants,
		AccessLevel: req.AccessLevel,
	})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.UserResponse{User: updated})
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	userID := r.PathValue("userID")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateUserPathValue(userID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var req dbproto.UserDeleteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if req.ID != "" && req.ID != id {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "service ID does not match the request path")
		return
	}
	if req.UserID != "" && req.UserID != userID {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "user ID does not match the request path")
		return
	}
	if err := platform.DeleteUser(r.Context(), id, userID, databases.DeleteUserRequest{
		Confirm:       req.Confirm,
		ReassignOwned: req.ReassignOwned,
	}); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.DeleteResponse{Deleted: true})
}

func (s *Server) handleUserPassword(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	userID := r.PathValue("userID")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateUserPathValue(userID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var req dbproto.UserPasswordRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
			return
		}
	}
	if req.ID != "" && req.ID != id {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "service ID does not match the request path")
		return
	}
	if req.UserID != "" && req.UserID != userID {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "user ID does not match the request path")
		return
	}
	secret, err := platform.SetUserPassword(r.Context(), id, userID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.SecretResponse{Value: secret.Value})
}

func (s *Server) handleUserReveal(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	userID := r.PathValue("userID")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateUserPathValue(userID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var req dbproto.UserRevealRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
			return
		}
	}
	if req.ID != "" && req.ID != id {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "service ID does not match the request path")
		return
	}
	if req.UserID != "" && req.UserID != userID {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "user ID does not match the request path")
		return
	}
	secret, err := platform.RevealUserPassword(r.Context(), id, userID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.SecretResponse{Value: secret.Value})
}

func (s *Server) handleBackupList(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	list, err := platform.ListBackups(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.BackupListResponse{Backups: list})
}

func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	name := r.PathValue("name")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateDatabasePathValue(name); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var req dbproto.BackupCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if req.ID != "" && req.ID != id {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "service ID does not match the request path")
		return
	}
	if req.Database != "" && req.Database != name {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "database name does not match the request path")
		return
	}
	kind := model.DatabaseBackupKind(req.Kind)
	if kind == "" {
		kind = model.BackupManual
	}
	job, err := platform.CreateBackup(r.Context(), id, name, databases.CreateBackupRequest{Kind: kind})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.JobResponse{Job: job})
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	backupID := r.PathValue("backupID")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateBackupPathValue(backupID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	body, err := platform.DownloadBackup(r.Context(), id, backupID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	defer func() { _ = body.Close() }()
	s.writeStream(w, "application/gzip", fmt.Sprintf("attachment; filename=%q", path.Base(backupID)), body)
}

func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	backupID := r.PathValue("backupID")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateBackupPathValue(backupID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := platform.DeleteBackup(r.Context(), id, backupID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.DeleteResponse{Deleted: true})
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	backupID := r.PathValue("backupID")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateBackupPathValue(backupID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var req dbproto.BackupRestoreRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if req.ID != "" && req.ID != id {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "service ID does not match the request path")
		return
	}
	if req.BackupID != "" && req.BackupID != backupID {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "backup ID does not match the request path")
		return
	}
	job, err := platform.RestoreBackup(r.Context(), id, backupID, databases.RestoreBackupRequest{
		Target:           req.Target,
		Overwrite:        req.Overwrite,
		Confirm:          req.Confirm,
		PreRestoreBackup: req.PreRestoreBackup,
	})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.JobResponse{Job: job})
}

func (s *Server) handleDatabaseImport(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	name := r.PathValue("name")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateDatabasePathValue(name); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	query := r.URL.Query()
	mode := databases.ImportMode(query.Get(dbproto.QueryMode))
	if mode == "" {
		mode = databases.ImportMerge
	}
	req := databases.ImportRequest{
		Mode:            mode,
		Confirm:         query.Get(dbproto.QueryConfirm),
		PreImportBackup: parseBool(query.Get(dbproto.QueryPreImportBackup)),
	}
	defer func() { _ = r.Body.Close() }()
	body := http.MaxBytesReader(w, r.Body, maxDatabaseImportBytes+1)
	// The helper derives the format from the staged bytes; the caller-supplied
	// filename is not used for paths or format detection.
	job, err := platform.ImportDatabase(r.Context(), id, name, body, "", req)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.JobResponse{Job: job})
}

func (s *Server) handleDatabaseExport(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	name := r.PathValue("name")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := validateDatabasePathValue(name); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	body, err := platform.ExportDatabase(r.Context(), id, name)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	defer func() { _ = body.Close() }()
	s.writeStream(w, "application/sql", fmt.Sprintf("attachment; filename=%q", name+".sql"), body)
}

// writeStream validates the first chunk before committing a 200 response. A
// stream that fails immediately becomes a normal error response; a late
// failure can only be logged because headers are already sent.
func (s *Server) writeStream(w http.ResponseWriter, contentType, disposition string, body io.ReadCloser) {
	prefix := make([]byte, 32<<10)
	n, err := body.Read(prefix)
	if n == 0 && err != nil && !errors.Is(err, io.EOF) {
		s.writeServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	if disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	w.WriteHeader(http.StatusOK)
	if n > 0 {
		if _, writeErr := w.Write(prefix[:n]); writeErr != nil {
			s.logger.Debug("helper: write stream prefix", "error", writeErr)
			return
		}
	}
	if err == nil {
		if _, copyErr := io.Copy(w, body); copyErr != nil {
			s.logger.Debug("helper: stream response", "error", copyErr)
		}
		return
	}
	if !errors.Is(err, io.EOF) {
		s.logger.Debug("helper: stream response", "error", err)
	}
}

func (s *Server) handleJobGet(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	jobID := r.PathValue("jobID")
	if err := validateUserPathValue(jobID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	job, err := platform.GetJob(r.Context(), jobID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.JobResponse{Job: job})
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	jobID := r.PathValue("jobID")
	if err := validateUserPathValue(jobID); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := platform.CancelJob(r.Context(), jobID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.DeleteResponse{Deleted: true})
}

func (s *Server) handleDatabaseEnv(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var req dbproto.SiteEnvRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if req.ServiceID != "" && req.ServiceID != id {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "service ID does not match the request path")
		return
	}
	env, err := platform.ResolveSiteEnv(r.Context(), id, req.Links)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.SiteEnvResponse{Env: env})
}

func (s *Server) handleServicePassword(w http.ResponseWriter, r *http.Request) {
	platform, ok := s.databasePlatform(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if id != string(services.IDRedis) {
		s.writeServiceError(w, fmt.Errorf("%w: only Redis has a service-level password", databases.ErrUnsupported))
		return
	}
	secret, err := platform.SetUserPassword(r.Context(), id, redisUserID("default"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, dbproto.SecretResponse{Value: secret.Value})
}

func parseBool(value string) bool {
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func validateDatabasePathValue(value string) error {
	if value == "" || len(value) > 63 {
		return fmt.Errorf("%w: invalid database name", helperproto.ErrInvalidRequest)
	}
	return nil
}

func validateUserPathValue(value string) error {
	if value == "" || len(value) > 128 {
		return fmt.Errorf("%w: invalid user ID", helperproto.ErrInvalidRequest)
	}
	return nil
}

func validateBackupPathValue(value string) error {
	if value == "" || len(value) > 300 {
		return fmt.Errorf("%w: invalid backup ID", helperproto.ErrInvalidRequest)
	}
	return nil
}

func (s *Server) handleServicesLogs(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.ServiceLogs == nil {
		s.notImplemented(w, "ServiceLogs")
		return
	}
	id := r.URL.Query().Get("id")
	var tail int
	if t := r.URL.Query().Get("tail"); t != "" {
		tail, _ = strconv.Atoi(t)
	}
	if id == "" && r.Body != nil && r.ContentLength != 0 {
		var req helperproto.ServiceLogsRequest
		if err := decodeJSON(w, r, &req); err == nil {
			id = req.ID
			tail = req.Tail
		}
	}
	if err := validateServiceID(id); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	logs, err := s.cfg.Service.ServiceLogs(r.Context(), id, tail)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, helperproto.ServiceLogsResponse{Logs: logs})
}
