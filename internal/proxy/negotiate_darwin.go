//go:build darwin && cgo

package proxy

/*
#cgo LDFLAGS: -framework GSS
#include <GSS/gssapi.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"unsafe"
)

func init() {
	negotiateDial = gssNegotiateDial
}

// gssNegotiateDial attempts to authenticate to the upstream proxy using
// Kerberos/Negotiate via Apple's GSS.framework.
func gssNegotiateDial(proxyURL *url.URL, target string, dialer *net.Dialer) (net.Conn, error) {
	conn, err := dialer.DialContext(context.Background(), "tcp", proxyURL.Host)
	if err != nil {
		return nil, fmt.Errorf("connect to proxy %s: %w", proxyURL.Host, err)
	}

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	if _, err := fmt.Fprint(conn, req); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send CONNECT: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		pkgLog.Debug("GSS: no auth required for %s", proxyURL.Host)
		return conn, nil
	}

	if resp.StatusCode != http.StatusProxyAuthRequired {
		conn.Close()
		return nil, fmt.Errorf("unexpected status from proxy: %s", resp.Status)
	}

	if !hasNegotiateScheme(resp.Header["Proxy-Authenticate"]) {
		conn.Close()
		return nil, fmt.Errorf("proxy does not advertise Negotiate")
	}

	pkgLog.Debug("GSS: proxy %s requests Negotiate", proxyURL.Host)

	spn := "HTTP@" + canonicalizeHostname(proxyURL.Host)
	pkgLog.Debug("GSS: SPN = %s", spn)

	// Round 1: send initial Negotiate token
	status, err := negotiateRoundTrip(conn, spn, target, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if status == http.StatusOK {
		pkgLog.Debug("GSS: Negotiate succeeded for %s", proxyURL.Host)
		return conn, nil
	}

	conn.Close()
	return nil, fmt.Errorf("Negotiate failed (need second round): status=%d", status)
}

// negotiateRoundTrip acquires GSS credentials, produces a Negotiate token,
// sends CONNECT with Proxy-Authorization: Negotiate, and returns the status.
func negotiateRoundTrip(conn net.Conn, spn, target string, serverToken []byte) (int, error) {
	// Import SPN
	cSpn := C.CString(spn)
	defer C.free(unsafe.Pointer(cSpn))

	var targetName C.gss_name_t
	var min C.OM_uint32
	maj := C.gss_import_name(&min, &C.gss_buffer_desc{
		value:  unsafe.Pointer(cSpn),
		length: C.size_t(len(spn)),
	}, C.GSS_C_NT_HOSTBASED_SERVICE, &targetName)
	if maj != C.GSS_S_COMPLETE {
		return 0, fmt.Errorf("gss_import_name failed (maj=%d,min=%d)", maj, min)
	}
	defer C.gss_release_name(&min, &targetName)
	pkgLog.Debug("GSS: imported name %s OK", spn)

	// Acquire default credentials
	var cred C.gss_cred_id_t
	maj = C.gss_acquire_cred(&min, C.GSS_C_NO_NAME, C.GSS_C_INDEFINITE, C.GSS_C_NO_OID_SET, C.GSS_C_INITIATE, &cred, nil, nil)
	if maj != C.GSS_S_COMPLETE {
		return 0, fmt.Errorf("gss_acquire_cred failed (maj=%d,min=%d)", maj, min)
	}
	defer C.gss_release_cred(&min, &cred)
	pkgLog.Debug("GSS: acquired credentials OK")

	// Init sec context
	var ctx C.gss_ctx_id_t
	var inBuf C.gss_buffer_t
	if len(serverToken) > 0 {
		inBuf = &C.gss_buffer_desc{
			value:  unsafe.Pointer(&serverToken[0]),
			length: C.size_t(len(serverToken)),
		}
		pkgLog.Debug("GSS: using server challenge (%d bytes)", len(serverToken))
	}

	var outBuf C.gss_buffer_desc
	maj = C.gss_init_sec_context(
		&min,
		cred,
		&ctx,
		targetName,
		C.GSS_C_NULL_OID,
		C.GSS_C_MUTUAL_FLAG,
		0,
		nil,
		inBuf,
		nil,
		&outBuf,
		nil,
		nil,
	)
	pkgLog.Debug("GSS: init_sec_context → major=%d, token=%d bytes", maj, outBuf.length)

	// Capture output token
	var token []byte
	if outBuf.length > 0 {
		token = C.GoBytes(outBuf.value, C.int(outBuf.length))
		C.gss_release_buffer(&min, &outBuf)
	}

	// Clean up context
	if ctx != nil {
		C.gss_delete_sec_context(&min, &ctx, nil)
	}

	if maj != C.GSS_S_COMPLETE && maj != C.GSS_S_CONTINUE_NEEDED {
		return 0, fmt.Errorf("gss_init_sec_context failed (maj=%d,min=%d)", maj, min)
	}

	if len(token) == 0 {
		return 0, fmt.Errorf("GSS produced empty token")
	}

	// Send CONNECT with Negotiate token
	encoded := base64.StdEncoding.EncodeToString(token)
	pkgLog.Debug("GSS: sending CONNECT Negotiate (%d bytes base64)", len(encoded))
	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Negotiate %s\r\n\r\n",
		target, target, encoded)
	if _, err := fmt.Fprint(conn, req); err != nil {
		return 0, fmt.Errorf("send CONNECT Negotiate: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, fmt.Errorf("read CONNECT Negotiate response: %w", err)
	}
	resp.Body.Close()

	pkgLog.Debug("GSS: CONNECT Negotiate → %d", resp.StatusCode)
	return resp.StatusCode, nil
}

func hasNegotiateScheme(headers []string) bool {
	for _, h := range headers {
		scheme := strings.SplitN(h, " ", 2)[0]
		if strings.EqualFold(strings.TrimSpace(scheme), "negotiate") {
			return true
		}
	}
	return false
}
