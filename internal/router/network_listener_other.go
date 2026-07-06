//go:build !darwin && !linux

package router

func StartNetworkListener() {
	pkgLog.Info("network listener not supported on this platform")
}

func CurrentSSID() string {
	return ""
}
