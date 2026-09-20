//go:build unix

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yourchocomate/hampp/internal/adminer"
	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/render"
	"github.com/yourchocomate/hampp/internal/site"
)

// InitOptions are the answers from the wizard or `hampp init` flags.
type InitOptions struct {
	Web    string
	DB     string
	Root   string
	Port   int
	HTTPS  bool
	Node   int // TUR major to install, 0 = none
	RCHook bool
	Start  bool
}

// DefaultInitOptions mirrors config defaults.
func (a *App) DefaultInitOptions() InitOptions {
	c := a.Cfg
	return InitOptions{Web: c.Web.Server, DB: c.DB.Engine, Root: c.Web.Root, Port: c.Web.Port,
		HTTPS: c.Web.HTTPS, RCHook: c.RCHook, Start: true}
}

// Packages returns what init will install for opts.
func (a *App) Packages(o InitOptions) []string {
	pkgs := []string{"php", "php-fpm", "composer"}
	if o.Web == config.WebNginx {
		pkgs = append(pkgs, "nginx")
	} else {
		pkgs = append(pkgs, "apache2")
	}
	if o.DB == config.DBMariaDB {
		pkgs = append(pkgs, "mariadb")
	}
	if o.HTTPS {
		pkgs = append(pkgs, "openssl-tool") // add-trusted-certificate
	}
	return pkgs
}

// Init sets hampp up. It is safe to run again: it only fills in what is missing
// and never deletes user files.
func (a *App) Init(ctx context.Context, o InitOptions) error {
	if err := a.RequireTermux(); err != nil {
		return err
	}
	if findings := a.DetectV1(); len(findings) > 0 {
		a.printf("! Found HamppServer v1 leftovers; run `hampp doctor --fix` to clean them up.\n")
	}

	a.Cfg.Web.Server, a.Cfg.DB.Engine, a.Cfg.Web.HTTPS, a.Cfg.RCHook = o.Web, o.DB, o.HTTPS, o.RCHook
	if o.Root != "" {
		a.Cfg.Web.Root = o.Root
	}
	if o.Port != 0 {
		a.Cfg.Web.Port = o.Port
	}
	if err := a.Cfg.Validate(); err != nil {
		return err
	}
	if site.OnSharedStorage(a.Cfg.Web.Root) {
		a.printf("! %s is on shared storage: npm, composer and Laravel symlinks will fail there.\n  Consider ~/www and `hampp mirror on` instead.\n", a.Cfg.Web.Root)
	}
	if err := a.Paths.Ensure(); err != nil {
		return err
	}
	if err := a.SaveConfig(); err != nil {
		return err
	}
	a.Initialized = true

	a.printf("==> Installing packages\n")
	if missing := a.PM.Missing(ctx, a.Packages(o)...); len(missing) > 0 {
		a.printf("    %s\n", strings.Join(a.PM.InstallArgs(missing...), " "))
		if err := a.PM.Install(ctx, a.Out, missing...); err != nil {
			return fmt.Errorf("package install failed: %w", err)
		}
	} else {
		a.printf("    all present\n")
	}

	a.printf("==> Web root %s\n", a.Paths.Short(a.Cfg.Web.Root))
	if err := os.MkdirAll(a.Cfg.Web.Root, 0o755); err != nil {
		return err
	}
	if err := a.writeWelcome(); err != nil {
		return err
	}

	if o.DB != config.DBNone && a.Cfg.Adminer {
		a.printf("==> Adminer %s\n", adminer.Version)
		if err := adminer.Install(ctx, a.Paths.Adminer(), a.SystemBundle()); err != nil {
			a.printf("! Adminer download failed (%v); retry later with `hampp init`.\n", err)
		}
		if o.DB == config.DBSQLite {
			if err := a.adminerSQLiteLogin(ctx); err != nil {
				a.printf("! Adminer SQLite login not configured: %v\n", err)
			}
		}
	}

	if a.Creds.CodePassword == "" {
		a.Creds.CodePassword = config.NewPassword()
	}
	if err := a.SaveCreds(); err != nil {
		return err
	}

	if o.RCHook {
		if err := a.InstallRCHook(); err != nil {
			a.printf("! Could not update shell startup files: %v\n", err)
		}
	}

	if o.Node > 0 {
		a.printf("==> Node.js %d\n", o.Node)
		if err := a.NodeInstall(ctx, o.Node); err != nil {
			a.printf("! Node.js install failed: %v\n", err)
		}
	}

	if !o.Start {
		a.printf("Setup complete. Start with: hampp start\n")
		return nil
	}
	a.printf("==> Starting services\n")
	if err := a.Start(ctx, nil); err != nil {
		return err
	}
	if o.DB == config.DBMariaDB && a.Creds.DBRootPassword == "" {
		a.printf("==> Securing MariaDB\n")
		if err := a.DBSetup(ctx); err != nil {
			a.printf("! %v\n", err)
		}
	}
	if v := a.PM.Version(ctx, "composer"); v != "" {
		a.printf("Composer %s is ready (global tools like `composer global require laravel/installer` land on PATH in new sessions).\n", v)
	}
	a.printf("\nReady: %s\n", site.Site{Host: "localhost"}.URL(a.Cfg.Web.Port, false))
	if o.HTTPS {
		a.printf("HTTPS: run `hampp ssl trust` once to trust the local certificate in Android.\n")
	}
	a.printf("Edit your files: `hampp edit` shows how to open ~/www in a mobile editor.\n")
	return nil
}

// writeWelcome writes index.php only when the web root has no index file.
func (a *App) writeWelcome() error {
	for _, n := range []string{"index.php", "index.html", "index.htm"} {
		if _, err := os.Stat(filepath.Join(a.Cfg.Web.Root, n)); err == nil {
			return nil
		}
	}
	c, _ := a.Ctx()
	b, err := render.Render("index.php.tmpl", c.Data)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.Cfg.Web.Root, "index.php"), b, 0o644)
}

// adminerSQLiteLogin writes adminer-plugins.php so Adminer 6 accepts a login
// for SQLite (it refuses passwordless logins). PHP computes the hash so the
// format is exactly what password_verify expects; the password goes via stdin.
func (a *App) adminerSQLiteLogin(ctx context.Context) error {
	if a.Creds.AdminerSQLitePwd == "" {
		a.Creds.AdminerSQLitePwd = config.NewPassword()
	}
	hash, err := a.R.Output(ctx, a.Creds.AdminerSQLitePwd, a.Env.Bin("php"), "-r", "echo password_hash(trim(stream_get_contents(STDIN)), PASSWORD_DEFAULT);")
	if err != nil {
		return err
	}
	hash = strings.TrimSpace(hash)
	if !strings.HasPrefix(hash, "$") || strings.ContainsAny(hash, "'\\") {
		return errors.New("unexpected password_hash output")
	}
	b, err := render.Render("adminer-plugins.php.tmpl", render.Data{PasswordHash: hash})
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(filepath.Join(a.Paths.Adminer(), "adminer-plugins.php"), b, 0o600)
}
