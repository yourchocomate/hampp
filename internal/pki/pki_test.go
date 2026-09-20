package pki

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func TestCALifecycle(t *testing.T) {
	dir := t.TempDir()
	ca, created, err := LoadOrCreateCA(dir, "hampp local CA (Pixel 7)", now)
	if err != nil || !created {
		t.Fatal(err, created)
	}
	if !ca.Cert.IsCA || !ca.Cert.MaxPathLenZero || ca.Cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatalf("bad CA constraints: %+v", ca.Cert)
	}
	if st, _ := os.Stat(CAKeyPath(dir)); st.Mode().Perm() != 0o600 {
		t.Fatalf("CA key must be 0600, is %v", st.Mode().Perm())
	}
	again, created, err := LoadOrCreateCA(dir, "ignored", now)
	if err != nil || created || again.Fingerprint() != ca.Fingerprint() {
		t.Fatal("CA must be loaded, not recreated")
	}
	if len(ca.Fingerprint()) != 64 {
		t.Fatal("fingerprint")
	}
}

func TestIssueVerifyAndRenewal(t *testing.T) {
	dir := t.TempDir()
	ca, _, err := LoadOrCreateCA(dir, "test CA", now)
	if err != nil {
		t.Fatal(err)
	}
	cert, key := filepath.Join(dir, "blog.crt"), filepath.Join(dir, "blog.key")
	hosts := []string{"blog.localhost", "localhost", "127.0.0.1", "::1"}

	if need, why := ca.NeedsIssue(cert, hosts, now); !need || why != "missing" {
		t.Fatal(need, why)
	}
	if err := ca.Issue(hosts, cert, key, now); err != nil {
		t.Fatal(err)
	}
	if need, why := ca.NeedsIssue(cert, hosts, now); need {
		t.Fatalf("fresh cert should not need issuing: %s", why)
	}
	for _, h := range []string{"blog.localhost", "localhost", "127.0.0.1"} {
		if err := ca.Verify(cert, h, now); err != nil {
			t.Errorf("verify %s: %v", h, err)
		}
	}
	if err := ca.Verify(cert, "other.localhost", now); err == nil {
		t.Error("must not verify an unrelated host")
	}

	parsed, _ := readCert(cert)
	if len(parsed.DNSNames) != 2 || len(parsed.IPAddresses) != 2 {
		t.Errorf("SANs: %v %v", parsed.DNSNames, parsed.IPAddresses)
	}
	for _, n := range parsed.DNSNames {
		if strings.Contains(n, "*") {
			t.Error("no wildcard SANs: browsers reject *.localhost")
		}
	}
	if d := parsed.NotAfter.Sub(now); d > LeafValidity || d < LeafValidity-2*time.Hour {
		t.Errorf("leaf validity %v", d)
	}
	if st, _ := os.Stat(key); st.Mode().Perm() != 0o600 {
		t.Error("leaf key must be 0600")
	}

	if need, why := ca.NeedsIssue(cert, append(hosts, "192.168.1.5"), now); !need || why != "host list changed" {
		t.Error("host change", need, why)
	}
	if need, why := ca.NeedsIssue(cert, hosts, now.Add(LeafValidity-10*24*time.Hour)); !need || !strings.HasPrefix(why, "expiring") {
		t.Error("expiry", need, why)
	}

	other, _, _ := LoadOrCreateCA(t.TempDir(), "other", now)
	if need, why := other.NeedsIssue(cert, hosts, now); !need || why != "signed by a different CA" {
		t.Error("different CA", need, why)
	}
}

func TestBundle(t *testing.T) {
	dir := t.TempDir()
	ca, _, _ := LoadOrCreateCA(dir, "test CA", now)
	sys := filepath.Join(dir, "cert.pem")
	os.WriteFile(sys, []byte("-----BEGIN CERTIFICATE-----\nSYSTEM\n-----END CERTIFICATE-----\n"), 0o644)
	out := filepath.Join(dir, "bundle.pem")
	if !BundleStale(sys, out) {
		t.Fatal("missing bundle is stale")
	}
	if err := WriteBundle(sys, ca.CertPath, out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if !strings.HasPrefix(string(b), "-----BEGIN CERTIFICATE-----\nSYSTEM") || strings.Count(string(b), "BEGIN CERTIFICATE") != 2 {
		t.Fatalf("bundle content:\n%s", b)
	}
	if BundleStale(sys, out) {
		t.Fatal("fresh bundle is not stale")
	}
	future := time.Now().Add(time.Hour)
	os.Chtimes(sys, future, future)
	if !BundleStale(sys, out) {
		t.Fatal("bundle older than system bundle is stale")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		t.Fatal("bundle must contain parseable certificates")
	}
}
