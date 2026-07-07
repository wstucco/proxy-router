//go:build !darwin

package procinfo

import "errors"

// Lookup is unsupported outside darwin; the connections view shows "?".
// Linux support (/proc/net/tcp + /proc/*/fd) is a possible follow-up.
func Lookup(localPort uint16) (Result, error) {
	return Result{}, errors.ErrUnsupported
}
