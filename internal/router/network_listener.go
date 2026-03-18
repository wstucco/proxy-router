package router

/*
#cgo LDFLAGS: -framework SystemConfiguration -framework CoreFoundation

#include <SystemConfiguration/SystemConfiguration.h>
#include <CoreFoundation/CoreFoundation.h>

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
    CFStringRef keyWifi = CFSTR("State:/Network/Interface/en0/AirPort");

    CFArrayRef keys = CFArrayCreate(NULL, (const void *[]){key, keyWifi}, 2, &kCFTypeArrayCallBacks);
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
	"log"
	"sync/atomic"
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
	ssid := fetchSSID()
	cachedSSID.Store(ssid)
	log.Printf("[network] SSID changed → %q", ssid)
}

// StartNetworkListener seeds the SSID cache and then blocks listening for
// network changes. Run it in a goroutine from cmdRun only.
func StartNetworkListener() {
	// Seed cache on first call
	cachedSSID.Store(fetchSSID())

	store := C.createStore(nil)
	if store == 0 {
		log.Println("[network] failed to create SCDynamicStore, falling back to per-request lookup")
		return
	}
	defer C.CFRelease(C.CFTypeRef(store))
	log.Println("[network] listening for network changes")
	C.startListening(store)
}
