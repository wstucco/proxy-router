package certmanager

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	logger "github.com/wstucco/proxy-router/internal/log"
)

const (
	caKeySize   = 2048
	certKeySize  = 2048
	caValidity   = 10 * 365 * 24 * time.Hour
	certValidity = 1 * 365 * 24 * time.Hour
)

var pkgLog = logger.New(logger.LevelInfo, "certmanager")

type Manager struct {
	caCertPath string
	caKeyPath  string
	caCert     *x509.Certificate
	caKey      crypto.PrivateKey
	cache      sync.Map
}

func NewManager(caCertPath, caKeyPath string) (*Manager, error) {
	m := &Manager{
		caCertPath: caCertPath,
		caKeyPath:  caKeyPath,
	}
	if err := m.loadOrGenerateCA(); err != nil {
		return nil, fmt.Errorf("certmanager: %w", err)
	}
	return m, nil
}

func (m *Manager) CACertPath() string {
	return m.caCertPath
}

func (m *Manager) loadOrGenerateCA() error {
	caCertPEM, err := os.ReadFile(m.caCertPath)
	caKeyPEM, errKey := os.ReadFile(m.caKeyPath)

	if err == nil && errKey == nil {
		return m.parseCA(caCertPEM, caKeyPEM)
	}

	pkgLog.Info("generating CA certificate...")
	if err := m.generateCA(); err != nil {
		return err
	}
	pkgLog.Info("CA certificate generated → %s", m.caCertPath)
	return nil
}

func (m *Manager) parseCA(certPEM, keyPEM []byte) error {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("failed to decode CA cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parsing CA cert: %w", err)
	}
	m.caCert = cert

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return fmt.Errorf("failed to decode CA key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		key, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		if err != nil {
			return fmt.Errorf("parsing CA key: %w", err)
		}
	}
	m.caKey = key
	return nil
}

func (m *Manager) generateCA() error {
	key, err := rsa.GenerateKey(rand.Reader, caKeySize)
	if err != nil {
		return fmt.Errorf("generating CA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generating serial: %w", err)
	}

	skid := sha1.Sum(key.N.Bytes())

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "proxy-router CA",
			Organization: []string{"proxy-router"},
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		SubjectKeyId:          skid[:],
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("creating CA cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshalling CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	if err := os.MkdirAll(filepath.Dir(m.caCertPath), 0700); err != nil {
		return fmt.Errorf("creating cert dir: %w", err)
	}
	if err := os.WriteFile(m.caCertPath, certPEM, 0644); err != nil {
		return fmt.Errorf("writing CA cert: %w", err)
	}
	if err := os.WriteFile(m.caKeyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("writing CA key: %w", err)
	}

	cert, _ := x509.ParseCertificate(certDER)
	m.caCert = cert
	m.caKey = key
	return nil
}

func (m *Manager) CertForHost(hostname string) (*tls.Certificate, error) {
	host := stripPort(hostname)
	if cached, ok := m.cache.Load(host); ok {
		return cached.(*tls.Certificate), nil
	}

	key, err := rsa.GenerateKey(rand.Reader, certKeySize)
	if err != nil {
		return nil, fmt.Errorf("generating key for %s: %w", host, err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generating serial for %s: %w", host, err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   host,
			Organization: []string{"proxy-router"},
		},
		NotBefore: time.Now().Add(-24 * time.Hour),
		NotAfter:  time.Now().Add(certValidity),
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
	}

	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, m.caCert, &key.PublicKey, m.caKey)
	if err != nil {
		return nil, fmt.Errorf("creating cert for %s: %w", host, err)
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certDER, m.caCert.Raw},
		PrivateKey:  key,
	}
	m.cache.Store(host, tlsCert)
	return tlsCert, nil
}

func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}
