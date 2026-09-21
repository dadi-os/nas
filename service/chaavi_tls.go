package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	chaaviTLSLeafCN = "chaavi.dadi"
	chaaviCACN      = "dadi mesh CA"
)

// chaaviTLSDir is $DADI_STATE_DIR/caddy/tls.
func (s stateConfig) chaaviTLSDir() string {
	return filepath.Join(s.dir, "caddy", "tls")
}

// ensureChaaviTLS creates a mesh CA and chaavi.dadi leaf if missing.
// Caddy terminates HTTPS with the leaf; clients trust ca.crt.
// An existing CA is never regenerated (clients already trust it).
func (s stateConfig) ensureChaaviTLS() error {
	dir := s.chaaviTLSDir()
	caCertPath := filepath.Join(dir, "ca.crt")
	caKeyPath := filepath.Join(dir, "ca.key")
	leafCertPath := filepath.Join(dir, "chaavi.crt")
	leafKeyPath := filepath.Join(dir, "chaavi.key")

	caReady := fileExists(caCertPath) && fileExists(caKeyPath)
	leafReady := fileExists(leafCertPath) && fileExists(leafKeyPath)
	if caReady && leafReady {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir chaavi tls: %w", err)
	}

	var caPriv *rsa.PrivateKey
	var caParsed *x509.Certificate
	if caReady {
		var err error
		caParsed, caPriv, err = loadRSACertificatePair(caCertPath, caKeyPath)
		if err != nil {
			return fmt.Errorf("load mesh CA: %w", err)
		}
	} else {
		if fileExists(caCertPath) || fileExists(caKeyPath) {
			return fmt.Errorf("incomplete mesh CA at %s (need both ca.crt and ca.key)", dir)
		}
		var err error
		caPriv, caParsed, err = createMeshCA(caCertPath, caKeyPath)
		if err != nil {
			return err
		}
	}

	if leafReady {
		return nil
	}
	if fileExists(leafCertPath) || fileExists(leafKeyPath) {
		return fmt.Errorf("incomplete chaavi leaf at %s (need both chaavi.crt and chaavi.key)", dir)
	}
	return createChaaviLeaf(caParsed, caPriv, leafCertPath, leafKeyPath)
}

// createMeshCA writes a new mesh CA keypair and returns the parsed material.
func createMeshCA(certPath, keyPath string) (*rsa.PrivateKey, *x509.Certificate, error) {
	caPriv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA key: %w", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: chaaviCACN},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caPriv.PublicKey, caPriv)
	if err != nil {
		return nil, nil, fmt.Errorf("create CA cert: %w", err)
	}
	caParsed, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA cert: %w", err)
	}
	if err := writePEM(certPath, "CERTIFICATE", caDER, 0o644); err != nil {
		return nil, nil, err
	}
	if err := writePEM(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(caPriv), 0o600); err != nil {
		return nil, nil, err
	}
	return caPriv, caParsed, nil
}

// createChaaviLeaf writes a chaavi.dadi server cert signed by ca.
func createChaaviLeaf(ca *x509.Certificate, caPriv *rsa.PrivateKey, certPath, keyPath string) error {
	leafPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate leaf key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return fmt.Errorf("leaf serial: %w", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: chaaviTLSLeafCN},
		DNSNames:     []string{chaaviTLSLeafCN},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(825 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafPriv.PublicKey, caPriv)
	if err != nil {
		return fmt.Errorf("create leaf cert: %w", err)
	}
	if err := writePEM(certPath, "CERTIFICATE", leafDER, 0o644); err != nil {
		return err
	}
	return writePEM(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(leafPriv), 0o600)
}

// loadRSACertificatePair reads a CERTIFICATE + RSA PRIVATE KEY PEM pair.
func loadRSACertificatePair(certPath, keyPath string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, nil, fmt.Errorf("invalid certificate PEM in %s", certPath)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("invalid private key PEM in %s", keyPath)
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// readChaaviCAPem returns the mesh CA certificate PEM, or empty if not yet generated.
func (s stateConfig) readChaaviCAPem() (string, error) {
	path := filepath.Join(s.chaaviTLSDir(), "ca.crt")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: typ, Bytes: der}); err != nil {
		return fmt.Errorf("pem encode %s: %w", path, err)
	}
	return nil
}
