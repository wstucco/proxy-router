//go:build linux

package router

import (
	"os/exec"
	"strings"
)

func fetchSSID() string {
	iface := wifiInterfaceLinux()
	if iface == "" {
		return ""
	}

	out, err := exec.Command("iw", "dev", iface, "link").Output()
	if err != nil {
		return ""
	}

	return parseSSIDLinux(string(out))
}

func wifiInterfaceLinux() string {
	out, err := exec.Command("iw", "dev").Output()
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Interface") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1]
			}
		}
	}
	return ""
}

func parseSSIDLinux(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SSID:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}
