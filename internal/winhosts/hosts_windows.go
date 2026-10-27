//go:build windows

// Package winhosts manages only devstack's marked block in the Windows hosts
// file; all unrelated lines remain outside the replacement range.
package winhosts

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/windows"

	"github.com/AramZahedi/dev-web-stack/internal/hosts"
)

const (
	BeginMarker = hosts.BeginMarker
	EndMarker   = hosts.EndMarker
)

// Render replaces the devstack block without changing unrelated entries.
func Render(existing string, domains []string) (string, error) {
	lineEnding := "\n"
	if strings.Contains(existing, "\r\n") {
		lineEnding = "\r\n"
		existing = strings.ReplaceAll(existing, "\r\n", "\n")
	}
	rendered, err := hosts.Render(existing, domains)
	if err != nil {
		return "", err
	}
	if lineEnding == "\r\n" {
		rendered = strings.ReplaceAll(rendered, "\n", "\r\n")
	}
	return rendered, nil
}

// Parse reports mappings in the managed block.
func Parse(content string) ([]string, bool) { return hosts.Parse(content) }

// Manager serializes updates to one hosts file.
type Manager struct {
	Path string
	mu   sync.Mutex
}

func NewManager(path string) *Manager { return &Manager{Path: path} }

func (m *Manager) Sync(domains []string) (bool, error) {
	if m == nil || m.Path == "" {
		return false, errors.New("winhosts: hosts path is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, err := os.ReadFile(m.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("winhosts: read hosts file: %w", err)
	}
	rendered, err := Render(string(existing), domains)
	if err != nil {
		return false, err
	}
	if string(existing) == rendered {
		return false, nil
	}
	clearReadOnly(m.Path)
	if err := hosts.WriteAtomic(m.Path, []byte(rendered), 0); err != nil {
		return false, fmt.Errorf("winhosts: write hosts file: %w", err)
	}
	return true, nil
}

func clearReadOnly(path string) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil || (attrs&windows.FILE_ATTRIBUTE_READONLY == 0) {
		return
	}
	_ = windows.SetFileAttributes(p, attrs&^windows.FILE_ATTRIBUTE_READONLY)
}

func (m *Manager) Status() (domains []string, found bool, err error) {
	if m == nil || m.Path == "" {
		return nil, false, errors.New("winhosts: hosts path is empty")
	}
	data, err := os.ReadFile(m.Path)
	if err != nil {
		return nil, false, fmt.Errorf("winhosts: read hosts file: %w", err)
	}
	domains, found = Parse(string(data))
	return domains, found, nil
}
