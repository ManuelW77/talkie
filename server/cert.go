package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoadOrCreateCert lädt das Server-Zertifikat aus dataDir oder erzeugt ein neues,
// selbstsigniertes (gültig 20 Jahre). Clients pinnen den Fingerabdruck.
func LoadOrCreateCert(dataDir string) (tls.Certificate, error) {
	certPath := filepath.Join(dataDir, "cert.pem")
	keyPath := filepath.Join(dataDir, "key.pem")
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		return tls.LoadX509KeyPair(certPath, keyPath)
	case errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist):
		// erster Start: neu erzeugen
	default:
		return tls.Certificate{}, fmt.Errorf("cert.pem oder key.pem fehlt in %s — nicht automatisch ersetzen, sonst vertrauen die Clients dem Server nicht mehr (beide löschen für ein neues Zertifikat)", dataDir)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "talkie"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"talkie"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(certPath, keyPath)
}

// CertHash ist der SHA-256 über das DER-Zertifikat — dasselbe, was der Client pinnt.
func CertHash(c tls.Certificate) []byte {
	h := sha256.Sum256(c.Certificate[0])
	return h[:]
}

// FormatFingerprint: Hex in Vierergruppen, gut abzulesen.
func FormatFingerprint(h []byte) string {
	s := strings.ToUpper(hex.EncodeToString(h))
	var parts []string
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, s[i:i+4])
	}
	return strings.Join(parts, ":")
}
