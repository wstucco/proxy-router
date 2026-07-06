//go:build darwin

package router

/*
#cgo LDFLAGS: -framework SystemConfiguration -framework CoreFoundation -framework Security

#include <SystemConfiguration/SystemConfiguration.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Authorization.h>

extern void networkDidChange(SCDynamicStoreRef store, CFArrayRef changedKeys, void *info);

static SCDynamicStoreRef createStore(void *info) {
    SCDynamicStoreContext ctx = {0, info, NULL, NULL, NULL};
    SCDynamicStoreRef store = SCDynamicStoreCreate(
        NULL,
        CFSTR("proxy-router"),
        networkDidChange,
        &ctx
    );
    return store;
}

static void startListening(SCDynamicStoreRef store) {
    CFStringRef key = SCDynamicStoreKeyCreateNetworkInterfaceEntity(
        NULL,
        kSCDynamicStoreDomainState,
        kSCCompAnyRegex,
        kSCEntNetAirPort
    );
    CFArrayRef keys = CFArrayCreate(NULL, (const void *[]){key}, 1, &kCFTypeArrayCallBacks);
    SCDynamicStoreSetNotificationKeys(store, NULL, keys);
    CFRelease(keys);
    CFRelease(key);

    CFRunLoopSourceRef src = SCDynamicStoreCreateRunLoopSource(NULL, store, 0);
    CFRunLoopAddSource(CFRunLoopGetCurrent(), src, kCFRunLoopDefaultMode);
    CFRelease(src);

    CFRunLoopRun();
}
*/
import "C"
import (
	"sync/atomic"
	"time"
	"unsafe"
)

var cachedSSID atomic.Value // stores string

// CurrentSSID returns the cached SSID — updated on network change events.
// Returns "" until StartNetworkListener has been called.
func CurrentSSID() string {
	v, _ := cachedSSID.Load().(string)
	return v
}

//export networkDidChange
func networkDidChange(store C.SCDynamicStoreRef, changedKeys C.CFArrayRef, info unsafe.Pointer) {
	_, _, _ = store, changedKeys, info
	go func() {
		time.Sleep(250 * time.Millisecond)
		ssid := fetchSSID()
		prev, _ := cachedSSID.Load().(string)
		if ssid == prev {
			return
		}
		cachedSSID.Store(ssid)
		if cfg := currentConfig.Load(); cfg != nil {
			locName, proxyPAC := SSIDLocationInfo(cfg, ssid)
			if locName != "" {
				pkgLog.Info("SSID changed → %q — location %q (%s)", ssid, locName, proxyPAC)
			} else {
				pkgLog.Info("SSID changed → %q — %s", ssid, proxyPAC)
			}
		} else {
			pkgLog.Debug("SSID changed → %q", ssid)
		}
	}()
}

// StartNetworkListener seeds the SSID cache and then blocks listening for
// network changes. Run it in a goroutine from cmdRun only.
func StartNetworkListener() {
	cachedSSID.Store(fetchSSID())

	store := C.createStore(nil)
	if store == 0 {
		pkgLog.Warn("failed to create SCDynamicStore, falling back to per-request lookup")
		return
	}
	defer C.CFRelease(C.CFTypeRef(store))
	pkgLog.Info("listening for network changes")
	C.startListening(store)
}
