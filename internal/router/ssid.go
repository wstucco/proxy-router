package router

import (
	"os/exec"
	"strings"
)

// CurrentSSID returns the current Wi-Fi SSID on macOS, or "" if not connected.
func CurrentSSID() string {
	// macOS 13+: use wdutil info (requires no sudo for SSID)
	// Fallback: airport utility
	out, err := exec.Command(
		"/System/Library/PrivateFrameworks/Apple80211.framework/Versions/Current/Resources/airport",
		"-I",
	).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "SSID: ") {
				return strings.TrimPrefix(line, "SSID: ")
			}
		}
	}

	// macOS 14+ fallback via networksetup
	out2, err2 := exec.Command("networksetup", "-getairportnetwork", "en0").Output()
	if err2 == nil {
		s := strings.TrimSpace(string(out2))
		if idx := strings.Index(s, ": "); idx != -1 {
			return s[idx+2:]
		}
	}

	return ""
}
