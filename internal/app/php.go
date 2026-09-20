//go:build unix

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/pkgmgr"
	"github.com/yourchocomate/hampp/internal/service"
)

// ExtState describes where a PHP extension stands.
type ExtState int

const (
	ExtBuiltIn   ExtState = iota // compiled into Termux's php
	ExtEnabled                   // shared module, loaded
	ExtInstalled                 // package installed but module not loaded
	ExtAvailable                 // package can be installed
)

func (s ExtState) String() string {
	return [...]string{"built-in", "enabled", "installed, off", "available"}[s]
}

// Ext is one row of `hampp php ext`.
type Ext struct {
	Name        string   // main module name, e.g. redis
	Package     string   // Termux package, "" for built-in
	Modules     []string // shared modules the package ships
	State       ExtState
	ByHampp     bool // enabled by hampp (php.extensions), so it can be disabled
	Description string
}

// php runs the PHP CLI with hampp's ini scan dirs. With web=true it also
// loads php-fpm's base php.ini, so values match what websites see.
func (a *App) php(ctx context.Context, web bool, args ...string) (string, error) {
	full := []string{"PHP_INI_SCAN_DIR=" + service.ScanDir(a.Paths), a.Env.Bin("php")}
	if web {
		full = append(full, "-c", filepath.Join(a.Paths.Conf(), "php.ini"))
	}
	return a.R.Output(ctx, "", "env", append(full, args...)...)
}

// refreshPHPIni re-renders ini files so the CLI sees changes immediately and
// reloads php-fpm if it is running (USR2 also reloads extensions).
func (a *App) refreshPHPIni(ctx context.Context) error {
	if err := a.Paths.Ensure(); err != nil {
		return err
	}
	c, _ := a.Ctx()
	if err := (service.PHPFPM{}).Configure(c); err != nil {
		return err
	}
	if !(service.PHPFPM{}).Status(c).Running {
		return nil
	}
	if err := (service.PHPFPM{}).Reload(ctx, c); err != nil {
		return fmt.Errorf("php-fpm reload: %w", err)
	}
	a.printf("  php     php-fpm reloaded\n")
	return nil
}

func (a *App) loadedModules(ctx context.Context) (map[string]bool, error) {
	out, err := a.php(ctx, true, "-m")
	if err != nil {
		return nil, err
	}
	return ParsePHPModules(out), nil
}

// ParsePHPModules parses `php -m`, lowercasing names ("Zend OPcache" → "zend opcache").
func ParsePHPModules(out string) map[string]bool {
	m := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "[") || strings.Contains(l, ":") || strings.Contains(l, "compiled with") || strings.HasPrefix(l, "These options") || strings.HasPrefix(l, "in Unknown") {
			continue // startup warnings are printed on stdout too
		}
		m[strings.ToLower(l)] = true
	}
	if m["zend opcache"] {
		m["opcache"] = true
	}
	return m
}

// ParsePHPPackages reads `apt-cache search ^php-` output into package → description,
// skipping the SAPIs (php-fpm, php-apache*), which are not extensions.
func ParsePHPPackages(out string) map[string]string {
	pkgs := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		name, desc, ok := strings.Cut(strings.TrimSpace(l), " - ")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if !strings.HasPrefix(name, "php-") || name == "php-fpm" || strings.HasPrefix(name, "php-apache") || name == "php-dev" {
			continue
		}
		pkgs[name] = strings.TrimSpace(desc)
	}
	return pkgs
}

// extName guesses the module name from a package name (php-zephir-parser → zephir_parser).
func extName(pkg string) string {
	return strings.ReplaceAll(strings.TrimPrefix(pkg, "php-"), "-", "_")
}

func (a *App) phpPackages(ctx context.Context) (map[string]string, error) {
	var out string
	var err error
	if a.PM.Kind == pkgmgr.Pacman {
		out, err = a.R.Output(ctx, "", "pacman", "-Ss", "^php-")
	} else {
		out, err = a.R.Output(ctx, "", "apt-cache", "search", "^php-")
	}
	if err != nil {
		return nil, err
	}
	return ParsePHPPackages(out), nil
}

// packageModules lists the shared modules (.so) a package installs and
// whether it ships its own ini file in $PREFIX/etc/php/conf.d.
func (a *App) packageModules(ctx context.Context, pkg string) (mods []string, ownIni bool) {
	var out string
	var err error
	if a.PM.Kind == pkgmgr.Pacman {
		out, err = a.R.Output(ctx, "", "pacman", "-Qlq", pkg)
	} else {
		out, err = a.R.Output(ctx, "", "dpkg", "-L", pkg)
	}
	if err != nil {
		return nil, false
	}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.Contains(l, "/lib/php/") && strings.HasSuffix(l, ".so"):
			mods = append(mods, strings.TrimSuffix(filepath.Base(l), ".so"))
		case strings.Contains(l, "/etc/php/conf.d/") && strings.HasSuffix(l, ".ini"):
			ownIni = true
		}
	}
	return mods, ownIni
}

// PHPExtList returns built-in and packaged extensions with their state.
func (a *App) PHPExtList(ctx context.Context) ([]Ext, error) {
	if err := a.RequireTermux(); err != nil {
		return nil, err
	}
	loaded, err := a.loadedModules(ctx)
	if err != nil {
		return nil, fmt.Errorf("php -m: %w", err)
	}
	pkgs, err := a.phpPackages(ctx)
	if err != nil {
		return nil, err
	}
	fromPkg := map[string]bool{}
	var exts []Ext
	for pkg, desc := range pkgs {
		e := Ext{Name: extName(pkg), Package: pkg, Description: desc, State: ExtAvailable}
		if a.PM.Installed(ctx, pkg) {
			e.Modules, _ = a.packageModules(ctx, pkg)
			e.State = ExtInstalled
			for _, m := range e.Modules {
				fromPkg[m] = true
				if loaded[m] {
					e.State = ExtEnabled
				}
				if slices.Contains(a.Cfg.PHP.Extensions, m) {
					e.ByHampp = true
				}
			}
		}
		fromPkg[e.Name] = true
		exts = append(exts, e)
	}
	for m := range loaded {
		if !fromPkg[m] && m != "zend opcache" {
			exts = append(exts, Ext{Name: m, State: ExtBuiltIn})
		}
	}
	sort.Slice(exts, func(i, j int) bool {
		if exts[i].State != exts[j].State {
			return exts[i].State < exts[j].State
		}
		return exts[i].Name < exts[j].Name
	})
	return exts, nil
}

func (a *App) findExt(ctx context.Context, name string) (Ext, error) {
	exts, err := a.PHPExtList(ctx)
	if err != nil {
		return Ext{}, err
	}
	name = strings.ToLower(strings.TrimPrefix(name, "php-"))
	for _, e := range exts {
		if e.Name == name || e.Package == "php-"+strings.ReplaceAll(name, "_", "-") || slices.Contains(e.Modules, name) {
			return e, nil
		}
	}
	// pdo_pgsql lives in php-pgsql, etc.
	if base, ok := strings.CutPrefix(name, "pdo_"); ok {
		return a.findExt(ctx, base)
	}
	return Ext{}, fmt.Errorf("no PHP extension %q in Termux (see: hampp php ext)", name)
}

// PHPExtInstall installs extension packages and makes sure their modules load.
// Some Termux packages (php-redis, php-apcu, …) ship no ini file, so hampp
// enables their modules itself.
func (a *App) PHPExtInstall(ctx context.Context, names ...string) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	for _, n := range names {
		e, err := a.findExt(ctx, n)
		if err != nil {
			return err
		}
		if e.State == ExtBuiltIn {
			a.printf("%s is built into PHP; nothing to install.\n", e.Name)
			continue
		}
		if e.State == ExtAvailable {
			if err := a.PM.Install(ctx, a.Out, e.Package); err != nil {
				return err
			}
		}
		mods, ownIni := a.packageModules(ctx, e.Package)
		if !ownIni {
			for _, m := range mods {
				if !slices.Contains(a.Cfg.PHP.Extensions, m) {
					a.Cfg.PHP.Extensions = append(a.Cfg.PHP.Extensions, m)
				}
			}
		}
	}
	return a.applyExtensions(ctx, names)
}

// applyExtensions saves config, reloads PHP and verifies the requested
// extensions load. Modules that fail (for example a package built for another
// PHP version) are switched off again, so one broken package never leaves
// every PHP command printing startup warnings.
func (a *App) applyExtensions(ctx context.Context, names []string) error {
	if err := a.SaveConfig(); err != nil {
		return err
	}
	if err := a.refreshPHPIni(ctx); err != nil {
		return err
	}
	loaded, err := a.loadedModules(ctx)
	if err != nil {
		return err
	}
	// Some Termux extension packages miss a runtime dependency (php-apcu needs
	// libandroid-shmem). Install libraries PHP reports as missing, then retry.
	if libs := MissingLibraries(a.phpStartupErrors(ctx)); len(libs) > 0 {
		a.printf("PHP extensions need missing libraries: %s; installing them…\n", strings.Join(libs, " "))
		if err := a.PM.Install(ctx, a.Out, libs...); err == nil {
			if err := a.refreshPHPIni(ctx); err != nil {
				return err
			}
			if loaded, err = a.loadedModules(ctx); err != nil {
				return err
			}
		}
	}
	var failed []string
	a.Cfg.PHP.Extensions = slices.DeleteFunc(a.Cfg.PHP.Extensions, func(m string) bool {
		if loaded[m] {
			return false
		}
		failed = append(failed, m)
		return true
	})
	var errs []error
	if len(failed) > 0 {
		reason := a.phpStartupErrors(ctx)
		if err := a.SaveConfig(); err != nil {
			return err
		}
		if err := a.refreshPHPIni(ctx); err != nil {
			return err
		}
		for _, m := range failed {
			errs = append(errs, extLoadError(m, reason))
		}
	}
	for _, n := range names {
		n = strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(n, "php-")), "-", "_")
		if loaded[n] {
			a.printf("✓ %s is enabled for websites and the CLI.\n", n)
		}
	}
	return errors.Join(errs...)
}

var missingLibRe = regexp.MustCompile(`library "(lib[A-Za-z0-9_+.-]+?)\.so[0-9.]*" not found`)

// MissingLibraries extracts Termux package names for shared libraries that
// dlopen could not find, e.g. "libandroid-shmem.so" → libandroid-shmem.
func MissingLibraries(startup string) []string {
	var libs []string
	for _, m := range missingLibRe.FindAllStringSubmatch(startup, -1) {
		if !slices.Contains(libs, m[1]) {
			libs = append(libs, m[1])
		}
	}
	return libs
}

// phpStartupErrors returns PHP's startup warnings (e.g. module API mismatch).
func (a *App) phpStartupErrors(ctx context.Context) string {
	out, _ := a.php(ctx, true, "-d", "display_startup_errors=1", "-r", "")
	return strings.TrimSpace(out)
}

// extLoadError explains why a module did not load and what to do.
func extLoadError(module, startup string) error {
	var mine []string
	for _, l := range strings.Split(startup, "\n") {
		if strings.Contains(strings.ToLower(l), module) || strings.Contains(l, "compiled with module API") {
			if i := strings.Index(l, "cannot locate symbol"); i >= 0 {
				l = l[i:]
				if j := strings.Index(l, " referenced"); j > 0 {
					l = l[:j]
				}
			}
			mine = append(mine, strings.TrimSpace(l))
		}
	}
	detail := strings.Join(mine, " ")
	if strings.Contains(startup, "module API") || strings.Contains(startup, "cannot locate symbol") {
		return fmt.Errorf("%s was switched off again: Termux's package was built for a different PHP version (%s). Wait for Termux to rebuild it, then run `pkg upgrade` and `hampp php ext enable %s`", module, strings.TrimSpace(detail), module)
	}
	if detail == "" {
		detail = "no startup message"
	}
	if libs := MissingLibraries(startup); len(libs) > 0 {
		return fmt.Errorf("%s was switched off again: it needs %s, which could not be installed. Try: pkg install %s && hampp php ext enable %s", module, strings.Join(libs, ", "), strings.Join(libs, " "), module)
	}
	return fmt.Errorf("%s was switched off again because PHP could not load it (%s)", module, detail)
}

// PHPExtEnable loads an installed shared module that is currently off.
func (a *App) PHPExtEnable(ctx context.Context, name string) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	e, err := a.findExt(ctx, name)
	if err != nil {
		return err
	}
	switch e.State {
	case ExtBuiltIn, ExtEnabled:
		a.printf("%s is already enabled.\n", e.Name)
		return nil
	case ExtAvailable:
		return a.PHPExtInstall(ctx, name)
	}
	for _, m := range e.Modules {
		if !slices.Contains(a.Cfg.PHP.Extensions, m) {
			a.Cfg.PHP.Extensions = append(a.Cfg.PHP.Extensions, m)
		}
	}
	return a.applyExtensions(ctx, []string{name})
}

// PHPExtDisable turns off a module hampp enabled. Modules enabled by their
// package's own ini file are only removed by uninstalling the package, because
// hampp never edits package files.
func (a *App) PHPExtDisable(ctx context.Context, name string) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	e, err := a.findExt(ctx, name)
	if err != nil {
		return err
	}
	if e.State == ExtBuiltIn {
		return fmt.Errorf("%s is compiled into Termux's PHP and cannot be turned off", e.Name)
	}
	if !e.ByHampp {
		if e.State == ExtEnabled {
			return fmt.Errorf("%s is enabled by its package's own ini file; remove it with: hampp php ext remove %s", e.Name, e.Name)
		}
		a.printf("%s is not enabled.\n", e.Name)
		return nil
	}
	a.Cfg.PHP.Extensions = slices.DeleteFunc(a.Cfg.PHP.Extensions, func(m string) bool { return slices.Contains(e.Modules, m) })
	if err := a.SaveConfig(); err != nil {
		return err
	}
	a.printf("Disabled %s.\n", e.Name)
	return a.refreshPHPIni(ctx)
}

// PHPExtRemove uninstalls an extension package.
func (a *App) PHPExtRemove(ctx context.Context, name string) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	e, err := a.findExt(ctx, name)
	if err != nil {
		return err
	}
	if e.Package == "" {
		return fmt.Errorf("%s is built into PHP", e.Name)
	}
	a.Cfg.PHP.Extensions = slices.DeleteFunc(a.Cfg.PHP.Extensions, func(m string) bool { return slices.Contains(e.Modules, m) })
	if err := a.SaveConfig(); err != nil {
		return err
	}
	if err := a.refreshPHPIni(ctx); err != nil {
		return err
	}
	return a.PM.Uninstall(ctx, a.Out, e.Package)
}

// Settings with a dedicated config field, so the web server limits stay in sync
// (nginx's client_max_body_size follows the upload size).
var sizeRe = regexp.MustCompile(`^[0-9]+[KMG]?$`)

// PHPSet sets a php.ini value for websites and the CLI and applies it.
func (a *App) PHPSet(ctx context.Context, key, value string, force bool) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if !config.ValidINIKey(key) {
		return fmt.Errorf("%q is not a php.ini setting name", key)
	}
	if !force {
		if out, err := a.php(ctx, true, "-r", "var_export(ini_get('"+key+"'));"); err == nil && strings.TrimSpace(out) == "false" {
			return fmt.Errorf("PHP does not know %q (typo, or its extension is not enabled); use --force to set it anyway", key)
		}
	}
	web := false
	switch key {
	case "upload_max_filesize", "post_max_size":
		if !sizeRe.MatchString(strings.ToUpper(value)) {
			return fmt.Errorf("%s must be a size like 64M or 1G", key)
		}
		a.Cfg.PHP.UploadMax = strings.ToUpper(value)
		delete(a.Cfg.PHP.Settings, "upload_max_filesize")
		delete(a.Cfg.PHP.Settings, "post_max_size")
		a.printf("Upload limit is now %s (upload_max_filesize, post_max_size and the web server limit).\n", a.Cfg.PHP.UploadMax)
		web = a.Cfg.Web.Server == config.WebNginx
	case "memory_limit":
		if !sizeRe.MatchString(strings.ToUpper(value)) && value != "-1" {
			return fmt.Errorf("memory_limit must be a size like 256M, or -1")
		}
		a.Cfg.PHP.MemoryLimit = strings.ToUpper(value)
		delete(a.Cfg.PHP.Settings, key)
	case "display_errors":
		on := strings.EqualFold(value, "on") || value == "1" || strings.EqualFold(value, "true")
		a.Cfg.PHP.DisplayError = on
		delete(a.Cfg.PHP.Settings, key)
	default:
		if a.Cfg.PHP.Settings == nil {
			a.Cfg.PHP.Settings = map[string]string{}
		}
		a.Cfg.PHP.Settings[key] = value
	}
	if err := a.SaveConfig(); err != nil {
		return err
	}
	if err := a.refreshPHPIni(ctx); err != nil {
		return err
	}
	if web {
		return a.Reload(ctx)
	}
	return nil
}

// PHPUnset removes an override set with PHPSet.
func (a *App) PHPUnset(ctx context.Context, key string) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	if _, ok := a.Cfg.PHP.Settings[key]; !ok {
		return fmt.Errorf("%s was not set with `hampp php set`", key)
	}
	delete(a.Cfg.PHP.Settings, key)
	if err := a.SaveConfig(); err != nil {
		return err
	}
	return a.refreshPHPIni(ctx)
}

// CommonPHPSettings are shown by `hampp php` and the TUI.
var CommonPHPSettings = []string{"memory_limit", "upload_max_filesize", "post_max_size", "max_execution_time", "display_errors", "date.timezone", "opcache.enable"}

// PHPGet returns effective values for websites (php-fpm) and the CLI.
func (a *App) PHPGet(ctx context.Context, keys []string) (map[string][2]string, error) {
	if len(keys) == 0 {
		keys = CommonPHPSettings
	}
	script := func(keys []string) string {
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "echo %q, \"\\t\", var_export(ini_get(%q), true), \"\\n\";", k, k)
		}
		return b.String()
	}
	out := map[string][2]string{}
	for i, web := range []bool{true, false} {
		res, err := a.php(ctx, web, "-r", script(keys))
		if err != nil {
			return nil, err
		}
		for _, l := range strings.Split(strings.TrimSpace(res), "\n") {
			k, v, ok := strings.Cut(l, "\t")
			if !ok {
				continue
			}
			v = strings.Trim(v, "'")
			if v == "false" {
				v = "(unknown)"
			} else if v == "" {
				v = "(empty)"
			}
			cur := out[k]
			cur[i] = v
			out[k] = cur
		}
	}
	// The CLI SAPI hard-codes some values even over ini files (e.g.
	// max_execution_time=0, display_errors=1), so for websites read what the
	// ini chain sets, in php-fpm's load order.
	chain := a.webIniChain()
	for _, k := range keys {
		def, forced := cliForced[k]
		if !forced {
			continue
		}
		v, ok := chain[k]
		if !ok {
			v = def
		}
		cur := out[k]
		cur[0] = v
		out[k] = cur
	}
	return out, nil
}

// cliForced lists ini keys the PHP CLI overrides, with php-fpm's default.
var cliForced = map[string]string{
	"max_execution_time": "30", "max_input_time": "-1", "display_errors": "1",
	"html_errors": "1", "implicit_flush": "0", "output_buffering": "0", "register_argc_argv": "1",
}

// webIniChain parses the ini files php-fpm loads, in order; later values win.
func (a *App) webIniChain() map[string]string {
	files := []string{filepath.Join(a.Paths.Conf(), "php.ini")}
	for _, dir := range []string{filepath.Join(a.Env.Prefix, "etc", "php", "conf.d"), service.HamppIniDir(a.Paths), service.UserIniDir(a.Paths)} {
		m, _ := filepath.Glob(filepath.Join(dir, "*.ini"))
		sort.Strings(m)
		files = append(files, m...)
	}
	vals := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for k, v := range ParseINI(string(b)) {
			vals[k] = v
		}
	}
	return vals
}

// ParseINI reads simple key = value lines (PHP ini syntax), ignoring comments
// and sections. On/Off/true/false become 1/"" like ini_get reports them.
func ParseINI(s string) map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || l[0] == ';' || l[0] == '#' || l[0] == '[' {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if i := strings.Index(v, " ;"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		v = strings.Trim(v, `"'`)
		switch strings.ToLower(v) {
		case "on", "true", "yes":
			v = "1"
		case "off", "false", "no", "none":
			v = ""
		}
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

// PHPIniFiles lists the ini chain in load order, for `hampp php`.
func (a *App) PHPIniFiles() []string {
	return []string{
		filepath.Join(a.Paths.Conf(), "php.ini") + "   (websites only: base settings)",
		filepath.Join(a.Env.Prefix, "etc", "php", "conf.d") + "/*.ini   (Termux packages, e.g. gd.ini)",
		service.HamppIniDir(a.Paths) + "/*.ini   (hampp: sockets, CA, extensions, `php set`)",
		service.UserIniFile(a.Paths) + "   (yours: `hampp php edit`, loaded last)",
	}
}

// PHPUserIni returns the user's ini file, creating it if needed.
func (a *App) PHPUserIni() (string, error) {
	if err := a.Paths.Ensure(); err != nil {
		return "", err
	}
	c, _ := a.Ctx()
	if err := service.ConfigurePHPIni(c); err != nil {
		return "", err
	}
	return service.UserIniFile(a.Paths), nil
}

// PHPCheckUserIni validates the ini chain after an edit.
func (a *App) PHPCheckUserIni(ctx context.Context) error {
	out, err := a.php(ctx, true, "-r", "echo 'ok';")
	if err != nil || !strings.Contains(out, "ok") {
		return errors.New("PHP reports a problem after your edit: " + firstLineOf(fmt.Sprint(err)))
	}
	if strings.Contains(out, "Warning") || strings.Contains(out, "PHP Startup") {
		return errors.New(strings.TrimSpace(strings.Replace(out, "ok", "", 1)))
	}
	return a.refreshPHPIni(ctx)
}
