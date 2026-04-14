//go:build !darwin

package router

func fetchSSID() string {
	return ""
}
