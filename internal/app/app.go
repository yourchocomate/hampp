//go:build unix

// Package app is hampp's core: it wires config, sites, certificates and
// services together. The CLI and the TUI are thin layers over it.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yourchocomate/hampp/internal/adminer"
	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/netinfo"
	"github.com/yourchocomate/hampp/internal/paths"
	"github.com/yourchocomate/hampp/internal/pkgmgr"
	"github.com/yourchocomate/hampp/internal/pki"
	"github.com/yourchocomate/hampp/internal/render"
	"github.com/yourchocomate/hampp/internal/service"
	"github.com/yourchocomate/hampp/internal/site"
	"github.com/yourchocomate/hampp/internal/sys"
	"github.com/yourchocomate/hampp/internal/termux"
)

type App struct {
	Env         termux.Env
	Paths       paths.Paths
	Cfg         config.Config
	Creds       config.Credentials
	PM          pkgmgr.Manager
	R           sys.Runner
	Out         io.Writer
	Now         func() time.Time
	Initialized bool
	Self        string // path of the running hampp binary
}

// Load detects the environment and reads config. A missing config is not an
// error; Initialized reports whether `hampp init` has run.
func Load(ctx context.Context, out io.Writer) (*App, error) {
	r := sys.Exec{}
	env := termux.Detect(ctx, r)
	p := paths.New(env.Home)
	a := &App{Env: env, Paths: p, R: r, Out: out, Now: time.Now, PM: pkgmgr.Detect(env.Prefix, r)}
	a.Self, _ = os.Executable()

	cfg, err := config.Load(p.ConfigFile(), env.Home)
	switch {
	case errors.Is(err, config.ErrNotInitialized):
		a.Cfg = config.Default(env.Home)
	case err != nil:
		return nil, err
	default:
		a.Cfg, a.Initialized = cfg, true
	}
	a.Creds, err = config.LoadCredentials(p.CredentialsFile())
	if err != nil {
		return nil, err
	}
	return a, nil
}

// RequireTermux fails with a friendly message outside Termux.
func (a *App) RequireTermux() error {
	if !a.Env.IsTermux {
		return errors.New("hampp manages servers inside Termux on Android; this does not look like Termux\n(for development on another OS, set HAMPP_PREFIX and HAMPP_FAKE_TERMUX=1)")
	}
	if os.Geteuid() == 0 {
		// pkg refuses root, and root-owned files would break the normal Termux user.
		return errors.New("do not run hampp as root (su/tsu): run it as the normal Termux user")
	}
	return nil
}

// RequireInit fails if `hampp init` has not run.
func (a *App) RequireInit() error {
	if err := a.RequireTermux(); err != nil {
		return err
	}
	if !a.Initialized {
		return config.ErrNotInitialized
	}
	return nil
}

func (a *App) SaveConfig() error { return config.Save(a.Paths.ConfigFile(), a.Cfg) }
func (a *App) SaveCreds() error {
	return config.SaveCredentials(a.Paths.CredentialsFile(), a.Creds)
}

func (a *App) printf(format string, args ...any) {
	if a.Out != nil {
		fmt.Fprintf(a.Out, format, args...)
	}
}

// Web returns the configured web server.
func (a *App) Web() service.Service {
	if a.Cfg.Web.Server == config.WebNginx {
		return service.Nginx{}
	}
	return service.Apache{}
}

// Core returns the stack in start order: db, php, web.
func (a *App) Core() []service.Service {
	var s []service.Service
	if a.Cfg.DB.Engine == config.DBMariaDB {
		s = append(s, service.MariaDB{})
	}
	return append(s, service.PHPFPM{}, a.Web())
}

// Extras are optional services started by their own commands.
func (a *App) Extras() []service.Service {
	return []service.Service{service.CodeServer{}, service.Mirror{Self: a.Self}}
}

// All returns core and extra services.
func (a *App) All() []service.Service { return append(a.Core(), a.Extras()...) }

// Find returns services by name ("web", "php", "db", "code", "mirror", "all").
func (a *App) Find(names []string) ([]service.Service, error) {
	if len(names) == 0 || (len(names) == 1 && names[0] == "all") {
		return a.Core(), nil
	}
	var out []service.Service
	for _, n := range names {
		found := false
		for _, s := range a.All() {
			if s.Name() == n || s.Title() == n {
				out = append(out, s)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown service %q (use web, php, db, code, mirror or all)", n)
		}
	}
	return out, nil
}

// Sites discovers served sites.
func (a *App) Sites() ([]site.Site, []string) {
	return site.Discover(a.Cfg.Web.Root, a.Cfg.Sites.Links)
}

// Ctx builds the service context: template data, sites and certificate paths.
// It does not touch the network or issue certificates.
func (a *App) Ctx() (*service.Ctx, []string) {
	sites, warnings := a.Sites()
	d := render.Data{
		Prefix:     a.Env.Prefix,
		RunDir:     a.Paths.Run(),
		LogDir:     a.Paths.Log(),
		TmpDir:     a.Paths.Tmp(),
		AdminerDir: a.Paths.Adminer(),
		Listen:     "127.0.0.1",
		Port:       a.Cfg.Web.Port,
		HTTPSPort:  a.Cfg.Web.HTTPSPort,
		HTTPS:      a.Cfg.Web.HTTPS,
		Adminer:    a.Cfg.Adminer && a.Cfg.DB.Engine != config.DBNone && adminer.Installed(a.Paths.Adminer()),
		PHPSock:    a.Paths.PHPSocket(),
		MySQLSock:  service.MySQLSocket(a.Env.Prefix),
		DBEngine:   a.Cfg.DB.Engine,
		DBPort:     a.Cfg.DB.Port,
		PHP:        a.Cfg.PHP,
		TimeZone:   a.Env.TimeZone,
	}
	if a.Cfg.Web.Share {
		d.Listen = "0.0.0.0"
	}
	d.PHPExtensions = a.Cfg.PHP.Extensions
	keys := make([]string, 0, len(a.Cfg.PHP.Settings))
	for k := range a.Cfg.PHP.Settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d.PHPSettings = append(d.PHPSettings, render.KV{Key: k, Value: a.Cfg.PHP.Settings[k]})
	}
	if st, err := os.Stat(a.bundlePath()); err == nil && !st.IsDir() {
		d.CABundle = a.bundlePath()
	}
	lan := []string(nil)
	if a.Cfg.Web.Share {
		lan = netinfo.LANIPs()
	}
	for _, s := range sites {
		rs := render.Site{Site: s,
			CertFile: filepath.Join(a.Paths.Certs(), s.CertName()+".crt"),
			KeyFile:  filepath.Join(a.Paths.Certs(), s.CertName()+".key"),
		}
		// Other devices cannot resolve *.localhost; nip.io maps <x>.<ip>.nip.io to <ip>.
		if !s.Default {
			for _, ip := range lan {
				rs.Aliases = append(rs.Aliases, fmt.Sprintf("%s.%s.nip.io", s.Name, ip))
			}
		}
		d.Sites = append(d.Sites, rs)
	}
	return &service.Ctx{Env: a.Env, Paths: a.Paths, Cfg: a.Cfg, Creds: a.Creds, R: a.R, Data: d}, warnings
}

func (a *App) bundlePath() string { return filepath.Join(a.Paths.CA(), "bundle.pem") }

// SystemBundle is Termux's CA bundle from the ca-certificates package.
func (a *App) SystemBundle() string {
	return filepath.Join(a.Env.Prefix, "etc", "tls", "cert.pem")
}

// ensureCerts creates the CA if needed and (re)issues site certificates.
func (a *App) ensureCerts(c *service.Ctx) error {
	if !a.Cfg.Web.HTTPS {
		return nil
	}
	ca, created, err := pki.LoadOrCreateCA(a.Paths.CA(), a.caName(), a.Now())
	if err != nil {
		return fmt.Errorf("local CA: %w", err)
	}
	if created {
		a.printf("Created local CA %s. Run `hampp ssl trust` to trust it in Android.\n", a.caName())
	}
	for _, s := range c.Data.Sites {
		hosts := s.Hosts()
		if a.Cfg.Web.Share {
			for _, ip := range netinfo.LANIPs() {
				hosts = append(hosts, ip)
			}
			hosts = append(hosts, s.Aliases...)
		}
		if need, why := ca.NeedsIssue(s.CertFile, hosts, a.Now()); need {
			if err := ca.Issue(hosts, s.CertFile, s.KeyFile, a.Now()); err != nil {
				return fmt.Errorf("certificate for %s: %w", s.Host, err)
			}
			if why != "missing" {
				a.printf("Re-issued certificate for %s (%s)\n", s.Host, why)
			}
		}
	}
	if pki.BundleStale(a.SystemBundle(), a.bundlePath()) {
		if err := pki.WriteBundle(a.SystemBundle(), ca.CertPath, a.bundlePath()); err == nil {
			c.Data.CABundle = a.bundlePath()
		}
	}
	return nil
}

func (a *App) caName() string {
	if a.Env.Model != "" {
		return "hampp local CA (" + a.Env.Model + ")"
	}
	return "hampp local CA"
}
