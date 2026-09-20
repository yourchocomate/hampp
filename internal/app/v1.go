//go:build unix

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/yourchocomate/hampp/internal/rcfile"
	"github.com/yourchocomate/hampp/internal/termux"
)

func (a *App) v1Dir() string     { return filepath.Join(a.Env.Prefix, "share", "HamppServer") }
func (a *App) v1Wrapper() string { return filepath.Join(a.Env.Prefix, "bin", "hampp") }
func (a *App) httpdConf() string { return filepath.Join(a.Env.Prefix, "etc", "apache2", "httpd.conf") }

// DetectV1 finds what HamppServer v1 left behind.
func (a *App) DetectV1() []string {
	var f []string
	if st, err := os.Stat(a.v1Dir()); err == nil && st.IsDir() {
		f = append(f, a.v1Dir()+" exists")
	}
	if b, err := os.ReadFile(a.v1Wrapper()); err == nil && termux.IsV1Wrapper(string(b)) {
		f = append(f, a.v1Wrapper()+" is the old Python launcher")
	}
	if b, err := os.ReadFile(a.httpdConf()); err == nil && strings.Contains(string(b), "/sdcard/www") {
		f = append(f, "the system httpd.conf was overwritten by v1 (hampp v2 does not use it)")
	}
	return f
}

// CleanV1 removes v1 files. The system httpd.conf is only reported, because
// restoring it is a package operation the user should run knowingly.
func (a *App) CleanV1() []string {
	var done []string
	if _, err := os.Stat(a.v1Dir()); err == nil {
		if err := os.RemoveAll(a.v1Dir()); err == nil {
			done = append(done, "removed "+a.v1Dir())
		}
	}
	if b, err := os.ReadFile(a.v1Wrapper()); err == nil && termux.IsV1Wrapper(string(b)) {
		if err := os.Remove(a.v1Wrapper()); err == nil {
			done = append(done, "removed the v1 launcher "+a.v1Wrapper()+" (reinstall hampp v2 if it was there)")
		}
	}
	if b, err := os.ReadFile(a.httpdConf()); err == nil && strings.Contains(string(b), "/sdcard/www") {
		done = append(done, "to restore Termux's stock httpd.conf run:\n  rm "+a.httpdConf()+" && apt install --reinstall -o Dpkg::Options::=--force-confmiss apache2")
	}
	return done
}

// Uninstall stops everything and removes hampp's files. Web content, installed
// packages and databases are never touched.
func (a *App) Uninstall(ctx context.Context, purge bool) []string {
	var done []string
	_ = a.Stop(ctx, nil)
	for _, f := range []string{filepath.Join(a.Env.Home, ".bashrc"), filepath.Join(a.Env.Home, ".zshrc")} {
		if removed, err := rcfile.Uninstall(f); err == nil && removed {
			done = append(done, "cleaned "+a.Paths.Short(f))
		}
	}
	if b, err := os.ReadFile(filepath.Join(a.Env.Home, ".my.cnf")); err == nil && strings.Contains(string(b), myCnfMarker) {
		_ = os.Remove(filepath.Join(a.Env.Home, ".my.cnf"))
		done = append(done, "removed ~/.my.cnf")
	}
	_ = os.RemoveAll(a.Paths.State)
	done = append(done, "removed "+a.Paths.Short(a.Paths.State))
	if purge {
		_ = os.RemoveAll(a.Paths.Data)
		_ = os.RemoveAll(a.Paths.Config)
		done = append(done, "removed "+a.Paths.Short(a.Paths.Data)+" (CA, certificates, Adminer)", "removed "+a.Paths.Short(a.Paths.Config)+" (config and passwords)")
	}
	done = append(done,
		"kept "+a.Paths.Short(a.Cfg.Web.Root)+" and your databases",
		"remove the binary with: apt remove hampp   (or rm $PREFIX/bin/hampp)",
	)
	return done
}
