//go:build unix

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/proc"
	"github.com/yourchocomate/hampp/internal/site"
	"github.com/yourchocomate/hampp/internal/sys"
	"github.com/yourchocomate/hampp/internal/termux"
)

// Doctor runs environment checks. Every non-OK result carries a concrete fix.
func (a *App) Doctor(ctx context.Context) []Check {
	var out []Check
	add := func(l Level, msg, fix string) { out = append(out, Check{l, msg, fix}) }

	if !a.Env.IsTermux {
		add(Fail, "Not running inside Termux", "Install Termux from F-Droid or GitHub (github.com/termux/termux-app)")
		return out
	}
	ver := a.Env.AppVersion
	if ver == "" {
		ver = "version unknown"
	}
	switch a.Env.APKRelease {
	case "GOOGLE_PLAY_STORE":
		add(Warn, fmt.Sprintf("Termux %s from Google Play (unofficial build)", ver),
			"Official builds come from F-Droid or GitHub; switching requires uninstalling Termux and its add-ons first")
	default:
		add(OK, fmt.Sprintf("Termux %s (%s)", ver, a.Env.InstallSource()), "")
	}

	// Android's phantom process killer (Android 12+).
	if a.Env.SDK >= 31 {
		fix := strings.Join(termux.PhantomKillerFix(a.Env.SDK), "\n")
		add(Warn, fmt.Sprintf("Android %s may kill background servers (phantom process limit: 32 across all apps)", termux.AndroidVersion(a.Env.SDK)), fix)
	} else if a.Env.SDK > 0 {
		add(OK, "Android "+termux.AndroidVersion(a.Env.SDK), "")
	}
	if n := proc.CountByNames("httpd", "nginx", "php-fpm", "mariadbd", "node", "rsync"); n > 12 {
		add(Warn, fmt.Sprintf("%d server processes running; Android allows 32 across all apps", n), "Stop what you don't need: hampp stop code mirror")
	}
	if fb := sys.FallbackName(); fb != "" {
		add(Warn, "Android blocks running programs from $PREFIX directly; hampp runs them through "+fb,
			"This is normal on the Google Play build of Termux. The official F-Droid or GitHub build avoids it: github.com/termux/termux-app")
	}
	if a.Cfg.WakeLock {
		add(OK, "Wake lock is taken while services run", "")
	} else {
		add(Warn, "Wake lock disabled: servers may stop when the screen is off", "hampp config set wake_lock true")
	}

	if !a.Initialized {
		add(Fail, "hampp is not set up", "hampp init")
		return append(out, a.v1Checks()...)
	}

	var pkgs []string
	for _, s := range a.Core() {
		pkgs = append(pkgs, s.Packages()...)
	}
	if missing := a.PM.Missing(ctx, pkgs...); len(missing) > 0 {
		add(Fail, "Missing packages: "+strings.Join(missing, " "), strings.Join(a.PM.InstallArgs(missing...), " "))
	} else {
		add(OK, "Packages installed: "+strings.Join(pkgs, " "), "")
	}

	out = append(out, a.pathChecks(ctx)...)

	c, warnings := a.Ctx()
	for _, w := range warnings {
		add(Warn, w, "")
	}
	statuses := map[string]bool{}
	for _, s := range a.All() {
		statuses[s.Name()] = s.Status(c).Running
	}
	for _, p := range []struct {
		name string
		host string
		port int
	}{{"web", c.Data.Listen, a.Cfg.Web.Port}, {"db", "127.0.0.1", a.Cfg.DB.Port}} {
		if p.name == "db" && a.Cfg.DB.Engine != config.DBMariaDB {
			continue
		}
		if !statuses[p.name] && !proc.PortFree(p.host, p.port) {
			add(Fail, fmt.Sprintf("Port %d is used by another program", p.port),
				fmt.Sprintf("Stop it (e.g. `sv down httpd` / `sv down mysqld`) or: hampp config set %s", map[string]string{"web": "web.port 8081", "db": "db.port 3307"}[p.name]))
		}
	}
	if a.Cfg.Web.HTTPS && !statuses["web"] && !proc.PortFree(c.Data.Listen, a.Cfg.Web.HTTPSPort) {
		add(Fail, fmt.Sprintf("HTTPS port %d is used by another program", a.Cfg.Web.HTTPSPort), "hampp config set web.https_port 8444")
	}

	if site.OnSharedStorage(a.Cfg.Web.Root) {
		add(Warn, "Web root is on shared storage (/sdcard): npm, composer and Laravel symlinks fail there",
			"Move projects to ~/www (hampp config set web.root ~/www) and use `hampp mirror on`; if you keep it, run `git config core.filemode false`")
	} else {
		add(OK, "Web root "+a.Paths.Short(a.Cfg.Web.Root)+" is in Termux storage", "")
	}
	if a.Cfg.Mirror.Enabled {
		if _, err := os.Stat(a.Cfg.Mirror.Source); err != nil {
			add(Fail, "Mirror source "+a.Cfg.Mirror.Source+" not readable", "termux-setup-storage")
		}
	}

	if statuses["php"] {
		out = append(out, a.phpLocalhostCheck(ctx))
	}
	out = append(out, a.phpPathChecks(ctx)...)
	if msg := a.phpStartupErrors(ctx); strings.Contains(msg, "Warning") || strings.Contains(msg, "Error") {
		add(Warn, "PHP prints startup warnings: "+firstLineOf(strings.TrimSpace(strings.TrimPrefix(msg, "PHP"))),
			"hampp php ext   (disable the extension named above), or check ~/.config/hampp/php.d/custom.ini")
	}
	if a.Cfg.Web.HTTPS {
		for _, ch := range a.SSLStatus(ctx) {
			if ch.Level != OK {
				out = append(out, ch)
			}
		}
	}
	if a.PM.Installed(ctx, "code-server") {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		if _, err := a.R.Output(cctx, "", a.Env.Bin("code-server"), "--version"); err != nil {
			add(Fail, "code-server is installed but does not run (often a shared-library update in TUR)", "pkg upgrade && pkg reinstall code-server")
		}
		cancel()
	}
	if _, err := os.Stat(filepath.Join(a.Env.Home, ".nvm", "nvm.sh")); err == nil {
		add(Warn, "nvm is installed in ~/.nvm but cannot work on Termux (it rejects $PREFIX and downloads glibc builds)",
			"Use `hampp node install <major>` / `hampp node use`, then remove ~/.nvm and its lines in ~/.bashrc")
	}
	return append(out, a.v1Checks()...)
}

// phpLocalhostCheck tests whether PHP (bionic getaddrinfo) resolves *.localhost.
func (a *App) phpLocalhostCheck(ctx context.Context) Check {
	out, err := a.R.Output(ctx, "", a.Env.Bin("php"), "-r", `echo gethostbyname("hampp-check.localhost");`)
	if err == nil && strings.TrimSpace(out) == "127.0.0.1" {
		return Check{OK, "PHP resolves *.localhost", ""}
	}
	return Check{Warn, "PHP cannot resolve *.localhost (browsers and curl can)",
		fmt.Sprintf("For server-side requests to your own sites use http://127.0.0.1:%d with a Host header", a.Cfg.Web.Port)}
}

func (a *App) v1Checks() []Check {
	var out []Check
	for _, f := range a.DetectV1() {
		out = append(out, Check{Warn, "HamppServer v1: " + f, "hampp doctor --fix"})
	}
	return out
}
