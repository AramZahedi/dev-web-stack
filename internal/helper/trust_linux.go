package helper

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	anchorFileName   = "devstack-root.crt"
	nssNickname      = "devstack local CA"
	fedoraAnchorsDir = "/etc/pki/ca-trust/source/anchors"
	debianAnchorsDir = "/usr/local/share/ca-certificates"
)

// detectAnchorDir returns the system CA anchor directory and the update
// command flavor to use. A non-empty override wins over distribution
// detection; paths under /etc/pki/ca-trust select the Fedora command.
func detectAnchorDir(override string) (dir string, fedora bool, ok bool) {
	if override != "" {
		clean := filepath.Clean(override)
		fedora := clean == fedoraAnchorsDir || strings.HasPrefix(clean, "/etc/pki/ca-trust/")
		return clean, fedora, true
	}
	if isDir(fedoraAnchorsDir) {
		return fedoraAnchorsDir, true, true
	}
	if isDir(debianAnchorsDir) {
		return debianAnchorsDir, false, true
	}
	if fileExists("/etc/debian_version") {
		return debianAnchorsDir, false, true
	}
	if fileExists("/etc/fedora-release") || fileExists("/etc/redhat-release") {
		return fedoraAnchorsDir, true, true
	}
	return "", false, false
}

func anchorUpdateCommand(fedora bool) (string, []string) {
	if fedora {
		return "update-ca-trust", []string{"extract"}
	}
	return "update-ca-certificates", nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (m *TrustManager) certutilPath() (string, bool) {
	path, err := exec.LookPath("certutil")
	if err != nil {
		return "", false
	}
	return path, true
}

func (m *TrustManager) installNSS(ctx context.Context) []Target {
	if _, ok := m.certutilPath(); !ok {
		return []Target{
			{Name: "Chrome", OK: true, Detail: "not installed"},
			{Name: "Firefox", OK: true, Detail: "not installed"},
		}
	}

	targets := []Target{
		m.installNSSDB(ctx, "Chrome", filepath.Join(m.userHome, ".pki", "nssdb")),
	}
	profiles, present, err := firefoxProfiles(m.userHome)
	if err != nil {
		return append(targets, Target{Name: "Firefox", OK: false, Detail: "read Firefox profiles: " + detailError(err)})
	}
	if !present || len(profiles) == 0 {
		return append(targets, Target{Name: "Firefox", OK: true, Detail: "not installed"})
	}
	for _, profile := range profiles {
		name := "Firefox (" + profile.Name + ")"
		if !pathWithinHome(m.userHome, profile.Dir) {
			targets = append(targets, Target{Name: name, OK: false, Detail: "profile path is outside the user's home"})
			continue
		}
		if !isDir(profile.Dir) {
			targets = append(targets, Target{Name: name, OK: true, Detail: "not installed"})
			continue
		}
		targets = append(targets, m.installNSSDB(ctx, name, profile.Dir))
	}
	return targets
}

func (m *TrustManager) removeNSS(ctx context.Context) []Target {
	if _, ok := m.certutilPath(); !ok {
		return []Target{
			{Name: "Chrome", OK: true, Detail: "not installed"},
			{Name: "Firefox", OK: true, Detail: "not installed"},
		}
	}

	targets := []Target{
		m.removeNSSNickname(ctx, "Chrome", filepath.Join(m.userHome, ".pki", "nssdb")),
	}
	profiles, present, err := firefoxProfiles(m.userHome)
	if err != nil {
		return append(targets, Target{Name: "Firefox", OK: false, Detail: "read Firefox profiles: " + detailError(err)})
	}
	if !present || len(profiles) == 0 {
		return append(targets, Target{Name: "Firefox", OK: true, Detail: "not installed"})
	}
	for _, profile := range profiles {
		name := "Firefox (" + profile.Name + ")"
		if !pathWithinHome(m.userHome, profile.Dir) {
			targets = append(targets, Target{Name: name, OK: false, Detail: "profile path is outside the user's home"})
			continue
		}
		if !isDir(profile.Dir) {
			targets = append(targets, Target{Name: name, OK: true, Detail: "not installed"})
			continue
		}
		targets = append(targets, m.removeNSSNickname(ctx, name, profile.Dir))
	}
	return targets
}

func (m *TrustManager) statusNSS(ctx context.Context) []Target {
	if _, ok := m.certutilPath(); !ok {
		return []Target{
			{Name: "Chrome", OK: true, Detail: "not installed"},
			{Name: "Firefox", OK: true, Detail: "not installed"},
		}
	}

	targets := []Target{
		m.statusNSSDB(ctx, "Chrome", filepath.Join(m.userHome, ".pki", "nssdb")),
	}
	profiles, present, err := firefoxProfiles(m.userHome)
	if err != nil {
		return append(targets, Target{Name: "Firefox", OK: false, Detail: "read Firefox profiles: " + detailError(err)})
	}
	if !present || len(profiles) == 0 {
		return append(targets, Target{Name: "Firefox", OK: true, Detail: "not installed"})
	}
	for _, profile := range profiles {
		name := "Firefox (" + profile.Name + ")"
		if !pathWithinHome(m.userHome, profile.Dir) {
			targets = append(targets, Target{Name: name, OK: false, Detail: "profile path is outside the user's home"})
			continue
		}
		if !isDir(profile.Dir) {
			targets = append(targets, Target{Name: name, OK: true, Detail: "not installed"})
			continue
		}
		targets = append(targets, m.statusNSSDB(ctx, name, profile.Dir))
	}
	return targets
}

func (m *TrustManager) installNSSDB(ctx context.Context, name, db string) Target {
	if !pathWithinHome(m.userHome, db) {
		return Target{Name: name, OK: false, Detail: "NSS database path is outside the user's home"}
	}
	if err := os.MkdirAll(db, 0o700); err != nil {
		return Target{Name: name, OK: false, Detail: "create NSS database directory: " + detailError(err)}
	}
	if _, err := os.Stat(filepath.Join(db, "cert9.db")); errors.Is(err, fs.ErrNotExist) {
		if _, err := m.runner.Run(ctx, "certutil", "-N", "--empty-password", "-d", "sql:"+db); err != nil {
			return Target{Name: name, OK: false, Detail: "create NSS database: " + detailError(err)}
		}
	}
	caFile, err := writeTempCertificate(m.rootPEM)
	if err != nil {
		return Target{Name: name, OK: false, Detail: "stage CA certificate: " + detailError(err)}
	}
	defer func() { _ = os.Remove(caFile) }()
	if _, err := m.runner.Run(ctx, "certutil", "-A", "-n", nssNickname, "-t", "C,,", "-i", caFile, "-d", "sql:"+db); err != nil {
		return Target{Name: name, OK: false, Detail: "install CA in NSS database: " + detailError(err)}
	}
	return Target{Name: name, OK: true, Detail: "installed"}
}

func (m *TrustManager) removeNSSNickname(ctx context.Context, name, db string) Target {
	if !pathWithinHome(m.userHome, db) {
		return Target{Name: name, OK: false, Detail: "NSS database path is outside the user's home"}
	}
	if _, err := os.Stat(db); errors.Is(err, fs.ErrNotExist) {
		return Target{Name: name, OK: true, Detail: "not installed"}
	}
	if !m.nssNicknamePresent(ctx, db) {
		return Target{Name: name, OK: true, Detail: "not installed"}
	}
	if _, err := m.runner.Run(ctx, "certutil", "-D", "-n", nssNickname, "-d", "sql:"+db); err != nil {
		return Target{Name: name, OK: false, Detail: "remove CA from NSS database: " + detailError(err)}
	}
	return Target{Name: name, OK: true, Detail: "removed"}
}

func (m *TrustManager) statusNSSDB(ctx context.Context, name, db string) Target {
	if !pathWithinHome(m.userHome, db) {
		return Target{Name: name, OK: false, Detail: "NSS database path is outside the user's home"}
	}
	if _, err := os.Stat(db); errors.Is(err, fs.ErrNotExist) {
		return Target{Name: name, OK: true, Detail: "not installed"}
	}
	if m.nssNicknamePresent(ctx, db) {
		return Target{Name: name, OK: true, Detail: "installed"}
	}
	return Target{Name: name, OK: false, Detail: "not installed"}
}

func (m *TrustManager) nssNicknamePresent(ctx context.Context, db string) bool {
	_, err := m.runner.Run(ctx, "certutil", "-L", "-n", nssNickname, "-d", "sql:"+db)
	return err == nil
}

func writeTempCertificate(certPEM []byte) (string, error) {
	f, err := os.CreateTemp("", "devstack-root-*.crt")
	if err != nil {
		return "", err
	}
	path := f.Name()
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(path)
	}
	if err := f.Chmod(0o600); err != nil {
		cleanup()
		return "", err
	}
	if _, err := f.Write(certPEM); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

type firefoxProfile struct {
	Name string
	Dir  string
}

// firefoxProfiles parses ~/.mozilla/firefox/profiles.ini. The boolean reports
// whether the profile file exists. Profiles outside the user's home are
// returned so callers can report them as rejected rather than silently
// ignoring attacker-controlled paths.
func firefoxProfiles(home string) ([]firefoxProfile, bool, error) {
	path := filepath.Join(home, ".mozilla", "firefox", "profiles.ini")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	base := filepath.Dir(path)

	var (
		profiles []firefoxProfile
		section  string
		current  map[string]string
	)
	flush := func() {
		if current == nil {
			return
		}
		if !strings.HasPrefix(strings.ToLower(section), "profile") {
			current = nil
			return
		}
		rawPath := current["path"]
		if rawPath == "" {
			current = nil
			return
		}
		isRelative := current["isrelative"]
		if isRelative == "" {
			isRelative = "1"
		}
		dir := rawPath
		if isRelative != "0" {
			dir = filepath.Join(base, rawPath)
		}
		name := current["name"]
		if name == "" {
			name = filepath.Base(filepath.Clean(rawPath))
		}
		profiles = append(profiles, firefoxProfile{Name: name, Dir: filepath.Clean(dir)})
		current = nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			section = strings.TrimSpace(line[1 : len(line)-1])
			current = make(map[string]string)
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		current[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, true, err
	}
	flush()
	return profiles, true, nil
}

// pathWithinHome reports whether path is a strict descendant of home after
// resolving symlinks in every existing ancestor. A symlinked .pki directory
// must not let the helper write outside the allowed home.
func pathWithinHome(home, path string) bool {
	if home == "" {
		return false
	}
	home = resolvePath(home)
	path = resolvePath(path)
	rel, err := filepath.Rel(home, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func resolvePath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(resolvePath(parent), filepath.Base(path))
}
