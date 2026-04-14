//go:build !darwin

package router

import "log"

func StartNetworkListener() {
	log.Println("[network] network listener not supported on this platform")
}

func CurrentSSID() string {
	return ""
}
