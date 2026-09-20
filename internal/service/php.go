//go:build unix

package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yourchocomate/hampp/internal/paths"
	"github.com/yourchocomate/hampp/internal/proc"
	"github.com/yourchocomate/hampp/internal/render"
)

// PHPFPM runs php-fpm on a unix socket in hampp's state dir.
type PHPFPM struct{}

func (PHPFPM) Name() string       { return "php" }
func (PHPFPM) Title() string      { return "php-fpm" }
func (PHPFPM) Packages() []string { return []string{"php", "php-fpm", "composer"} }

func (p PHPFPM) Configure(c *Ctx) error {
	for _, d := range []string{"php-sessions", "opcache"} {
		if err := os.MkdirAll(filepath.Join(c.Paths.Tmp(), d), 0o700); err != nil {
			return err
		}
	}
	if _, err := render.WriteIfChanged("php-fpm.conf.tmpl", conf(c, "php-fpm.conf"), c.Data, 0o600); err != nil {
		return err
	}
	if _, err := render.WriteIfChanged("php.ini.tmpl", conf(c, "php.ini"), c.Data, 0o600); err != nil {
		return err
	}
	return ConfigurePHPIni(c)
}

// PHP ini layout, shared by php-fpm and the PHP CLI through PHP_INI_SCAN_DIR.
// PHP loads, in order: Termux's conf.d (package ini files such as gd.ini),
// hampp's generated dir, then the user's own dir, so the user always wins.
func HamppIniDir(p paths.Paths) string { return filepath.Join(p.Conf(), "php.d") }
func UserIniDir(p paths.Paths) string  { return filepath.Join(p.Config, "php.d") }
func UserIniFile(p paths.Paths) string { return filepath.Join(UserIniDir(p), "custom.ini") }

// ScanDir is the PHP_INI_SCAN_DIR value. The leading ":" keeps PHP's
// compiled-in scan dir ($PREFIX/etc/php/conf.d) first.
func ScanDir(p paths.Paths) string { return ":" + HamppIniDir(p) + ":" + UserIniDir(p) }

// ConfigurePHPIni renders hampp's shared ini files and creates the user file.
func ConfigurePHPIni(c *Ctx) error {
	dir := HamppIniDir(c.Paths)
	for name, file := range map[string]string{
		"php-hampp.ini.tmpl":      "10-hampp.ini",
		"php-extensions.ini.tmpl": "20-extensions.ini",
		"php-settings.ini.tmpl":   "50-settings.ini",
	} {
		if _, err := render.WriteIfChanged(name, filepath.Join(dir, file), c.Data, 0o644); err != nil {
			return err
		}
	}
	if _, err := os.Stat(UserIniFile(c.Paths)); os.IsNotExist(err) {
		b, err := render.Render("php-user.ini.tmpl", c.Data)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(UserIniDir(c.Paths), 0o700); err != nil {
			return err
		}
		return os.WriteFile(UserIniFile(c.Paths), b, 0o644)
	}
	return nil
}

func (p PHPFPM) args(c *Ctx, extra ...string) []string {
	return append([]string{"PHP_INI_SCAN_DIR=" + ScanDir(c.Paths), c.Env.Bin("php-fpm"),
		"-y", conf(c, "php-fpm.conf"), "-c", conf(c, "php.ini")}, extra...)
}

// php-fpm is started through env(1) so PHP_INI_SCAN_DIR reaches it; USR2
// reloads re-exec the master with the same environment.
func (p PHPFPM) Test(ctx context.Context, c *Ctx) error {
	_, err := c.R.Output(ctx, "", "env", p.args(c, "-t")...)
	return err
}

func (p PHPFPM) Start(ctx context.Context, c *Ctx) error {
	pidFile := run(c, "php-fpm.pid")
	if proc.Running(pidFile) > 0 {
		return nil
	}
	_ = os.Remove(c.Data.PHPSock) // stale socket from a crash
	if _, err := c.R.Output(ctx, "", "env", p.args(c, "-D")...); err != nil {
		return failure(err, logf(c, "php-fpm.log"))
	}
	wctx, cancel := waitCtx(ctx, 10*time.Second)
	defer cancel()
	if err := proc.WaitDial(wctx, "unix", c.Data.PHPSock); err != nil {
		return failure(err, logf(c, "php-fpm.log"))
	}
	return nil
}

func (p PHPFPM) Stop(_ context.Context, c *Ctx) error {
	// SIGQUIT = graceful shutdown for php-fpm.
	err := stopPID(run(c, "php-fpm.pid"), syscall.SIGQUIT, 10*time.Second)
	_ = os.Remove(c.Data.PHPSock)
	return err
}

func (p PHPFPM) Reload(ctx context.Context, c *Ctx) error {
	pid := proc.Running(run(c, "php-fpm.pid"))
	if pid == 0 {
		return nil
	}
	if err := p.Test(ctx, c); err != nil {
		return err
	}
	return syscall.Kill(pid, syscall.SIGUSR2)
}

func (p PHPFPM) Status(c *Ctx) Status {
	return pidStatus(p, run(c, "php-fpm.pid"), "socket")
}

func (PHPFPM) Logs(c *Ctx) []string {
	return []string{logf(c, "php-fpm.log"), logf(c, "php-error.log")}
}

// PHPVersion returns e.g. "8.5.1" from `php -v`.
func PHPVersion(ctx context.Context, c *Ctx) string {
	out, err := c.R.Output(ctx, "", "env", "PHP_INI_SCAN_DIR="+ScanDir(c.Paths), c.Env.Bin("php"), "-r", "echo PHP_VERSION;")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
