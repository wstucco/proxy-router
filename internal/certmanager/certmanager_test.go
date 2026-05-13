package certmanager

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestNewManagerGeneratesCA(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cacert.pem")
	keyPath := filepath.Join(dir, "cakey.pem")

	mgr, err := NewManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}

	if mgr.caCert == nil {
		t.Fatal("caCert is nil after generation")
	}
	if mgr.caKey == nil {
		t.Fatal("caKey is nil after generation")
	}

	// CA cert file should exist
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		t.Error("cacert.pem not written")
	}
	// CA key file should exist
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Error("cakey.pem not written")
	}

	// Key should have 0600 permissions
	info, err := os.Stat(keyPath)
	if err == nil && info.Mode().Perm() != 0600 {
		t.Errorf("cakey.pem permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestNewManagerLoadsExistingCA(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cacert.pem")
	keyPath := filepath.Join(dir, "cakey.pem")

	mgr1, err := NewManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("first NewManager() failed: %v", err)
	}

	mgr2, err := NewManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("second NewManager() failed: %v", err)
	}

	if !mgr1.caCert.Equal(mgr2.caCert) {
		t.Error("loaded CA cert differs from generated one")
	}
}

func TestCertForHost(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "cacert.pem"), filepath.Join(dir, "cakey.pem"))
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}

	hosts := []string{"example.com", "*.example.com", "192.168.1.1", "api.internal.corp.com:443"}
	for _, host := range hosts {
		cert, err := mgr.CertForHost(host)
		if err != nil {
			t.Fatalf("CertForHost(%q) failed: %v", host, err)
		}
		if cert == nil {
			t.Fatalf("CertForHost(%q) returned nil", host)
		}
		if len(cert.Certificate) == 0 {
			t.Fatalf("CertForHost(%q): no certificate chain", host)
		}
	}
}

func TestCertCache(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "cacert.pem"), filepath.Join(dir, "cakey.pem"))
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}

	cert1, err := mgr.CertForHost("example.com")
	if err != nil {
		t.Fatal(err)
	}
	cert2, err := mgr.CertForHost("example.com")
	if err != nil {
		t.Fatal(err)
	}

	// Should be the same pointer (cached)
	if cert1 != cert2 {
		t.Error("CertForHost returned different instances for cached host")
	}
}

func TestCertSignedByCA(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "cacert.pem"), filepath.Join(dir, "cakey.pem"))
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}

	tlsCert, err := mgr.CertForHost("test.example.com")
	if err != nil {
		t.Fatal(err)
	}

	// Parse the leaf certificate
	cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Fatalf("parsing leaf cert: %v", err)
	}

	// Verify the certificate chain: leaf → CA
	roots := x509.NewCertPool()
	roots.AddCert(mgr.caCert)

	opts := x509.VerifyOptions{
		Roots: roots,
		DNSName: "test.example.com",
	}

	if _, err := cert.Verify(opts); err != nil {
		t.Errorf("certificate chain verification failed: %v", err)
	}
}

func TestCertHasSAN(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(filepath.Join(dir, "cacert.pem"), filepath.Join(dir, "cakey.pem"))
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}

	tlsCert, err := mgr.CertForHost("secure.example.com")
	if err != nil {
		t.Fatal(err)
	}

	cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		t.Fatalf("parsing leaf cert: %v", err)
	}

	found := false
	for _, dns := range cert.DNSNames {
		if dns == "secure.example.com" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("leaf cert DNSNames = %v, want 'secure.example.com'", cert.DNSNames)
	}
}

func TestCACertificatePEM(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cacert.pem")
	keyPath := filepath.Join(dir, "cakey.pem")

	_, err := NewManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}

	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("no PEM block found in cacert.pem")
	}
	if block.Type != "CERTIFICATE" {
		t.Errorf("PEM type = %q, want 'CERTIFICATE'", block.Type)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing CA cert PEM: %v", err)
	}
	if !cert.IsCA {
		t.Error("CA cert should have IsCA = true")
	}
}

func TestStripPort(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"example.com:443", "example.com"},
		{"example.com", "example.com"},
		{"192.168.1.1:8080", "192.168.1.1"},
		{"192.168.1.1", "192.168.1.1"},
		{"[::1]:443", "::1"},
		{"", ""},
	}

	for _, tt := range tests {
		got := stripPort(tt.input)
		if got != tt.want {
			t.Errorf("stripPort(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCACertPath(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cacert.pem")
	keyPath := filepath.Join(dir, "cakey.pem")

	mgr, err := NewManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}

	if mgr.CACertPath() != certPath {
		t.Errorf("CACertPath() = %q, want %q", mgr.CACertPath(), certPath)
	}
}
