//go:build unix

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yourchocomate/hampp/internal/pki"
	"github.com/yourchocomate/hampp/internal/service"
	"github.com/yourchocomate/hampp/internal/termux"
)

// SSLInit enables HTTPS, creates the local CA and issues site certificates.
func (a *App) SSLInit(ctx context.Context) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	if missing := a.PM.Missing(ctx, "openssl-tool"); len(missing) > 0 {
		if err := a.PM.Install(ctx, a.Out, missing...); err != nil {
			a.printf("! openssl-tool not installed (%v); Termux tools will use SSL_CERT_FILE only.\n", err)
		}
	}
	a.Cfg.Web.HTTPS = true
	if err := a.SaveConfig(); err != nil {
		return err
	}
	if _, err := a.Prepare(ctx, nil); err != nil {
		return err
	}
	ca, err := pki.LoadCA(a.Paths.CA())
	if err != nil {
		return err
	}
	a.printf("Local CA: %s\nSHA-256:  %s\n", ca.Cert.Subject.CommonName, ca.Fingerprint())
	a.printf("The CA key never leaves %s (mode 0600). Anyone holding it could impersonate websites to this phone, so never share it.\n", a.Paths.Short(ca.KeyPath))
	return a.restartWebIfRunning(ctx)
}

// TrustResult describes what `ssl trust` did and what the user still has to do.
type TrustResult struct {
	CopiedTo      string
	CopyErr       error
	TermuxTrusted bool
	TermuxErr     error
	AndroidSteps  []string
	FirefoxSteps  []string
	Fingerprint   string
}

// SSLTrust prepares everything that can be automated. Since Android 11 no app
// can install a CA certificate; the user must do it in Settings.
func (a *App) SSLTrust(ctx context.Context) (TrustResult, error) {
	var res TrustResult
	if err := a.RequireInit(); err != nil {
		return res, err
	}
	if !pki.Exists(a.Paths.CA()) {
		if err := a.SSLInit(ctx); err != nil {
			return res, err
		}
	}
	ca, err := pki.LoadCA(a.Paths.CA())
	if err != nil {
		return res, err
	}
	res.Fingerprint = ca.Fingerprint()

	// 1. Copy to Downloads so the Settings file picker can see it.
	downloads := filepath.Join(a.Env.Home, "storage", "downloads")
	if _, err := os.Stat(downloads); err != nil {
		res.CopyErr = errors.New("shared storage is not set up: run `termux-setup-storage`, allow access, then run `hampp ssl trust` again")
	} else {
		dst := filepath.Join(downloads, "hampp-ca.crt")
		b, _ := os.ReadFile(ca.CertPath)
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			res.CopyErr = err
		} else {
			res.CopiedTo = "Downloads/hampp-ca.crt"
		}
	}
	file := res.CopiedTo
	if file == "" {
		file = "hampp-ca.crt"
	}
	res.AndroidSteps = termux.CAInstallSteps(a.Env.SDK, file)
	res.FirefoxSteps = termux.FirefoxCASteps()

	// 2. Termux tools: a combined bundle via SSL_CERT_FILE and php.ini, plus
	// add-trusted-certificate (openssl-tool) for programs using the certs dir.
	if err := pki.WriteBundle(a.SystemBundle(), ca.CertPath, a.bundlePath()); err != nil {
		res.TermuxErr = err
	} else {
		res.TermuxTrusted = true
	}
	if _, err := a.R.LookPath("add-trusted-certificate"); err == nil {
		if _, err := a.R.Output(ctx, "", "add-trusted-certificate", ca.CertPath); err != nil {
			a.printf("! add-trusted-certificate: %v\n", err)
		}
	}
	if a.Cfg.RCHook {
		if err := a.InstallRCHook(); err != nil {
			a.printf("! %v\n", err)
		}
	}
	// php.ini picks up the bundle on the next render.
	_ = a.reloadPHP(ctx)
	return res, nil
}

// Check is one line of `ssl status` or `doctor`.
type Check struct {
	Level   Level
	Message string
	Fix     string
}

type Level int

const (
	OK Level = iota
	Warn
	Fail
)

func (l Level) Symbol(ascii bool) string {
	if ascii {
		return [...]string{"[ok]", "[!!]", "[xx]"}[l]
	}
	return [...]string{"✓", "!", "✗"}[l]
}

// SSLStatus verifies the CA, every site certificate and a real HTTPS request.
func (a *App) SSLStatus(ctx context.Context) []Check {
	var out []Check
	if !a.Cfg.Web.HTTPS {
		return []Check{{Warn, "HTTPS is off", "hampp ssl init"}}
	}
	ca, err := pki.LoadCA(a.Paths.CA())
	if err != nil {
		return []Check{{Fail, "No local CA", "hampp ssl init"}}
	}
	out = append(out, Check{OK, fmt.Sprintf("CA %s, valid until %s", ca.Cert.Subject.CommonName, ca.Cert.NotAfter.Format("2006-01-02")), ""})
	c, _ := a.Ctx()
	for _, s := range c.Data.Sites {
		if err := ca.Verify(s.CertFile, s.Host, a.Now()); err != nil {
			out = append(out, Check{Fail, fmt.Sprintf("%s: %v", s.Host, err), "hampp reload"})
			continue
		}
		exp, _ := pki.Expiry(s.CertFile)
		out = append(out, Check{OK, fmt.Sprintf("%s certificate valid until %s", s.Host, exp.Format("2006-01-02")), ""})
	}
	if pki.BundleStale(a.SystemBundle(), a.bundlePath()) {
		out = append(out, Check{Warn, "Termux trust bundle missing or older than ca-certificates", "hampp ssl trust"})
	} else {
		out = append(out, Check{OK, "Termux tools trust the CA (SSL_CERT_FILE, php.ini)", ""})
	}
	if a.Web().Status(c).Running {
		url := "https://localhost:" + strconv.Itoa(a.Cfg.Web.HTTPSPort) + "/"
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		code, err := a.R.Output(cctx, "", "curl", "-sS", "-o", "/dev/null", "-w", "%{http_code}", "--cacert", a.bundlePath(), url)
		if err != nil {
			out = append(out, Check{Fail, "HTTPS request to " + url + " failed: " + firstLineOf(err.Error()), "hampp logs web"})
		} else {
			out = append(out, Check{OK, "curl " + url + " → HTTP " + strings.TrimSpace(code), ""})
		}
	} else {
		out = append(out, Check{Warn, "Web server is not running; HTTPS not tested", "hampp start"})
	}
	out = append(out, Check{Warn, "hampp cannot read Android's trust store: check for a padlock at https://localhost:" + strconv.Itoa(a.Cfg.Web.HTTPSPort) + "/ in your browser", "hampp ssl trust"})
	return out
}

// SSLUninstall disables HTTPS and deletes the CA and certificates.
func (a *App) SSLUninstall(ctx context.Context) ([]string, error) {
	if err := a.RequireInit(); err != nil {
		return nil, err
	}
	a.Cfg.Web.HTTPS = false
	if err := a.SaveConfig(); err != nil {
		return nil, err
	}
	for _, d := range []string{a.Paths.CA(), a.Paths.Certs()} {
		if err := os.RemoveAll(d); err != nil {
			return nil, err
		}
	}
	_ = os.Remove(filepath.Join(a.Env.Home, "storage", "downloads", "hampp-ca.crt"))
	if err := a.restartWebIfRunning(ctx); err != nil {
		return nil, err
	}
	return []string{
		"Remove the CA from Android too:",
		"Settings > search \"Trusted credentials\" (or \"User credentials\") > User tab >",
		"  hampp local CA > Remove",
		"If you ran add-trusted-certificate, delete the hampp file from " + filepath.Join(a.Env.Prefix, "etc", "tls", "certs"),
	}, nil
}

func (a *App) restartWebIfRunning(ctx context.Context) error {
	c, _ := a.Ctx()
	if !a.Web().Status(c).Running {
		return nil
	}
	return a.Restart(ctx, []string{"web"})
}

func (a *App) reloadPHP(ctx context.Context) error {
	c, _ := a.Ctx()
	if (service.PHPFPM{}).Status(c).Running {
		return a.Restart(ctx, []string{"php"})
	}
	return nil
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
