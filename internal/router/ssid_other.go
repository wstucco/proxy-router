//go:build !darwin && !linux

package router

func fetchSSID() string {
	return ""
}
