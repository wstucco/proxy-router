// Package procinfo resolves the process owning a local TCP port.
// Used by the connections view to attribute proxy clients (little
// snitch style). Darwin-only for now; other platforms return
// errors.ErrUnsupported and the UI shows "?".
package procinfo

// Result identifies the process owning a local TCP port.
type Result struct {
	PID  int
	Name string
}
