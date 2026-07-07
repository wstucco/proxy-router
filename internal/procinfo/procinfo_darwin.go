//go:build darwin

package procinfo

/*
#include <stdlib.h>
#include <string.h>
#include <libproc.h>
#include <sys/proc_info.h>
#include <arpa/inet.h>

// find_pid_by_local_tcp_port scans all processes for a TCP socket bound to
// the given local port and returns the owning pid, or -1 if not found.
// Buffer sizing is done here in C: the proc_info structs are unstable
// across SDKs and mirroring them in Go would be fragile.
static int find_pid_by_local_tcp_port(int port) {
	int bytes = proc_listpids(PROC_ALL_PIDS, 0, NULL, 0);
	if (bytes <= 0) return -1;
	pid_t *pids = malloc(bytes);
	if (!pids) return -1;
	bytes = proc_listpids(PROC_ALL_PIDS, 0, pids, bytes);
	int count = bytes / (int)sizeof(pid_t);
	int found = -1;

	for (int i = 0; i < count && found < 0; i++) {
		pid_t pid = pids[i];
		if (pid <= 0) continue;

		int fdbytes = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, NULL, 0);
		if (fdbytes <= 0) continue;
		struct proc_fdinfo *fds = malloc(fdbytes);
		if (!fds) continue;
		fdbytes = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, fds, fdbytes);
		int nfds = fdbytes / PROC_PIDLISTFD_SIZE;

		for (int j = 0; j < nfds; j++) {
			if (fds[j].proc_fdtype != PROX_FDTYPE_SOCKET) continue;
			struct socket_fdinfo si;
			int r = proc_pidfdinfo(pid, fds[j].proc_fd, PROC_PIDFDSOCKETINFO, &si, sizeof(si));
			if (r != sizeof(si)) continue;
			if (si.psi.soi_kind != SOCKINFO_TCP) continue;
			if ((int)ntohs(si.psi.soi_proto.pri_tcp.tcpsi_ini.insi_lport) == port) {
				found = pid;
				break;
			}
		}
		free(fds);
	}
	free(pids);
	return found;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// ErrNotFound means no process owns a TCP socket with that local port —
// typically the connection closed before the scan ran.
var ErrNotFound = errors.New("procinfo: no process found for port")

// Lookup finds the process owning the TCP socket with the given local port.
// The scan walks every pid/fd on the system (tens of ms) — call it off the
// hot path. Only same-user processes are visible without privileges, which
// is fine: proxy clients are the user's own processes.
func Lookup(localPort uint16) (Result, error) {
	pid := C.find_pid_by_local_tcp_port(C.int(localPort))
	if pid < 0 {
		return Result{}, ErrNotFound
	}
	var buf [C.PROC_PIDPATHINFO_MAXSIZE]C.char
	n := C.proc_name(C.int(pid), unsafe.Pointer(&buf[0]), C.uint32_t(len(buf)))
	name := ""
	if n > 0 {
		name = C.GoStringN(&buf[0], n)
	}
	return Result{PID: int(pid), Name: name}, nil
}
