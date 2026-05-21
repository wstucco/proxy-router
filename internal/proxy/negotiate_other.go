//go:build !darwin || !cgo

package proxy

func init() {
	negotiateDial = nil
}
