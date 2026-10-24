package helper

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	anchorFileName     = "devstack-root.crt"
	nssNickname        = "devstack local CA"
	fedoraAnchorsDir   = "/etc/pki/ca-trust/source/anchors"
	debianAnchorsDir   = "/usr/local/share/ca-certificates"
	maxProfilesIniSize = 1 << 20
)

var nssDBFileNames = []string{"cert9.db", "key4.db", "pkcs11.txt"}

// detectAnchorDir returns the system CA anchor directory and the update
// command flavor to use. A non-empty override wins over distribution
// detection; paths under /etc/pki/ca-trust select the Fedora command.
func detectAnchorDir(override string) (dir string, fedora bool, ok bool) {
	if override != "" {
		clean := filepath.Clean(override)
		fedora := clean == fedoraAnchorsDir || strings.HasPrefix(clean, "/etc/pki/ca-trust/")
		return clean, fedora, true
	}
	// Prefer the distribution's own identification over probing directories,
	// which can exist on a foreign family (for example a Fedora directory left
	// behind on a Debian system).
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		if familyFedora, known := osReleaseFamily(data); known {
			if familyFedora {
				return fedoraAnchorsDir, true, true
			}
			return debianAnchorsDir, false, true
		}
	}
	if fileExists("/etc/debian_version") {
		return debianAnchorsDir, false, true
	}
	if fileExists("/etc/fedora-release") || fileExists("/etc/redhat-release") {
		return fedoraAnchorsDir, true, true
	}
	// Directory fallback for minimal systems without os-release.
	if isDir(fedoraAnchorsDir) {
		return fedoraAnchorsDir, true, true
	}
	if isDir(debianAnchorsDir) {
		return debianAnchorsDir, false, true
	}
	return "", false, false
}

// osReleaseFamily maps /etc/os-release contents to the update command family.
func osReleaseFamily(data []byte) (fedora, known bool) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 4*1024), maxProfilesIniSize)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.ToLower(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	ids := []string{values["id"]}
	ids = append(ids, strings.Fields(values["id_like"])...)
	for _, id := range ids {
		switch strings.ToLower(id) {
		case "fedora", "rhel", "redhat", "centos", "rocky", "almalinux", "ol", "amzn":
			return true, true
		case "debian", "ubuntu", "linuxmint", "pop", "elementary", "raspbian", "kali", "neon", "zorin":
			return false, true
		}
	}
	return false, false
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

// resolveHomeOwner records the owner of the user's home directory. certutil is
// executed with these credentials so root never writes through user-controlled
// NSS paths.
func (m *TrustManager) resolveHomeOwner() {
	info, err := os.Stat(m.userHome)
	if err != nil {
		m.ownerErr = fmt.Errorf("inspect user home %s: %w", m.userHome, err)
		return
	}
	if !info.IsDir() {
		m.ownerErr = fmt.Errorf("user home %s is not a directory", m.userHome)
		return
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		m.ownerErr = fmt.Errorf("cannot determine the owner of %s", m.userHome)
		return
	}
	m.ownerUID = int(stat.Uid)
	m.ownerGID = int(stat.Gid)
	if os.Geteuid() != 0 && m.ownerUID != os.Geteuid() {
		m.ownerErr = fmt.Errorf(
			"user home %s is owned by uid %d but the helper runs as uid %d",
			m.userHome, m.ownerUID, os.Geteuid())
	}
}

// chownToOwner repairs ownership of a path created by the privileged helper.
// It is a no-op for an unprivileged helper (the home owner already created the
// path) and uses Lchown so a symlink cannot redirect the ownership change.
func (m *TrustManager) chownCreatedDir(path string) error {
	if m.ownerErr != nil {
		return m.ownerErr
	}
	if os.Geteuid() != 0 {
		return nil
	}
	if m.ownerUID == os.Geteuid() && m.ownerGID == os.Getegid() {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Chown(m.ownerUID, m.ownerGID)
}

func (m *TrustManager) chownToOwner(path string) error {
	if m.ownerErr != nil {
		return m.ownerErr
	}
	if os.Geteuid() != 0 {
		return nil
	}
	if m.ownerUID == os.Geteuid() && m.ownerGID == os.Getegid() {
		return nil
	}
	return os.Lchown(path, m.ownerUID, m.ownerGID)
}

// runCertutil executes certutil as the home owner when the runner supports
// credential switching. Fakes that only implement CommandRunner keep working.
func (m *TrustManager) runCertutil(ctx context.Context, args ...string) ([]byte, error) {
	if m.ownerErr != nil {
		return nil, m.ownerErr
	}
	runner, ok := m.runner.(UserCommandRunner)
	if !ok {
		return m.runner.Run(ctx, "certutil", args...)
	}
	if os.Geteuid() != 0 && m.ownerUID != os.Geteuid() {
		return nil, fmt.Errorf("refusing to run certutil as uid %d", m.ownerUID)
	}
	return runner.RunAs(ctx, m.ownerUID, m.ownerGID, "certutil", args...)
}

func (m *TrustManager) installNSS(ctx context.Context) []Target {
	if _, ok := m.certutilPath(); !ok {
		return []Target{
			{Name: "Chrome", OK: true, Detail: "not installed"},
			{Name: "Firefox", OK: true, Detail: "not installed"},
		}
	}

	targets := []Target{
		m.installNSSDB(ctx, "Chrome", filepath.Join(m.userHome, ".pki", "nssdb"), true),
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
		if err := m.checkNSSDir(profile.Dir); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				targets = append(targets, Target{Name: name, OK: true, Detail: "not installed"})
			} else {
				targets = append(targets, Target{Name: name, OK: false, Detail: "invalid Firefox profile directory: " + detailError(err)})
			}
			continue
		}
		targets = append(targets, m.installNSSDB(ctx, name, profile.Dir, false))
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
		if err := m.checkNSSDir(profile.Dir); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				targets = append(targets, Target{Name: name, OK: true, Detail: "not installed"})
			} else {
				targets = append(targets, Target{Name: name, OK: false, Detail: "invalid Firefox profile directory: " + detailError(err)})
			}
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
		if err := m.checkNSSDir(profile.Dir); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				targets = append(targets, Target{Name: name, OK: true, Detail: "not installed"})
			} else {
				targets = append(targets, Target{Name: name, OK: false, Detail: "invalid Firefox profile directory: " + detailError(err)})
			}
			continue
		}
		targets = append(targets, m.statusNSSDB(ctx, name, profile.Dir))
	}
	return targets
}

func (m *TrustManager) installNSSDB(ctx context.Context, name, db string, create bool) Target {
	var err error
	if create {
		err = m.ensureNSSDir(db)
	} else {
		err = m.checkNSSDir(db)
	}
	if err != nil {
		return Target{Name: name, OK: false, Detail: "prepare NSS database directory: " + detailError(err)}
	}
	if err := openDirNoFollow(db); err != nil {
		return Target{Name: name, OK: false, Detail: "open NSS database directory: " + detailError(err)}
	}
	if err := checkNSSDatabaseFiles(db); err != nil {
		return Target{Name: name, OK: false, Detail: "invalid NSS database: " + detailError(err)}
	}
	// Do not chown an existing NSS database as root. certutil runs as the home
	// owner, so files it creates are already correctly owned. Chowning a path
	// inside the user's home would let a symlink race change ownership of a
	// root-owned file.

	initialized, err := nssDatabaseInitialized(db)
	if err != nil {
		return Target{Name: name, OK: false, Detail: "inspect NSS database: " + detailError(err)}
	}
	if !initialized {
		if _, err := m.runCertutil(ctx, "-N", "--empty-password", "-d", "sql:"+db); err != nil {
			return Target{Name: name, OK: false, Detail: "create NSS database: " + detailError(err)}
		}
		if err := checkNSSDatabaseFiles(db); err != nil {
			return Target{Name: name, OK: false, Detail: "invalid NSS database after creation: " + detailError(err)}
		}
	}

	caFile, err := m.writeTempCertificate()
	if err != nil {
		return Target{Name: name, OK: false, Detail: "stage CA certificate: " + detailError(err)}
	}
	defer func() { _ = os.Remove(caFile) }()
	if _, err := m.runCertutil(ctx, "-A", "-n", nssNickname, "-t", "C,,", "-i", caFile, "-d", "sql:"+db); err != nil {
		return Target{Name: name, OK: false, Detail: "install CA in NSS database: " + detailError(err)}
	}
	if err := checkNSSDatabaseFiles(db); err != nil {
		return Target{Name: name, OK: false, Detail: "invalid NSS database after install: " + detailError(err)}
	}
	return Target{Name: name, OK: true, Detail: "installed"}
}

func (m *TrustManager) removeNSSNickname(ctx context.Context, name, db string) Target {
	if err := m.checkNSSDir(db); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Target{Name: name, OK: true, Detail: "not installed"}
		}
		return Target{Name: name, OK: false, Detail: "invalid NSS database directory: " + detailError(err)}
	}
	if err := openDirNoFollow(db); err != nil {
		return Target{Name: name, OK: false, Detail: "open NSS database directory: " + detailError(err)}
	}
	if err := checkNSSDatabaseFiles(db); err != nil {
		return Target{Name: name, OK: false, Detail: "invalid NSS database: " + detailError(err)}
	}
	initialized, err := nssDatabaseInitialized(db)
	if err != nil {
		return Target{Name: name, OK: false, Detail: "inspect NSS database: " + detailError(err)}
	}
	if !initialized {
		return Target{Name: name, OK: true, Detail: "not installed"}
	}
	present, err := m.nssNicknameState(ctx, db)
	if err != nil {
		return Target{Name: name, OK: false, Detail: "list NSS database: " + detailError(err)}
	}
	if !present {
		return Target{Name: name, OK: true, Detail: "not installed"}
	}
	if _, err := m.runCertutil(ctx, "-D", "-n", nssNickname, "-d", "sql:"+db); err != nil {
		return Target{Name: name, OK: false, Detail: "remove CA from NSS database: " + detailError(err)}
	}
	return Target{Name: name, OK: true, Detail: "removed"}
}

func (m *TrustManager) statusNSSDB(ctx context.Context, name, db string) Target {
	if err := m.checkNSSDir(db); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Target{Name: name, OK: true, Detail: "not installed"}
		}
		return Target{Name: name, OK: false, Detail: "invalid NSS database directory: " + detailError(err)}
	}
	if err := openDirNoFollow(db); err != nil {
		return Target{Name: name, OK: false, Detail: "open NSS database directory: " + detailError(err)}
	}
	if err := checkNSSDatabaseFiles(db); err != nil {
		return Target{Name: name, OK: false, Detail: "invalid NSS database: " + detailError(err)}
	}
	initialized, err := nssDatabaseInitialized(db)
	if err != nil {
		return Target{Name: name, OK: false, Detail: "inspect NSS database: " + detailError(err)}
	}
	if !initialized {
		return Target{Name: name, OK: true, Detail: "not installed"}
	}
	present, err := m.nssNicknameState(ctx, db)
	if err != nil {
		return Target{Name: name, OK: false, Detail: "list NSS database: " + detailError(err)}
	}
	if present {
		return Target{Name: name, OK: true, Detail: "installed"}
	}
	return Target{Name: name, OK: false, Detail: "not installed"}
}

// nssNicknameState lists the whole database so a certutil/database error is
// surfaced rather than mistaken for an absent nickname. The nickname column is
// padded to the trust column, so require the padding after an exact prefix.
func (m *TrustManager) nssNicknameState(ctx context.Context, db string) (bool, error) {
	output, err := m.runCertutil(ctx, "-L", "-d", "sql:"+db)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == nssNickname {
			return true, nil
		}
		if !strings.HasPrefix(line, nssNickname) {
			continue
		}
		rest := strings.TrimPrefix(line, nssNickname)
		if strings.HasPrefix(rest, "  ") || strings.HasPrefix(rest, "\t") {
			return true, nil
		}
	}
	return false, nil
}

func nssDatabaseInitialized(db string) (bool, error) {
	info, err := os.Lstat(filepath.Join(db, "cert9.db"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("cert9.db is not a regular file")
	}
	return true, nil
}

// checkNSSDatabaseFiles rejects symlinks and non-regular database files. A
// user can otherwise point cert9.db, key4.db, or pkcs11.txt outside the home.
func checkNSSDatabaseFiles(db string) error {
	for _, name := range nssDBFileNames {
		path := filepath.Join(db, name)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", name)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", name)
		}
	}
	return nil
}

// checkNSSDir walks every component below the home and rejects symlinks or
// non-directories. It does not create anything.
func (m *TrustManager) checkNSSDir(path string) error {
	return m.walkNSSDir(path, false)
}

// ensureNSSDir is checkNSSDir with creation of missing components. Components
// created by the helper are chowned to the home owner.
func (m *TrustManager) ensureNSSDir(path string) error {
	return m.walkNSSDir(path, true)
}

func (m *TrustManager) walkNSSDir(path string, create bool) error {
	if m.userHome == "" {
		return errors.New("user home directory is empty")
	}
	if !pathWithinHome(m.userHome, path) {
		return errors.New("NSS database path is outside the user's home")
	}
	rel, err := filepath.Rel(m.userHome, path)
	if err != nil {
		return err
	}
	current := m.userHome
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s is a symlink", current)
			}
			if !info.IsDir() {
				return fmt.Errorf("%s is not a directory", current)
			}
			// Never chown a pre-existing component by path. The user can swap
			// an ancestor for a symlink after the Lstat check; only directories
			// this process created are chowned, through a no-follow fd.
		case errors.Is(err, fs.ErrNotExist):
			if !create {
				return err
			}
			if err := os.Mkdir(current, 0o700); err != nil {
				if !errors.Is(err, fs.ErrExist) {
					return err
				}
				// A concurrent creator won the race; validate its result.
				info, statErr := os.Lstat(current)
				if statErr != nil {
					return statErr
				}
				if info.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("%s is a symlink", current)
				}
				if !info.IsDir() {
					return fmt.Errorf("%s is not a directory", current)
				}
				continue
			}
			if err := m.chownCreatedDir(current); err != nil {
				return fmt.Errorf("chown %s: %w", current, err)
			}
		default:
			return err
		}
	}
	return nil
}

// openDirNoFollow validates the final database directory through a no-follow
// handle. Privilege-dropped certutil remains the primary protection against
// races after this check.
func openDirNoFollow(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	info, statErr := f.Stat()
	closeErr := f.Close()
	if statErr != nil {
		return statErr
	}
	if closeErr != nil {
		return closeErr
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

func (m *TrustManager) writeTempCertificate() (string, error) {
	// Use the world-traversable system temp directory: certutil runs as the
	// home owner and must be able to read the staged certificate even when the
	// helper's TMPDIR is not accessible to that user.
	f, err := os.CreateTemp("/tmp", "devstack-root-*.crt")
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
	if _, err := f.Write(m.rootPEM); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	// certutil runs as the owner and must be able to read the staged cert.
	if err := m.chownToOwner(path); err != nil {
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
	if !pathWithinHome(home, path) {
		return nil, false, errors.New("profiles.ini is outside the user's home")
	}
	data, err := readProfilesINI(path)
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
	scanner.Buffer(make([]byte, 0, 64*1024), maxProfilesIniSize)
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

// readProfilesINI reads profiles.ini without following a symlink and without
// blocking on a FIFO. It also bounds the amount of attacker-controlled data.
func readProfilesINI(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("profiles.ini is a symlink")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("profiles.ini is not a regular file")
	}
	if info.Size() > maxProfilesIniSize {
		return nil, fmt.Errorf("profiles.ini is larger than %d bytes", maxProfilesIniSize)
	}

	// O_NONBLOCK prevents a regular-file/FIFO race from blocking the helper.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() {
		return nil, errors.New("profiles.ini is not a regular file")
	}
	if opened.Size() > maxProfilesIniSize {
		return nil, fmt.Errorf("profiles.ini is larger than %d bytes", maxProfilesIniSize)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxProfilesIniSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxProfilesIniSize {
		return nil, fmt.Errorf("profiles.ini is larger than %d bytes", maxProfilesIniSize)
	}
	return data, nil
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

// RunAs executes a command with the home owner's credentials. NoSetGroups is
// required for an unprivileged helper: setgroups(2) is root-only even when the
// target uid/gid match the current process.
func (execRunner) RunAs(ctx context.Context, uid, gid int, name string, args ...string) ([]byte, error) {
	if uid < 0 || gid < 0 {
		return nil, fmt.Errorf("invalid credentials uid=%d gid=%d", uid, gid)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cred := &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	if os.Geteuid() != 0 {
		cred.NoSetGroups = true
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
	// Do not pass the root helper environment (which may contain secrets) to
	// the unprivileged certutil process.
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LANG=C",
		"LC_ALL=C",
	}
	if account, err := user.LookupId(strconv.Itoa(uid)); err == nil && account.HomeDir != "" {
		env = append(env, "HOME="+account.HomeDir)
	}
	cmd.Env = env
	return runCombined(cmd)
}

var _ UserCommandRunner = execRunner{}
