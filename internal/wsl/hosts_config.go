package wsl

import (
	"regexp"
	"strings"
)

var generateHostsSetting = regexp.MustCompile(`(?i)^\s*generateHosts\s*=`)

// DisableGeneratedHosts updates the [network] setting while preserving other
// WSL configuration sections and values. The agent then owns devstack host
// entries without WSL overwriting unrelated /etc/hosts lines on boot.
func DisableGeneratedHosts(config string) string {
	trimmed := strings.TrimRight(config, "\r\n")
	var lines []string
	if trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}
	var out []string
	section := ""
	networkFound := false
	settingWritten := false
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(trimmedLine, "[") && strings.HasSuffix(trimmedLine, "]") {
			if section == "network" && networkFound && !settingWritten {
				out = append(out, "generateHosts=false")
				settingWritten = true
			}
			section = strings.ToLower(strings.TrimSpace(trimmedLine[1 : len(trimmedLine)-1]))
			out = append(out, line)
			if section == "network" && !networkFound {
				out = append(out, "generateHosts=false")
				networkFound = true
				settingWritten = true
			}
			continue
		}
		if section == "network" && generateHostsSetting.MatchString(trimmedLine) {
			continue
		}
		out = append(out, line)
	}
	if section == "network" && networkFound && !settingWritten {
		out = append(out, "generateHosts=false")
		settingWritten = true
	}
	if !networkFound {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		out = append(out, "[network]", "generateHosts=false")
	}
	return strings.Join(out, "\n") + "\n"
}
