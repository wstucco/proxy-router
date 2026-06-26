//go:build (!darwin && !linux) || !cgo

package proxy

func init() {
	negotiateDial = nil
}
