//go:build unix

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourchocomate/hampp/internal/service"
	"github.com/yourchocomate/hampp/internal/sys"
)

func TestParsePHPModules(t *testing.T) {
	m := ParsePHPModules("[PHP Modules]\nCore\ngd\nPDO\nredis\n\n[Zend Modules]\nZend OPcache\n")
	for _, want := range []string{"core", "gd", "pdo", "redis", "opcache"} {
		if !m[want] {
			t.Errorf("missing %s", want)
		}
	}
	if m["[php modules]"] {
		t.Error("headers are not modules")
	}
}

func TestParsePHPPackages(t *testing.T) {
	// Real `apt-cache search ^php-` output from Termux (2026-09).
	out := `php-apache - Apache 2.0 Handler module for PHP
php-apache-ldap - LDAP module for PHP/Apache
php-apcu - APCu - APC User Cache
php-fpm - FastCGI Process Manager for PHP
php-gd - gd module for PHP
php-redis - PHP extension for interfacing with Redis
php-zephir-parser - The Zephir Parser delivered as a C extension for the PHP language`
	p := ParsePHPPackages(out)
	if len(p) != 4 || p["php-apcu"] != "APCu - APC User Cache" || p["php-redis"] == "" {
		t.Fatalf("%v", p)
	}
	for _, sapi := range []string{"php-fpm", "php-apache", "php-apache-ldap"} {
		if _, ok := p[sapi]; ok {
			t.Errorf("%s is not an extension", sapi)
		}
	}
	if extName("php-zephir-parser") != "zephir_parser" {
		t.Error("extName")
	}
}

func TestPHPExtInstallEnablesPackagesWithoutIni(t *testing.T) {
	a, f := newTestApp(t)
	php := a.Env.Bin("php")
	scan := "PHP_INI_SCAN_DIR=" + service.ScanDir(a.Paths)
	f.Responses["env "+scan+" "+php+" -c "+filepath.Join(a.Paths.Conf(), "php.ini")+" -m"] = sys.FakeResult{Out: "[PHP Modules]\nCore\ngd\n"}
	f.Responses["apt-cache search ^php-"] = sys.FakeResult{Out: "php-gd - gd module for PHP\nphp-redis - PHP extension for interfacing with Redis\n"}
	f.Responses["dpkg-query -W -f=${Status} php-gd"] = sys.FakeResult{Out: "install ok installed"}
	f.Responses["dpkg -L php-gd"] = sys.FakeResult{Out: a.Env.Prefix + "/etc/php/conf.d/gd.ini\n" + a.Env.Prefix + "/lib/php/gd.so\n"}
	f.Responses["dpkg -L php-redis"] = sys.FakeResult{Out: a.Env.Prefix + "/lib/php/redis.so\n"}
	// After enabling, PHP reports redis as loaded.
	loadedAfter := sys.FakeResult{Out: "[PHP Modules]\nCore\ngd\nredis\n"}

	exts, err := a.PHPExtList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]ExtState{}
	for _, e := range exts {
		states[e.Name] = e.State
	}
	if states["gd"] != ExtEnabled || states["redis"] != ExtAvailable || states["core"] != ExtBuiltIn {
		t.Fatalf("states: %v", states)
	}

	f.Responses["env "+scan+" "+php+" -c "+filepath.Join(a.Paths.Conf(), "php.ini")+" -m"] = loadedAfter
	if err := a.PHPExtInstall(context.Background(), "redis"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.Calls, "\n")
	if !strings.Contains(joined, "pkg install -y php-redis") {
		t.Fatalf("package not installed:\n%s", joined)
	}
	if len(a.Cfg.PHP.Extensions) != 1 || a.Cfg.PHP.Extensions[0] != "redis" {
		t.Fatalf("redis ships no ini, so hampp must enable it: %v", a.Cfg.PHP.Extensions)
	}
	b, err := os.ReadFile(filepath.Join(service.HamppIniDir(a.Paths), "20-extensions.ini"))
	if err != nil || !strings.Contains(string(b), "extension=redis\n") {
		t.Fatalf("extensions ini: %s %v", b, err)
	}
	// gd is enabled by its package's own ini and must not be duplicated or disabled by hampp.
	if err := a.PHPExtDisable(context.Background(), "gd"); err == nil || !strings.Contains(err.Error(), "remove") {
		t.Fatalf("got %v", err)
	}
}

func TestPHPSetUploadSyncsWebLimit(t *testing.T) {
	a, f := newTestApp(t)
	f.Responses["env*"] = sys.FakeResult{Out: "'64M'"}
	if err := a.PHPSet(context.Background(), "upload_max_filesize", "128m", false); err != nil {
		t.Fatal(err)
	}
	if a.Cfg.PHP.UploadMax != "128M" {
		t.Fatal(a.Cfg.PHP.UploadMax)
	}
	if err := a.PHPSet(context.Background(), "upload_max_filesize", "lots", false); err == nil {
		t.Fatal("size must be validated")
	}
	if err := a.PHPSet(context.Background(), "max_execution_time", "300", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(service.HamppIniDir(a.Paths), "50-settings.ini"))
	if !strings.Contains(string(b), `max_execution_time = "300"`) {
		t.Fatalf("%s", b)
	}
	f.Responses["env*"] = sys.FakeResult{Out: "false"}
	if err := a.PHPSet(context.Background(), "xdebug.mode", "debug", false); err == nil {
		t.Fatal("unknown keys need --force")
	}
	if err := a.PHPSet(context.Background(), "xdebug.mode", "debug", true); err != nil {
		t.Fatal(err)
	}
	if err := a.PHPUnset(context.Background(), "xdebug.mode"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.Paths.Config, "php.d", "custom.ini")); err != nil {
		t.Fatal("user ini file should be created for `hampp php edit`")
	}
}

func TestBrokenExtensionIsRolledBack(t *testing.T) {
	a, f := newTestApp(t)
	php := a.Env.Bin("php")
	web := "env PHP_INI_SCAN_DIR=" + service.ScanDir(a.Paths) + " " + php + " -c " + filepath.Join(a.Paths.Conf(), "php.ini")
	// Real Termux output (2026-09): php-redis built against PHP 8.4's module API.
	warning := "Warning: PHP Startup: redis: Unable to initialize module\nModule compiled with module API=20240924\nPHP    compiled with module API=20250925\nThese options need to match\n in Unknown on line 0\n"
	f.Responses[web+" -m"] = sys.FakeResult{Out: warning + "[PHP Modules]\nCore\n"}
	f.Responses[web+" -d display_startup_errors=1 -r"] = sys.FakeResult{Out: warning}
	f.Responses["apt-cache search ^php-"] = sys.FakeResult{Out: "php-redis - PHP extension for interfacing with Redis\n"}
	f.Responses["dpkg-query -W -f=${Status} php-redis"] = sys.FakeResult{Out: "install ok installed"}
	f.Responses["dpkg -L php-redis"] = sys.FakeResult{Out: a.Env.Prefix + "/lib/php/redis.so\n"}

	err := a.PHPExtEnable(context.Background(), "redis")
	if err == nil || !strings.Contains(err.Error(), "different PHP version") || !strings.Contains(err.Error(), "20240924") {
		t.Fatalf("got %v", err)
	}
	if len(a.Cfg.PHP.Extensions) != 0 {
		t.Fatalf("broken module must be switched off again: %v", a.Cfg.PHP.Extensions)
	}
	b, _ := os.ReadFile(filepath.Join(service.HamppIniDir(a.Paths), "20-extensions.ini"))
	if strings.Contains(string(b), "extension=redis") {
		t.Fatal("ini must not keep the broken module")
	}
	if m := ParsePHPModules(warning + "[PHP Modules]\nCore\n"); len(m) != 1 || !m["core"] {
		t.Fatalf("warnings are not module names: %v", m)
	}
}

func TestMissingLibrariesAndINIParsing(t *testing.T) {
	// Real Termux output (2026-09) for php-apcu.
	msg := `Warning: PHP Startup: Unable to load dynamic library 'apcu' (tried: /usr/lib/php/apcu (dlopen failed: library "/usr/lib/php/apcu" not found), /usr/lib/php/apcu.so (dlopen failed: library "libandroid-shmem.so" not found)) in Unknown on line 0`
	if got := MissingLibraries(msg); len(got) != 1 || got[0] != "libandroid-shmem" {
		t.Fatalf("%v", got)
	}
	ini := ParseINI("; comment\n[PHP]\nmax_execution_time = 120\ndisplay_errors = Off ; trailing\nerror_log = \"/x/y.log\"\n")
	if ini["max_execution_time"] != "120" || ini["display_errors"] != "" || ini["error_log"] != "/x/y.log" {
		t.Fatalf("%v", ini)
	}
}

func TestPHPGetWebValuesForCLIForcedKeys(t *testing.T) {
	a, f := newTestApp(t)
	f.Responses["env*"] = sys.FakeResult{Out: "max_execution_time\t'0'\n"}
	a.Cfg.PHP.Settings = map[string]string{"max_execution_time": "300"}
	c, _ := a.Ctx()
	if err := (service.PHPFPM{}).Configure(c); err != nil {
		t.Fatal(err)
	}
	vals, err := a.PHPGet(context.Background(), []string{"max_execution_time"})
	if err != nil {
		t.Fatal(err)
	}
	if v := vals["max_execution_time"]; v[0] != "300" || v[1] != "0" {
		t.Fatalf("websites must see 300 from the ini chain, CLI 0: %v", v)
	}
}

func TestOpcacheLockFailureIsRecovered(t *testing.T) {
	a, _ := newTestApp(t)
	if err := a.Paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	c, _ := a.Ctx()
	// The exact wording differs between PHP versions; both must be recognised.
	for _, msg := range []string{
		"exit status 255: Sun Sep 20 13:29:35 2026 (25582): Error Cannot create lock - Permission denied (13)",
		"Fatal Error Unable to create opcache lock file in /data/data/com.termux/files/usr/tmp: Permission denied (13)",
	} {
		if !service.OpcacheLockFailure(errors.New(msg)) {
			t.Fatalf("not recognised: %s", msg)
		}
	}
	if service.OpcacheLockFailure(errors.New("exit status 1: syntax error")) {
		t.Fatal("unrelated errors must not disable OPcache")
	}

	if !a.recoverOpcache(context.Background(), c, errors.New("Error Cannot create lock - Permission denied (13)")) {
		t.Fatal("should disable OPcache and ask for a retry")
	}
	b, err := os.ReadFile(filepath.Join(service.HamppIniDir(a.Paths), service.OpcacheOffIni))
	if err != nil || !strings.Contains(string(b), "opcache.enable = 0") {
		t.Fatalf("ini: %s %v", b, err)
	}
	if !service.OpcacheDisabled(a.Paths) {
		t.Fatal("OpcacheDisabled")
	}
	// Only once: a second failure means something else is wrong.
	if a.recoverOpcache(context.Background(), c, errors.New("Cannot create lock - Permission denied (13)")) {
		t.Fatal("must not loop")
	}
}
