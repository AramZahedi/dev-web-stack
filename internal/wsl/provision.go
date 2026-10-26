package wsl

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	DefaultManagedDistro = "devstack"
	DefaultInstallDir    = `C:\devstack\wsl\devstack`
	managedMarkerPath    = "/data/devstack/managed.json"
	minimumWSLVersion    = "2.0.0"
)

var (
	ErrUnmanagedDistro      = errors.New("wsl: refusing to adopt an unmarked distro")
	ErrConfirmationRequired = errors.New("wsl: unregister requires explicit confirmation")
	ErrWSLVersion           = errors.New("wsl: WSL 2.0.0 or newer is required")
	ErrSystemdRequired      = errors.New("wsl: managed distro must run systemd as PID 1")
)

var distroNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var wslVersionPattern = regexp.MustCompile(`(?m)^\s*WSL version:\s*([0-9]+\.[0-9]+\.[0-9]+)`)

// ProvisionConfig contains only the fixed managed-distro inputs. RootFSPath
// must refer to a separately downloaded, checksum-verified Debian 12 archive.
type ProvisionConfig struct {
	DistroName       string
	InstallDir       string
	RootFSPath       string
	RootFSSHA256     string
	MinimumDiskBytes uint64
	ForwardingPort   int
	InstallID        string
}

// ManagedDistro reports the validated state of the managed distribution.
type ManagedDistro struct {
	Name      string
	InstallID string
	Imported  bool
}

// Provisioner imports and starts only the configured managed WSL distro.
// Commands are fixed; no caller-supplied command or shell fragment is run.
type Provisioner struct {
	Runner        Runner
	Config        ProvisionConfig
	FreeDiskBytes func(path string) (uint64, error)
}

type managedMarker struct {
	ManagedBy string `json:"managed_by"`
	InstallID string `json:"install_id"`
}

const initializeManagedDistroScript = `set -eu
install_id="$1"
case "$install_id" in *[!a-f0-9-]*|'') exit 2;; esac
mkdir -p /data/devstack
chown root:root /data/devstack
chmod 0755 /data/devstack
printf '{"managed_by":"devstack","install_id":"%s"}\n' "$install_id" > /data/devstack/managed.json
chown root:root /data/devstack/managed.json
chmod 0644 /data/devstack/managed.json
`

// Preflight checks the installed WSL runtime version before any distro is
// changed. The distro itself is always imported as WSL 2.
func (p *Provisioner) Preflight(ctx context.Context) error {
	if err := p.validate(); err != nil {
		return err
	}
	out, err := p.Runner.Run(ctx, "--version")
	if err != nil {
		return fmt.Errorf("wsl: query version: %w", err)
	}
	match := wslVersionPattern.FindSubmatch(out)
	if len(match) != 2 || compareVersion(string(match[1]), minimumWSLVersion) < 0 {
		return fmt.Errorf("%w: output %q", ErrWSLVersion, strings.TrimSpace(string(out)))
	}
	return nil
}

// Provision ensures the configured distro exists, is marked as devstack-owned,
// runs systemd, and is started. It never adopts or unregisters an unmarked name.
func (p *Provisioner) Provision(ctx context.Context) (ManagedDistro, error) {
	if err := p.Preflight(ctx); err != nil {
		return ManagedDistro{}, err
	}
	if p.Config.ForwardingPort < 0 || p.Config.ForwardingPort > 65535 {
		return ManagedDistro{}, fmt.Errorf("wsl: invalid localhost-forwarding port %d", p.Config.ForwardingPort)
	}
	if err := p.verifyRootFS(); err != nil {
		return ManagedDistro{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p.Config.InstallDir), 0o755); err != nil {
		return ManagedDistro{}, fmt.Errorf("wsl: create import parent: %w", err)
	}

	list, err := p.Runner.Run(ctx, "--list", "--quiet")
	if err != nil {
		return ManagedDistro{}, fmt.Errorf("wsl: list distributions: %w", err)
	}
	for _, name := range ParseDistroList(list) {
		if strings.EqualFold(name, p.Config.DistroName) {
			marker, err := p.readManagedMarker(ctx)
			if err != nil {
				if errors.Is(err, ErrUnmanagedDistro) {
					return ManagedDistro{}, err
				}
				return ManagedDistro{}, fmt.Errorf("wsl: validate managed distro: %w", err)
			}
			if err := p.start(ctx); err != nil {
				return ManagedDistro{}, err
			}
			if err := p.requireSystemd(ctx); err != nil {
				return ManagedDistro{}, err
			}
			if p.Config.ForwardingPort != 0 {
				if err := p.CheckLocalhostForwarding(ctx, p.Config.ForwardingPort); err != nil {
					return ManagedDistro{}, err
				}
			}
			return ManagedDistro{Name: p.Config.DistroName, InstallID: marker.InstallID}, nil
		}
	}

	if _, err := p.Runner.Run(ctx, "--import", p.Config.DistroName, p.Config.InstallDir, p.Config.RootFSPath, "--version", "2"); err != nil {
		return ManagedDistro{}, fmt.Errorf("wsl: import Debian 12 distro: %w", err)
	}
	if err := p.start(ctx); err != nil {
		return ManagedDistro{}, err
	}
	installID := p.Config.InstallID
	if installID == "" {
		installID, err = newInstallID()
		if err != nil {
			return ManagedDistro{}, err
		}
	}
	if !validInstallID(installID) {
		return ManagedDistro{}, errors.New("wsl: invalid install ID")
	}
	if _, err := p.Runner.Run(ctx, "-d", p.Config.DistroName, "--user", "root", "--", "bash", "-lc", initializeManagedDistroScript, "devstack-init", installID); err != nil {
		return ManagedDistro{}, fmt.Errorf("wsl: initialize managed marker: %w", err)
	}
	marker, err := p.readManagedMarker(ctx)
	if err != nil {
		return ManagedDistro{}, fmt.Errorf("wsl: verify managed marker: %w", err)
	}
	if marker.InstallID != installID {
		return ManagedDistro{}, errors.New("wsl: managed marker install ID does not match")
	}
	if err := p.requireSystemd(ctx); err != nil {
		return ManagedDistro{}, err
	}
	if p.Config.ForwardingPort != 0 {
		if err := p.CheckLocalhostForwarding(ctx, p.Config.ForwardingPort); err != nil {
			return ManagedDistro{}, err
		}
	}
	return ManagedDistro{Name: p.Config.DistroName, InstallID: installID, Imported: true}, nil
}

// CheckLocalhostForwarding checks whether this distro can reach the Windows
// host gateway at the selected daemon port. A zero port is never inferred.
func (p *Provisioner) CheckLocalhostForwarding(ctx context.Context, port int) error {
	if err := p.validateRunner(); err != nil {
		return err
	}
	return ProbeLocalhostForwarding(ctx, p.Runner, p.Config.DistroName, port)
}

// Unregister removes only a currently marked devstack distro and requires an
// explicit confirmation. It does not remove projects or host-side backups.
func (p *Provisioner) Unregister(ctx context.Context, confirmed bool) error {
	if !confirmed {
		return ErrConfirmationRequired
	}
	if err := p.validate(); err != nil {
		return err
	}
	list, err := p.Runner.Run(ctx, "--list", "--quiet")
	if err != nil {
		return fmt.Errorf("wsl: list distributions: %w", err)
	}
	found := false
	for _, name := range ParseDistroList(list) {
		if strings.EqualFold(name, p.Config.DistroName) {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	if _, err := p.readManagedMarker(ctx); err != nil {
		return err
	}
	if _, err := p.Runner.Run(ctx, "--terminate", p.Config.DistroName); err != nil {
		return fmt.Errorf("wsl: stop managed distro: %w", err)
	}
	if _, err := p.Runner.Run(ctx, "--unregister", p.Config.DistroName); err != nil {
		return fmt.Errorf("wsl: unregister managed distro: %w", err)
	}
	return nil
}

func (p *Provisioner) readManagedMarker(ctx context.Context) (managedMarker, error) {
	out, err := p.Runner.Run(ctx, "-d", p.Config.DistroName, "--", "cat", managedMarkerPath)
	if err != nil {
		return managedMarker{}, fmt.Errorf("%w: marker is missing", ErrUnmanagedDistro)
	}
	var marker managedMarker
	if err := json.Unmarshal(out, &marker); err != nil || marker.ManagedBy != "devstack" || !validInstallID(marker.InstallID) {
		return managedMarker{}, fmt.Errorf("%w: invalid marker", ErrUnmanagedDistro)
	}
	return marker, nil
}

func (p *Provisioner) requireSystemd(ctx context.Context) error {
	out, err := p.Runner.Run(ctx, "-d", p.Config.DistroName, "--", "cat", "/proc/1/comm")
	if err != nil {
		return fmt.Errorf("wsl: check systemd: %w", err)
	}
	if strings.TrimSpace(string(out)) != "systemd" {
		return fmt.Errorf("%w: PID 1 is %q", ErrSystemdRequired, strings.TrimSpace(string(out)))
	}
	return nil
}

func (p *Provisioner) start(ctx context.Context) error {
	if _, err := p.Runner.Run(ctx, "-d", p.Config.DistroName, "--exec", "/bin/true"); err != nil {
		return fmt.Errorf("wsl: start distro: %w", err)
	}
	return nil
}

func (p *Provisioner) verifyRootFS() error {
	file, err := os.Open(p.Config.RootFSPath)
	if err != nil {
		return fmt.Errorf("wsl: open rootfs: %w", err)
	}
	defer file.Close()
	if len(p.Config.RootFSSHA256) != 64 {
		return errors.New("wsl: rootfs SHA-256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(p.Config.RootFSSHA256); err != nil {
		return fmt.Errorf("wsl: invalid rootfs SHA-256: %w", err)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return fmt.Errorf("wsl: hash rootfs: %w", err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, p.Config.RootFSSHA256) {
		return fmt.Errorf("wsl: rootfs SHA-256 mismatch: got %s", got)
	}
	parent := filepath.Dir(p.Config.InstallDir)
	freeDisk := p.FreeDiskBytes
	if freeDisk == nil {
		freeDisk = diskFreeBytes
	}
	available, err := freeDisk(parent)
	if err != nil {
		return fmt.Errorf("wsl: check import disk space: %w", err)
	}
	required := p.Config.MinimumDiskBytes
	if uint64(size) > ^uint64(0)/2 {
		return errors.New("wsl: rootfs size overflows disk-space requirement")
	}
	if expanded := uint64(size) * 2; expanded > required {
		required = expanded
	}
	if available < required {
		return fmt.Errorf("wsl: insufficient disk space: have %d bytes, need %d bytes", available, required)
	}
	return nil
}

func (p *Provisioner) validate() error {
	if err := p.validateRunner(); err != nil {
		return err
	}
	if p.Config.DistroName == "" {
		p.Config.DistroName = DefaultManagedDistro
	}
	if !distroNamePattern.MatchString(p.Config.DistroName) {
		return errors.New("wsl: invalid distro name")
	}
	if p.Config.InstallDir == "" {
		p.Config.InstallDir = DefaultInstallDir
	}
	if p.Config.RootFSPath == "" || !filepath.IsAbs(p.Config.RootFSPath) {
		return errors.New("wsl: rootfs path must be absolute")
	}
	return nil
}

func (p *Provisioner) validateRunner() error {
	if p == nil || p.Runner == nil {
		return errors.New("wsl: nil provisioner runner")
	}
	return nil
}

func newInstallID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("wsl: generate install ID: %w", err)
	}
	encoded := hex.EncodeToString(raw[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func validInstallID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(id, "-", "")
	if len(compact) != 32 {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}

func compareVersion(a, b string) int {
	left := strings.Split(a, ".")
	right := strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		l, _ := strconv.Atoi(left[i])
		r, _ := strconv.Atoi(right[i])
		if l < r {
			return -1
		}
		if l > r {
			return 1
		}
	}
	return 0
}
