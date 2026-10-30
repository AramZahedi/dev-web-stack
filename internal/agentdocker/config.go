package agentdocker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

const maxConfigBytes = 1 << 20

// ServiceConfig lists only regular, non-sensitive files below the service's
// WSL config directory.
func (d *Docker) ServiceConfig(ctx context.Context, id string) (agentproto.ServiceConfigResponse, error) {
	service, err := d.service(id)
	if err != nil {
		return agentproto.ServiceConfigResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return agentproto.ServiceConfigResponse{}, err
	}
	root, err := d.safeServiceConfigRoot(service.ID)
	if err != nil {
		return agentproto.ServiceConfigResponse{}, err
	}
	var files []agentproto.ServiceConfigFile
	err = filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if _, err := cleanConfigRel(rel); err != nil {
			return nil
		}
		content, err := readConfig(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, errUnsafeConfig) {
				return nil
			}
			return err
		}
		files = append(files, agentproto.ServiceConfigFile{Path: rel, Class: "custom", Hash: hashBytes(content)})
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return agentproto.ServiceConfigResponse{}, fmt.Errorf("agentdocker: list %s config: %w", id, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return agentproto.ServiceConfigResponse{Files: files}, nil
}

// ServiceContent reads one explicitly selected safe config file from WSL.
func (d *Docker) ServiceContent(ctx context.Context, request agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	service, err := d.service(request.ID)
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	rel, full, err := d.resolveConfig(service.ID, request.Rel, false)
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	content, err := readConfig(full)
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	return agentproto.ServiceContentResponse{Path: rel, Content: string(content), Hash: hashBytes(content)}, nil
}

// ServiceApply atomically writes one safe config file. Existing content is
// backed up under the WSL backups tree before the replacement is published.
// Active web-server configs are validated and reloaded; a failed validation or
// reload restores the previous file content.
func (d *Docker) ServiceApply(ctx context.Context, request agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	service, err := d.service(request.ID)
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	if err := validateConfigContent(request.Content); err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	rel, full, err := d.resolveConfig(service.ID, request.Rel, true)
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	var previous []byte
	existed := false
	if current, err := os.Lstat(full); err == nil {
		if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
			return agentproto.ServiceContentResponse{}, fmt.Errorf("agentdocker: config file %q is not a regular file", rel)
		}
		previous, err = readConfig(full)
		if err != nil {
			return agentproto.ServiceContentResponse{}, err
		}
		existed = true
		if _, err := d.backupConfig(service.ID, rel, previous, "config"); err != nil {
			return agentproto.ServiceContentResponse{}, fmt.Errorf("agentdocker: backup %s config %s: %w", service.ID, rel, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return agentproto.ServiceContentResponse{}, err
	}
	if err := writeAtomic(full, []byte(request.Content), 0o644); err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	stage, applyErr := d.applyLiveWebConfig(ctx, service)
	if applyErr != nil {
		rollbackErr := d.restoreServiceConfig(full, previous, existed)
		if stage == "reload" {
			// The reload may have failed partway; make a best effort to put the
			// previous config back in front of the running server.
			_ = d.reloadActiveWebConfig(ctx, service)
		}
		if rollbackErr != nil {
			return agentproto.ServiceContentResponse{}, fmt.Errorf("agentdocker: %s %s config %s: %w (rollback failed: %v)", stage, service.ID, rel, applyErr, rollbackErr)
		}
		return agentproto.ServiceContentResponse{}, fmt.Errorf("agentdocker: %s %s config %s: %w", stage, service.ID, rel, applyErr)
	}
	return agentproto.ServiceContentResponse{Path: rel, Content: request.Content, Hash: hashBytes([]byte(request.Content))}, nil
}

// applyLiveWebConfig validates and reloads an active web-server config. It
// returns the stage that failed so the caller can decide whether the previous
// config must be reloaded after a rollback. Non-web and inactive services are
// staged only; their change becomes live on the next activation.
func (d *Docker) applyLiveWebConfig(ctx context.Context, service services.Service) (string, error) {
	if service.Group != services.GroupWeb {
		return "", nil
	}
	d.opMu.Lock()
	defer d.opMu.Unlock()
	status, err := d.Status(ctx, string(service.ID))
	if err != nil {
		return "inspect", fmt.Errorf("read service state: %w", err)
	}
	if status.State != "active" {
		return "", nil
	}
	if err := d.ensureCompose(); err != nil {
		return "prepare", err
	}
	if len(service.ValidateCommand) == 0 {
		return "validate", errors.New("catalog has no validation command")
	}
	if _, err := d.compose(ctx, append([]string{"exec", "-T", string(service.ID)}, service.ValidateCommand...)...); err != nil {
		return "validate", err
	}
	if len(service.ReloadCommand) == 0 {
		return "reload", errors.New("catalog has no reload command")
	}
	if _, err := d.compose(ctx, append([]string{"exec", "-T", string(service.ID)}, service.ReloadCommand...)...); err != nil {
		return "reload", err
	}
	return "", nil
}

// reloadActiveWebConfig is the best-effort rollback companion for a failed
// reload. Errors are intentionally ignored by the caller.
func (d *Docker) reloadActiveWebConfig(ctx context.Context, service services.Service) error {
	if service.Group != services.GroupWeb || len(service.ReloadCommand) == 0 {
		return nil
	}
	d.opMu.Lock()
	defer d.opMu.Unlock()
	if err := d.ensureCompose(); err != nil {
		return err
	}
	_, err := d.compose(ctx, append([]string{"exec", "-T", string(service.ID)}, service.ReloadCommand...)...)
	return err
}

func (d *Docker) restoreServiceConfig(full string, previous []byte, existed bool) error {
	if !existed {
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeAtomic(full, previous, 0o644)
}

// ServiceReset restores the newest previous config backup. It first retains
// the current content in a separate reset-backup area to prevent toggle loops.
func (d *Docker) ServiceReset(ctx context.Context, request agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	service, err := d.service(request.ID)
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	rel, full, err := d.resolveConfig(service.ID, request.Rel, false)
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	backup, err := d.latestConfigBackup(service.ID, rel)
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	if backup == "" {
		return agentproto.ServiceContentResponse{}, fmt.Errorf("agentdocker: no previous config backup for %q", rel)
	}
	current, err := readConfig(full)
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	if _, err := d.backupConfig(service.ID, rel, current, "config-reset"); err != nil {
		return agentproto.ServiceContentResponse{}, fmt.Errorf("agentdocker: preserve current config before reset: %w", err)
	}
	content, err := readConfig(backup)
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	if err := writeAtomic(full, content, 0o644); err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	return agentproto.ServiceContentResponse{Path: rel, Content: string(content), Hash: hashBytes(content)}, nil
}

func (d *Docker) serviceConfigRoot(id services.ID) string {
	canonicalID := id
	if canonicalID == services.IDPostgreSQL {
		canonicalID = "postgres"
	}
	return filepath.Join(d.root, "services", string(canonicalID), "config")
}

func (d *Docker) safeServiceConfigRoot(id services.ID) (string, error) {
	root := d.serviceConfigRoot(id)
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("agentdocker: inspect service config root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("agentdocker: service config root is not a real directory")
	}
	return root, nil
}

func (d *Docker) resolveConfig(id services.ID, rel string, allowMissing bool) (string, string, error) {
	clean, err := cleanConfigRel(rel)
	if err != nil {
		return "", "", err
	}
	root, err := d.safeServiceConfigRoot(id)
	if err != nil {
		return "", "", err
	}
	full := filepath.Join(root, filepath.FromSlash(clean))
	current := root
	parts := strings.Split(clean, "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if allowMissing {
				return clean, full, nil
			}
			return "", "", fmt.Errorf("agentdocker: service config %q was not found", clean)
		}
		if err != nil {
			return "", "", fmt.Errorf("agentdocker: inspect config path %q: %w", clean, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("agentdocker: service config path %q contains a symlink", clean)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", "", fmt.Errorf("agentdocker: service config path %q has a non-directory parent", clean)
		}
		if i == len(parts)-1 && info.IsDir() {
			return "", "", fmt.Errorf("agentdocker: service config path %q is a directory", clean)
		}
	}
	return clean, full, nil
}

var errUnsafeConfig = errors.New("agentdocker: unsafe config file")

func cleanConfigRel(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" || strings.ContainsRune(rel, 0) || strings.Contains(rel, `\`) || path.IsAbs(rel) {
		return "", errUnsafeConfig
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != rel {
		return "", errUnsafeConfig
	}
	for _, part := range strings.Split(clean, "/") {
		lower := strings.ToLower(part)
		if part == "" || strings.HasPrefix(part, ".") || sensitiveConfigName(lower) {
			return "", errUnsafeConfig
		}
	}
	return clean, nil
}

func sensitiveConfigName(name string) bool {
	for _, marker := range []string{"secret", "password", "passwd", "credential", "token", "private", "apikey", "api_key"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".key", ".pem", ".p12", ".pfx", ".crt", ".cer":
		return true
	default:
		return false
	}
}

func readConfig(name string) ([]byte, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("agentdocker: read config: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("agentdocker: read config: %w", err)
	}
	if len(data) > maxConfigBytes {
		return nil, errors.New("agentdocker: service config exceeds 1 MiB")
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return nil, errors.New("agentdocker: service config must be UTF-8 text")
	}
	return data, nil
}

func validateConfigContent(content string) error {
	if len(content) > maxConfigBytes {
		return errors.New("agentdocker: service config exceeds 1 MiB")
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return errors.New("agentdocker: service config must be UTF-8 text")
	}
	return nil
}

func (d *Docker) backupConfig(id services.ID, rel string, content []byte, area string) (string, error) {
	base := filepath.Join(d.root, "backups", "services", string(id), area)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(base, 0o700)
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	stampDir := filepath.Join(base, stamp)
	if err := os.MkdirAll(stampDir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(stampDir, 0o700)
	dest := filepath.Join(stampDir, filepath.FromSlash(rel))
	destDir := filepath.Dir(dest)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", err
	}
	for p := destDir; strings.HasPrefix(p, stampDir); p = filepath.Dir(p) {
		_ = os.Chmod(p, 0o700)
		if p == stampDir {
			break
		}
	}
	if err := writeAtomic(dest, content, 0o600); err != nil {
		return "", err
	}
	return dest, nil
}

func (d *Docker) latestConfigBackup(id services.ID, rel string) (string, error) {
	base := filepath.Join(d.root, "backups", "services", string(id), "config")
	var matches []string
	err := filepath.WalkDir(base, func(name string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return fs.SkipAll
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		candidate, err := filepath.Rel(base, name)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(candidate), "/")
		if len(parts) > 1 && strings.Join(parts[1:], "/") == rel {
			matches = append(matches, name)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return "", nil
	}
	return matches[len(matches)-1], nil
}

func hashBytes(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

func writeAtomic(name string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return fmt.Errorf("agentdocker: create file directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), "agent-*.tmp")
	if err != nil {
		return fmt.Errorf("agentdocker: create temporary file: %w", err)
	}
	temp := tmp.Name()
	defer os.Remove(temp)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentdocker: write temporary file: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentdocker: set file permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentdocker: sync temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("agentdocker: close temporary file: %w", err)
	}
	if err := os.Rename(temp, name); err != nil {
		return fmt.Errorf("agentdocker: publish file: %w", err)
	}
	return nil
}

// ServiceCredential reveals only a known database secret through the explicit
// credential endpoint. No caller-supplied path is ever read.
func (d *Docker) ServiceCredential(ctx context.Context, request agentproto.ServiceCredentialRequest) (agentproto.ServiceCredentialResponse, error) {
	service, err := d.service(request.ID)
	if err != nil {
		return agentproto.ServiceCredentialResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return agentproto.ServiceCredentialResponse{}, err
	}
	field := strings.TrimSpace(request.Field)
	if field == "" {
		field = "devstack_password"
	}
	filename := ""
	username := "devstack"
	switch service.ID {
	case services.IDMySQL, services.IDMariaDB:
		switch field {
		case "root_password":
			filename, username = field, "root"
		case "devstack_password":
			filename = field
		default:
			return agentproto.ServiceCredentialResponse{}, errors.New("agentdocker: unsupported database credential field")
		}
	case "postgres":
		switch field {
		case "postgres_password":
			filename, username = field, "postgres"
		case "devstack_password":
			filename = field
		default:
			return agentproto.ServiceCredentialResponse{}, errors.New("agentdocker: unsupported database credential field")
		}
	default:
		return agentproto.ServiceCredentialResponse{}, fmt.Errorf("agentdocker: credentials are not supported for %s", service.ID)
	}
	name := filepath.Join(d.root, "services", string(service.ID), "secrets", filename)
	dirInfo, err := os.Lstat(filepath.Dir(name))
	if err != nil {
		return agentproto.ServiceCredentialResponse{}, fmt.Errorf("agentdocker: inspect %s secret directory: %w", service.ID, err)
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() || dirInfo.Mode().Perm()&0o077 != 0 {
		return agentproto.ServiceCredentialResponse{}, errors.New("agentdocker: credential directory is not private")
	}
	info, err := os.Lstat(name)
	if err != nil {
		return agentproto.ServiceCredentialResponse{}, fmt.Errorf("agentdocker: read %s credential: %w", service.ID, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return agentproto.ServiceCredentialResponse{}, errors.New("agentdocker: credential file is not a private regular file")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return agentproto.ServiceCredentialResponse{}, fmt.Errorf("agentdocker: read %s credential: %w", service.ID, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return agentproto.ServiceCredentialResponse{}, errors.New("agentdocker: credential is empty")
	}
	return agentproto.ServiceCredentialResponse{ID: request.ID, User: username, Value: value}, nil
}

func (d *Docker) handleServiceConfig(w http.ResponseWriter, r *http.Request) {
	response, err := d.ServiceConfig(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (d *Docker) handleServiceContent(w http.ResponseWriter, r *http.Request) {
	var request agentproto.ServiceConfigRequest
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.ID != r.PathValue("id") {
		writeError(w, http.StatusBadRequest, errors.New("service ID does not match URL"))
		return
	}
	response, err := d.ServiceContent(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (d *Docker) handleServiceApply(w http.ResponseWriter, r *http.Request) {
	var request agentproto.ServiceConfigRequest
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.ID != r.PathValue("id") {
		writeError(w, http.StatusBadRequest, errors.New("service ID does not match URL"))
		return
	}
	response, err := d.ServiceApply(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (d *Docker) handleServiceReset(w http.ResponseWriter, r *http.Request) {
	var request agentproto.ServiceConfigRequest
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.ID != r.PathValue("id") {
		writeError(w, http.StatusBadRequest, errors.New("service ID does not match URL"))
		return
	}
	response, err := d.ServiceReset(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (d *Docker) handleServiceCredential(w http.ResponseWriter, r *http.Request) {
	var request agentproto.ServiceCredentialRequest
	if err := decodeRequest(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.ID != r.PathValue("id") {
		writeError(w, http.StatusBadRequest, errors.New("service ID does not match URL"))
		return
	}
	response, err := d.ServiceCredential(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
