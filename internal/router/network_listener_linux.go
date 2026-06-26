//go:build linux

package router

import (
	"sync/atomic"
	"time"
)

var cachedSSIDLinux atomic.Value

func CurrentSSID() string {
	v, _ := cachedSSIDLinux.Load().(string)
	return v
}

func StartNetworkListener() {
	cachedSSIDLinux.Store(fetchSSID())

	go func() {
		for {
			time.Sleep(5 * time.Second)
			ssid := fetchSSID()
			prev, _ := cachedSSIDLinux.Load().(string)
			if ssid == prev {
				continue
			}
			cachedSSIDLinux.Store(ssid)
			pkgLog.Debug("SSID changed → %q", ssid)
		}
	}()
}
