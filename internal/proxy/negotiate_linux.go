//go:build linux

package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/jcmturner/gokrb5/v8/client"
	"github.com/jcmturner/gokrb5/v8/config"
	"github.com/jcmturner/gokrb5/v8/credentials"
	"github.com/jcmturner/gokrb5/v8/messages"
	"github.com/jcmturner/gokrb5/v8/types"
)

func init() {
	negotiateDial = gokrb5NegotiateDial
}

// gokrb5NegotiateDial attempts to authenticate to the upstream proxy using
// Kerberos/Negotiate via gokrb5 (pure Go).
func gokrb5NegotiateDial(proxyURL *url.URL, target string, dialer *net.Dialer) (_ net.Conn, retErr error) {
	conn, err := dialer.DialContext(context.Background(), "tcp", proxyURL.Host)
	if err != nil {
		return nil, fmt.Errorf("connect to proxy %s: %w", proxyURL.Host, err)
	}
	defer func() {
		if retErr != nil {
			conn.Close()
		}
	}()

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	if _, err := fmt.Fprint(conn, req); err != nil {
		return nil, fmt.Errorf("send CONNECT: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		pkgLog.Debug("gokrb5: no auth required for %s", proxyURL.Host)
		return conn, nil
	}

	if resp.StatusCode != http.StatusProxyAuthRequired {
		return nil, fmt.Errorf("unexpected status from proxy: %s", resp.Status)
	}

	if !hasNegotiateScheme(resp.Header["Proxy-Authenticate"]) {
		return nil, fmt.Errorf("proxy does not advertise Negotiate")
	}

	pkgLog.Debug("gokrb5: proxy %s requests Negotiate", proxyURL.Host)

	spn := "HTTP/" + canonicalizeHostname(proxyURL.Host)
	pkgLog.Debug("gokrb5: SPN = %s", spn)

	krb5Client, err := newGokrb5Client()
	if err != nil {
		return nil, fmt.Errorf("creating gokrb5 client: %w", err)
	}
	defer krb5Client.Destroy()

	tkt, encKey, err := krb5Client.GetServiceTicket(spn)
	if err != nil {
		return nil, fmt.Errorf("getting service ticket for %s: %w", spn, err)
	}

	cname := krb5Client.Credentials.CName()
	realm := krb5Client.Credentials.Realm()
	auth, err := types.NewAuthenticator(realm, cname)
	if err != nil {
		return nil, fmt.Errorf("creating authenticator: %w", err)
	}

	apReq, err := messages.NewAPReq(tkt, encKey, auth)
	if err != nil {
		return nil, fmt.Errorf("building AP-REQ: %w", err)
	}

	// Wrap Kerberos AP-REQ in SPNEGO NegTokenInit
	negToken, err := buildSPNEGOToken(apReq)
	if err != nil {
		return nil, fmt.Errorf("building SPNEGO token: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(negToken)
	pkgLog.Debug("gokrb5: sending CONNECT Negotiate (%d bytes base64)", len(encoded))
	authReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Negotiate %s\r\n\r\n",
		target, target, encoded)
	if _, err := fmt.Fprint(conn, authReq); err != nil {
		return nil, fmt.Errorf("send CONNECT Negotiate: %w", err)
	}

	authResp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return nil, fmt.Errorf("read CONNECT Negotiate response: %w", err)
	}
	authResp.Body.Close()

	pkgLog.Debug("gokrb5: CONNECT Negotiate → %d", authResp.StatusCode)

	if authResp.StatusCode == http.StatusOK {
		return conn, nil
	}

	return nil, fmt.Errorf("Negotiate failed (status=%d)", authResp.StatusCode)
}

// newGokrb5Client creates a gokrb5 client from the system credential cache.
func newGokrb5Client() (*client.Client, error) {
	cfgPath := "/etc/krb5.conf"
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("loading krb5 config %s: %w", cfgPath, err)
	}

	ccachePath := defaultCCachePath()
	ccache, err := credentials.LoadCCache(ccachePath)
	if err != nil {
		return nil, fmt.Errorf("loading ccache %s: %w", ccachePath, err)
	}

	krb5Client, err := client.NewFromCCache(ccache, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating client from ccache: %w", err)
	}

	return krb5Client, nil
}

func defaultCCachePath() string {
	if v := os.Getenv("KRB5CCNAME"); v != "" {
		return v
	}
	uid := os.Getuid()
	return filepath.Join("/tmp", fmt.Sprintf("krb5cc_%d", uid))
}

// buildSPNEGOToken constructs a SPNEGO NegTokenInit containing a Kerberos AP-REQ.
func buildSPNEGOToken(apReq messages.APReq) ([]byte, error) {
	// SPNEGO NegTokenInit (RFC 4178)
	// Application tag 0x60, followed by constructed SEQUENCE
	// OID: 1.3.6.1.5.5.2 (SPNEGO)
	// MechTypes: OID 1.2.840.113554.1.2.2 (Kerberos v5)
	// MechToken: DER-encoded AP-REQ

	apReqBytes, err := apReq.Marshal()
	if err != nil {
		return nil, err
	}

	// MechType OID for Kerberos v5: 1.2.840.113554.1.2.2
	mechOID := []byte{0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x12, 0x01, 0x02, 0x02}

	// MechToken (OCTET STRING wrapping AP-REQ)
	mechToken := append([]byte{0x04, 0x82}, byte(len(apReqBytes)>>8), byte(len(apReqBytes)))
	mechToken = append(mechToken, apReqBytes...)

	// NegTokenInit SEQUENCE
	// Tag: MechTypes [0] EXPLICIT (0xa0), MechToken [2] EXPLICIT (0xa2)
	mechTypes := append([]byte{0xa0, 0x0b}, mechOID...)
	mechTokenField := append([]byte{0xa2, 0x82}, byte((len(mechToken)+2)>>8), byte((len(mechToken)+2)&0xff))
	mechTokenField = append(mechTokenField, mechToken...)

	inner := append(mechTypes, mechTokenField...)

	// Outer APPLICATION 0 tag wrapping the SEQUENCE
	result := append([]byte{0x60, 0x82}, byte(len(inner)>>8), byte(len(inner)))
	result = append(result, inner...)

	return result, nil
}
