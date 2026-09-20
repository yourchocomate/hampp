// Package pki manages hampp's local certificate authority and per-site
// certificates. mkcert's `-install` cannot touch Android's trust store, so hampp
// generates the same kind of CA itself and guides the user through installing it.
package pki

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/yourchocomate/hampp/internal/config"
)

const (
	CAValidity   = 10 * 365 * 24 * time.Hour
	LeafValidity = 825 * 24 * time.Hour // mkcert's limit; local CAs are exempt from the 398-day rule
	RenewBefore  = 30 * 24 * time.Hour
)

type CA struct {
	Cert     *x509.Certificate
	Key      *ecdsa.PrivateKey
	CertPath string
	KeyPath  string
}

func CACertPath(dir string) string { return filepath.Join(dir, "ca.crt") }
func CAKeyPath(dir string) string  { return filepath.Join(dir, "ca.key") }

// Exists reports whether a CA has been created in dir.
func Exists(dir string) bool {
	_, err := os.Stat(CACertPath(dir))
	return err == nil
}

// LoadOrCreateCA loads the CA from dir, creating it on first use.
func LoadOrCreateCA(dir, commonName string, now time.Time) (*CA, bool, error) {
	if Exists(dir) {
		ca, err := LoadCA(dir)
		return ca, false, err
	}
	ca, err := createCA(dir, commonName, now)
	return ca, true, err
}

func LoadCA(dir string) (*CA, error) {
	cert, err := readCert(CACertPath(dir))
	if err != nil {
		return nil, err
	}
	kb, err := os.ReadFile(CAKeyPath(dir))
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(kb)
	if blk == nil {
		return nil, errors.New("ca.key: no PEM data")
	}
	key, err := x509.ParseECPrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca.key: %w", err)
	}
	return &CA{Cert: cert, Key: key, CertPath: CACertPath(dir), KeyPath: CAKeyPath(dir)}, nil
}

func createCA(dir, commonName string, now time.Time) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"hampp local development CA"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(CAValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := config.WriteFileAtomic(CAKeyPath(dir), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		return nil, err
	}
	if err := config.WriteFileAtomic(CACertPath(dir), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key, CertPath: CACertPath(dir), KeyPath: CAKeyPath(dir)}, nil
}

// Issue writes a leaf certificate and key for hosts. Hosts are explicit names
// and IPs; no wildcard is used because browsers reject *.localhost.
func (ca *CA) Issue(hosts []string, certPath, keyPath string, now time.Time) error {
	if len(hosts) == 0 {
		return errors.New("no hosts")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: hosts[0], Organization: []string{"hampp local development certificate"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(LeafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := config.WriteFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		return err
	}
	return config.WriteFileAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

// NeedsIssue reports whether the certificate at certPath is missing, expiring
// within RenewBefore, not signed by ca, or does not cover exactly hosts.
func (ca *CA) NeedsIssue(certPath string, hosts []string, now time.Time) (bool, string) {
	cert, err := readCert(certPath)
	if err != nil {
		return true, "missing"
	}
	if now.Add(RenewBefore).After(cert.NotAfter) {
		return true, "expiring " + cert.NotAfter.Format("2006-01-02")
	}
	if err := cert.CheckSignatureFrom(ca.Cert); err != nil {
		return true, "signed by a different CA"
	}
	var have []string
	have = append(have, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		have = append(have, ip.String())
	}
	want := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			want = append(want, ip.String())
		} else {
			want = append(want, h)
		}
	}
	slices.Sort(have)
	slices.Sort(want)
	if !slices.Equal(have, want) {
		return true, "host list changed"
	}
	return false, ""
}

// Verify checks that certPath chains to the CA for host.
func (ca *CA) Verify(certPath, host string, now time.Time) error {
	cert, err := readCert(certPath)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	_, err = cert.Verify(x509.VerifyOptions{DNSName: host, Roots: pool, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err
}

// Fingerprint returns the SHA-256 fingerprint shown by Android's credential viewer.
func (ca *CA) Fingerprint() string {
	sum := sha256.Sum256(ca.Cert.Raw)
	return hex.EncodeToString(sum[:])
}

// WriteBundle writes systemBundle followed by the CA certificate. Termux tools
// read it via SSL_CERT_FILE; the stock cert.pem is replaced on ca-certificates
// upgrades, so hampp never edits it in place.
func WriteBundle(systemBundle, caCert, out string) error {
	sys, err := os.ReadFile(systemBundle)
	if err != nil {
		return fmt.Errorf("system CA bundle %s: %w (try: pkg install ca-certificates)", systemBundle, err)
	}
	ca, err := os.ReadFile(caCert)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	b.Write(bytes.TrimRight(sys, "\n"))
	b.WriteString("\n\n# hampp local development CA\n")
	b.Write(ca)
	return config.WriteFileAtomic(out, b.Bytes(), 0o644)
}

// BundleStale reports whether the bundle is missing or older than the system bundle.
func BundleStale(systemBundle, bundle string) bool {
	b, err := os.Stat(bundle)
	if err != nil {
		return true
	}
	s, err := os.Stat(systemBundle)
	return err == nil && s.ModTime().After(b.ModTime())
}

// Expiry returns the NotAfter of a certificate file.
func Expiry(certPath string) (time.Time, error) {
	c, err := readCert(certPath)
	if err != nil {
		return time.Time{}, err
	}
	return c.NotAfter, nil
}

func readCert(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%s: no certificate", path)
	}
	return x509.ParseCertificate(blk.Bytes)
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	return n
}
