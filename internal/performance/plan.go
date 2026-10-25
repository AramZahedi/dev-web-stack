package performance

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Host target paths. This package never writes them; ApplyPlan returns their
// content and target paths so only the privileged helper applies host tuning.
const (
	SysctlTarget = "/etc/sysctl.d/99-devstack-performance.conf"
	LimitsTarget = "/etc/security/limits.d/99-devstack-performance.conf"
)

// ManagedFile is generated content under <root>/services.
type ManagedFile struct {
	Path    string      `json:"path"`
	Content []byte      `json:"content"`
	Mode    fs.FileMode `json:"mode"`
}

// HostFile is generated content for a fixed host path. The helper owns the
// write; the daemon only transports content and the fixed target.
type HostFile struct {
	Path    string      `json:"path"`
	Content []byte      `json:"content"`
	Mode    fs.FileMode `json:"mode"`
}

// Backup is prior host content saved under
// <root>/backups/performance/<timestamp>/. Only files that existed produce a
// backup entry; absent files are removed on reset.
type Backup struct {
	Source  string      `json:"source"`
	Path    string      `json:"path"`
	Content []byte      `json:"content"`
	Mode    fs.FileMode `json:"mode"`
}

// PriorFile is the current content of one host tuning file.
type PriorFile struct {
	Existed bool
	Content []byte
	Mode    fs.FileMode
}

// PriorHost is the current host tuning state captured before apply.
type PriorHost struct {
	Sysctl PriorFile
	Limits PriorFile
}

// ApplyPlan describes every managed file, host target, and backup needed for
// one apply. Paths are absolute and deterministic for the supplied root and
// timestamp.
type ApplyPlan struct {
	Profile   Profile       `json:"profile"`
	Values    Values        `json:"values"`
	Files     []ManagedFile `json:"files"`
	HostFiles []HostFile    `json:"host_files"`
	Backups   []Backup      `json:"backups"`
	BackupDir string        `json:"backup_dir"`
	// Stale lists existing PHP fragments that are no longer active. The
	// caller removes them only after the new files and host tuning succeed.
	Stale []string `json:"stale"`
}

// ResetPlan restores host tuning and replaces managed includes with safe empty
// snippets. Empty snippets keep mandatory base includes parseable while
// removing every tuning directive.
type ResetPlan struct {
	Files       []ManagedFile `json:"files"`
	RemoveHost  []string      `json:"remove_host"`
	RestoreHost []HostFile    `json:"restore_host"`
	BackupDir   string        `json:"backup_dir"`
	// Stale lists existing PHP fragments that are no longer active. Reset
	// callers remove them after writing the reset fragments.
	Stale []string `json:"stale"`
}

// BuildApplyPlan builds only the apply plan.
func BuildApplyPlan(root string, v Values, at time.Time, prior PriorHost, existingPHPFragments ...string) (ApplyPlan, error) {
	apply, _, err := BuildPlans(root, v, at, prior, existingPHPFragments...)
	return apply, err
}

// BuildResetPlan builds only the reset plan.
func BuildResetPlan(root string, v Values, at time.Time, prior PriorHost, existingPHPFragments ...string) (ResetPlan, error) {
	_, reset, err := BuildPlans(root, v, at, prior, existingPHPFragments...)
	return reset, err
}

// BuildPlans renders the complete apply/reset plan without touching the
// filesystem. The caller supplies the existing host file state and a fixed
// timestamp so tests and retries produce identical paths. Optional
// existingPHPFragments are absolute paths under
// <root>/services/php/performance; files not active in v are returned in
// ApplyPlan.Stale/ResetPlan.Stale for removal after a successful write.
func BuildPlans(root string, v Values, at time.Time, prior PriorHost, existingPHPFragments ...string) (ApplyPlan, ResetPlan, error) {
	if err := validateRoot(root); err != nil {
		return ApplyPlan{}, ResetPlan{}, err
	}
	if at.IsZero() {
		return ApplyPlan{}, ResetPlan{}, invalid("timestamp", "is required for the backup directory")
	}
	if err := validatePrior("prior sysctl", prior.Sysctl); err != nil {
		return ApplyPlan{}, ResetPlan{}, err
	}
	if err := validatePrior("prior limits", prior.Limits); err != nil {
		return ApplyPlan{}, ResetPlan{}, err
	}
	if err := v.Validate(); err != nil {
		return ApplyPlan{}, ResetPlan{}, err
	}

	backupDir := filepath.Join(root, "backups", "performance", at.UTC().Format("20060102T150405.000000000Z"))
	applyFiles, resetFiles, err := buildManagedFiles(root, v)
	if err != nil {
		return ApplyPlan{}, ResetPlan{}, err
	}
	stale, err := stalePHPFragments(root, v, existingPHPFragments)
	if err != nil {
		return ApplyPlan{}, ResetPlan{}, err
	}

	sysctl, err := RenderSysctl(v)
	if err != nil {
		return ApplyPlan{}, ResetPlan{}, err
	}
	limits, err := RenderLimits(v)
	if err != nil {
		return ApplyPlan{}, ResetPlan{}, err
	}
	hostFiles := []HostFile{
		{Path: SysctlTarget, Content: sysctl, Mode: 0o644},
		{Path: LimitsTarget, Content: limits, Mode: 0o644},
	}
	backups := make([]Backup, 0, 2)
	if prior.Sysctl.Existed {
		backups = append(backups, newBackup(
			SysctlTarget,
			filepath.Join(backupDir, "etc", "sysctl.d", filepath.Base(SysctlTarget)),
			prior.Sysctl,
		))
	}
	if prior.Limits.Existed {
		backups = append(backups, newBackup(
			LimitsTarget,
			filepath.Join(backupDir, "etc", "security", "limits.d", filepath.Base(LimitsTarget)),
			prior.Limits,
		))
	}

	reset := ResetPlan{
		Files:     resetFiles,
		BackupDir: backupDir,
		Stale:     stale,
	}
	if prior.Sysctl.Existed {
		reset.RestoreHost = append(reset.RestoreHost, restoreFile(SysctlTarget, prior.Sysctl))
	} else {
		reset.RemoveHost = append(reset.RemoveHost, SysctlTarget)
	}
	if prior.Limits.Existed {
		reset.RestoreHost = append(reset.RestoreHost, restoreFile(LimitsTarget, prior.Limits))
	} else {
		reset.RemoveHost = append(reset.RemoveHost, LimitsTarget)
	}

	return ApplyPlan{
		Profile:   v.Profile,
		Values:    v,
		Files:     applyFiles,
		HostFiles: hostFiles,
		Backups:   backups,
		BackupDir: backupDir,
		Stale:     stale,
	}, reset, nil
}

func stalePHPFragments(root string, v Values, existing []string) ([]string, error) {
	dir := filepath.Join(root, "services", "php", "performance")
	desired := make(map[string]bool, len(v.PHP.Sites))
	for _, site := range v.PHP.Sites {
		desired[filepath.Join(dir, site.SiteID+".conf")] = true
	}
	seen := make(map[string]bool, len(existing))
	stale := make([]string, 0)
	for _, path := range existing {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, invalid("existing php fragment", "%q must be an absolute clean path", path)
		}
		if filepath.Dir(path) != dir || !strings.HasSuffix(filepath.Base(path), ".conf") {
			return nil, invalid("existing php fragment", "%q is outside %s", path, dir)
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		if !desired[path] {
			stale = append(stale, path)
		}
	}
	sort.Strings(stale)
	return stale, nil
}

func buildManagedFiles(root string, v Values) ([]ManagedFile, []ManagedFile, error) {
	nginxDir := filepath.Join(root, "services", "nginx", "config", "conf.d", "performance")
	nginx, err := RenderNginx(v)
	if err != nil {
		return nil, nil, err
	}
	apache, err := RenderApache(v)
	if err != nil {
		return nil, nil, err
	}

	applyFiles := []ManagedFile{
		{Path: filepath.Join(nginxDir, "main.conf"), Content: nginx.Main, Mode: 0o644},
		{Path: filepath.Join(nginxDir, "events.conf"), Content: nginx.Events, Mode: 0o644},
		{Path: filepath.Join(nginxDir, "http.conf"), Content: nginx.HTTP, Mode: 0o644},
		{Path: filepath.Join(root, "services", "apache", "config", "conf.d", "performance.conf"), Content: apache, Mode: 0o644},
	}
	resetFiles := []ManagedFile{
		{Path: filepath.Join(nginxDir, "main.conf"), Content: []byte("# Code generated by devstack performance reset. No tuning applied.\n"), Mode: 0o644},
		{Path: filepath.Join(nginxDir, "events.conf"), Content: []byte("# Code generated by devstack performance reset. No tuning applied.\n"), Mode: 0o644},
		{Path: filepath.Join(nginxDir, "http.conf"), Content: []byte("# Code generated by devstack performance reset. No tuning applied.\n"), Mode: 0o644},
		{Path: filepath.Join(root, "services", "apache", "config", "conf.d", "performance.conf"), Content: []byte("# Code generated by devstack performance reset. No tuning applied.\n"), Mode: 0o644},
	}

	ids := make([]string, 0, len(v.PHP.Sites))
	for _, site := range v.PHP.Sites {
		ids = append(ids, site.SiteID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		pool, err := RenderPHPPool(v, id)
		if err != nil {
			return nil, nil, err
		}
		path := filepath.Join(root, "services", "php", "performance", id+".conf")
		applyFiles = append(applyFiles, ManagedFile{Path: path, Content: pool, Mode: 0o644})
		resetFiles = append(resetFiles, ManagedFile{
			Path:    path,
			Content: []byte("; Code generated by devstack performance reset. No overrides applied.\n"),
			Mode:    0o644,
		})
	}

	sort.Slice(applyFiles, func(i, j int) bool { return applyFiles[i].Path < applyFiles[j].Path })
	sort.Slice(resetFiles, func(i, j int) bool { return resetFiles[i].Path < resetFiles[j].Path })
	return applyFiles, resetFiles, nil
}

func validateRoot(root string) error {
	if root == "" {
		return invalid("root", "is required")
	}
	if strings.ContainsAny(root, "\x00\r\n") {
		return invalid("root", "contains a control character")
	}
	if !filepath.IsAbs(root) {
		return invalid("root", "%q is not an absolute path", root)
	}
	if filepath.Clean(root) != root {
		return invalid("root", "%q is not a clean path", root)
	}
	if root == string(filepath.Separator) {
		return invalid("root", "must not be the filesystem root")
	}
	return nil
}

func validatePrior(field string, p PriorFile) error {
	if !p.Existed && len(p.Content) > 0 {
		return invalid(field, "content was supplied for a file marked absent")
	}
	if p.Mode != 0 && p.Mode&fs.ModeType != 0 {
		return invalid(field, "mode %v is not a regular file mode", p.Mode)
	}
	return nil
}

func newBackup(source, path string, prior PriorFile) Backup {
	return Backup{
		Source:  source,
		Path:    path,
		Content: cloneBytes(prior.Content),
		Mode:    priorMode(prior.Mode),
	}
}

func restoreFile(path string, prior PriorFile) HostFile {
	return HostFile{
		Path:    path,
		Content: cloneBytes(prior.Content),
		Mode:    priorMode(prior.Mode),
	}
}

func priorMode(mode fs.FileMode) fs.FileMode {
	if mode.Perm() == 0 {
		return 0o644
	}
	return mode.Perm()
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
