package router

import (
	"os/exec"
	"strings"
)

// CurrentSSID returns the current Wi-Fi SSID on macOS, or "" if not connected.
func CurrentSSID() string {
	iface := wifiInterface()
	if iface == "" {
		return ""
	}

	out, err := exec.Command("ipconfig", "getsummary", iface).Output()
	if err != nil {
		return ""
	}

	return parseSSID(string(out))
}

// wifiInterface finds the BSD device name for the Wi-Fi/AirPort adapter.
func wifiInterface() string {
	out, err := exec.Command("networksetup", "-listallhardwareports").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	for i, line := range lines {
		if strings.Contains(line, "Wi-Fi") || strings.Contains(line, "AirPort") {
			if i+1 < len(lines) {
				fields := strings.Fields(lines[i+1])
				if len(fields) > 0 {
					return fields[len(fields)-1]
				}
			}
		}
	}
	return ""
}

// parseSSID extracts the SSID value from ipconfig getsummary output.
func parseSSID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SSID : ") {
			parts := strings.SplitN(line, ": ", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}
