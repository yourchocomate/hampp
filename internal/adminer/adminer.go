// Package adminer installs a pinned, checksum-verified Adminer release.
package adminer

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/yourchocomate/hampp/internal/config"
)

// Pinned release. The digest is GitHub's sha256 for the release asset and was
// cross-checked by downloading and hashing it.
const (
	Version = "6.1.0"
	URL     = "https://github.com/vrana/adminer/releases/download/v" + Version + "/adminer-" + Version + ".php"
	SHA256  = "95bf24b510b41904446f720f4f1212c9e28b1d523f3df44259480d7a12ea181e"
)

// Path is where Adminer lives; it is served at /adminer.
func Path(dir string) string { return filepath.Join(dir, "index.php") }

// Installed reports whether the pinned version is present and intact.
func Installed(dir string) bool {
	sum, err := fileSHA256(Path(dir))
	return err == nil && sum == SHA256
}

// Install downloads Adminer into dir unless the pinned version is already there.
// caBundle is Termux's $PREFIX/etc/tls/cert.pem: upstream Go only reads
// Android's /system store, while Termux's own Go is patched to read this file.
func Install(ctx context.Context, dir, caBundle string) error {
	if Installed(dir) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, URL, nil)
	if err != nil {
		return err
	}
	resp, err := client(caBundle).Do(req)
	if err != nil {
		return fmt.Errorf("download adminer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download adminer: %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("download adminer: %w", err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != SHA256 {
		return fmt.Errorf("adminer checksum mismatch: got %s, want %s", got, SHA256)
	}
	return config.WriteFileAtomic(Path(dir), b, 0o644)
}

func client(caBundle string) *http.Client {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if b, err := os.ReadFile(caBundle); err == nil {
		pool.AppendCertsFromPEM(b)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: tr}
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
