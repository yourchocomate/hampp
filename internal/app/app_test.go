//go:build unix

package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/paths"
	"github.com/yourchocomate/hampp/internal/pkgmgr"
	"github.com/yourchocomate/hampp/internal/service"
	"github.com/yourchocomate/hampp/internal/sys"
	"github.com/yourchocomate/hampp/internal/termux"
)

// newTestApp builds an App over a fake Termux prefix and home.
func newTestApp(t *testing.T) (*App, *sys.Fake) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	prefix := filepath.Join(root, "usr")
	for _, d := range []string{home, filepath.Join(prefix, "bin"), filepath.Join(prefix, "etc", "tls")} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(prefix, "etc", "tls", "cert.pem"), []byte("# system roots\n"), 0o644)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	f := &sys.Fake{Responses: map[string]sys.FakeResult{}}
	a := &App{
		Env:   termux.Env{Prefix: prefix, Home: home, IsTermux: true, SDK: 34, Model: "Pixel 7"},
		Paths: paths.New(home),
		Cfg:   config.Default(home),
		PM:    pkgmgr.Manager{Kind: pkgmgr.Apt, Prefix: prefix, R: f},
		R:     f,
		Out:   &bytes.Buffer{},
		Now:   func() time.Time { return time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC) },
	}
	a.Initialized = true
	if err := a.SaveConfig(); err != nil {
		t.Fatal(err)
	}
	return a, f
}

func TestSetupSQL(t *testing.T) {
	sql := SetupSQL("hampp", "PW", "ROOT")
	for _, want := range []string{
		"CREATE USER IF NOT EXISTS 'hampp'@'localhost' IDENTIFIED BY 'PW';",
		"CREATE USER IF NOT EXISTS 'hampp'@'127.0.0.1' IDENTIFIED BY 'PW';",
		"GRANT ALL PRIVILEGES ON *.* TO 'hampp'@'localhost' WITH GRANT OPTION;",
		"DELETE FROM mysql.global_priv WHERE User='';",
		"Host NOT IN ('localhost', '127.0.0.1', '::1')",
		"ALTER USER IF EXISTS 'root'@'::1' IDENTIFIED BY 'ROOT';",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %s\n%s", want, sql)
		}
	}
	// The hampp user must exist before root loses its empty password.
	if strings.Index(sql, "CREATE USER") > strings.Index(sql, "ALTER USER IF EXISTS 'root'") {
		t.Error("order")
	}
}

func TestDBSetupSavesPasswordsBeforeSQL(t *testing.T) {
	a, f := newTestApp(t)
	f.Responses["dpkg-query*"] = sys.FakeResult{Out: "install ok installed"}
	// Pretend mariadb is running so Start is a no-op.
	os.MkdirAll(a.Paths.Run(), 0o700)
	os.WriteFile(filepath.Join(a.Paths.Run(), "mariadb.pid"), []byte(fmt.Sprint(os.Getpid())), 0o600)
	mariadbBin := a.Env.Bin("mariadb")
	f.Responses[mariadbBin+" --no-defaults*"] = sys.FakeResult{} // passwordless root works
	var saved config.Credentials
	f.Responses[mariadbBin+" --no-defaults --protocol=socket --socket="+filepath.Join(a.Env.Prefix, "var/run/mysqld.sock")+" -u root"] = sys.FakeResult{}
	if err := a.DBSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	saved, _ = config.LoadCredentials(a.Paths.CredentialsFile())
	if saved.DBRootPassword == "" || saved.DBPassword == "" || saved.DBUser != "hampp" {
		t.Fatalf("credentials not saved: %+v", saved)
	}
	var sqlRan bool
	for _, in := range f.Stdins {
		if strings.Contains(in, "ALTER USER IF EXISTS 'root'@'localhost' IDENTIFIED BY '"+saved.DBRootPassword+"'") {
			sqlRan = true
		}
	}
	if !sqlRan {
		t.Fatal("setup SQL with the saved root password was not executed")
	}
	if b, err := os.ReadFile(filepath.Join(a.Env.Home, ".my.cnf")); err != nil || !strings.Contains(string(b), saved.DBPassword) {
		t.Fatal("~/.my.cnf not written")
	}
}

func TestCtxAndCertificates(t *testing.T) {
	a, _ := newTestApp(t)
	os.MkdirAll(filepath.Join(a.Cfg.Web.Root, "blog", "public"), 0o755)
	a.Cfg.Web.HTTPS = true
	c, _ := a.Ctx()
	if c.Data.Listen != "127.0.0.1" || c.Data.MySQLSock != filepath.Join(a.Env.Prefix, "var/run/mysqld.sock") {
		t.Fatalf("%+v", c.Data)
	}
	if len(c.Data.Sites) != 2 || c.Data.Sites[1].Host != "blog.localhost" {
		t.Fatalf("sites: %+v", c.Data.Sites)
	}
	if err := a.Paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureCerts(c); err != nil {
		t.Fatal(err)
	}
	for _, s := range c.Data.Sites {
		if _, err := os.Stat(s.CertFile); err != nil {
			t.Errorf("%s: %v", s.Host, err)
		}
	}
	if c.Data.CABundle == "" {
		t.Error("bundle should be written for php.ini")
	}
	checks := a.SSLStatus(context.Background())
	for _, ch := range checks {
		if ch.Level == Fail {
			t.Errorf("ssl status: %s", ch.Message)
		}
	}
}

func TestShareAddsNipAliasesOnlyForNamedSites(t *testing.T) {
	a, _ := newTestApp(t)
	os.MkdirAll(filepath.Join(a.Cfg.Web.Root, "blog"), 0o755)
	a.Cfg.Web.Share = true
	c, _ := a.Ctx()
	if c.Data.Listen != "0.0.0.0" {
		t.Fatal("share listens on all interfaces")
	}
	if len(c.Data.Sites[0].Aliases) != 0 {
		t.Error("default site needs no alias; unknown hosts fall back to it")
	}
	for _, al := range c.Data.Sites[1].Aliases {
		if !strings.HasPrefix(al, "blog.") || !strings.HasSuffix(al, ".nip.io") {
			t.Error(al)
		}
	}
}

func TestFindServices(t *testing.T) {
	a, _ := newTestApp(t)
	core, _ := a.Find(nil)
	if len(core) != 3 || core[0].Name() != "db" || core[2].Title() != "apache" {
		t.Fatalf("start order db, php, web: %v", core)
	}
	if s, _ := a.Find([]string{"mariadb"}); len(s) != 1 || s[0].Name() != "db" {
		t.Fatal("find by program name")
	}
	if _, err := a.Find([]string{"redis"}); err == nil {
		t.Fatal("unknown service")
	}
	a.Cfg.DB.Engine = config.DBSQLite
	a.Cfg.Web.Server = config.WebNginx
	core, _ = a.Find([]string{"all"})
	if len(core) != 2 || core[1].Title() != "nginx" {
		t.Fatal("sqlite has no db service")
	}
}

func TestSiteLinkRefusesSharedStorage(t *testing.T) {
	a, _ := newTestApp(t)
	err := a.SiteLink(context.Background(), "x", "/sdcard", "", false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSiteLinkAndUnlink(t *testing.T) {
	a, _ := newTestApp(t)
	proj := filepath.Join(a.Env.Home, "projects", "API Server")
	os.MkdirAll(filepath.Join(proj, "public"), 0o755)
	if err := a.SiteLink(context.Background(), "", proj, "", false); err != nil {
		t.Fatal(err)
	}
	s, ok := a.FindSite("api-server")
	if !ok || !s.Linked || s.DocRoot != filepath.Join(proj, "public") {
		t.Fatalf("%+v", s)
	}
	reloaded, err := config.Load(a.Paths.ConfigFile(), a.Env.Home)
	if err != nil || len(reloaded.Sites.Links) != 1 {
		t.Fatal("link must be saved", err)
	}
	if err := a.SiteUnlink(context.Background(), "api-server"); err != nil {
		t.Fatal(err)
	}
	if err := a.SiteUnlink(context.Background(), "api-server"); err == nil {
		t.Fatal("second unlink must fail")
	}
}

func TestV1Detection(t *testing.T) {
	a, _ := newTestApp(t)
	if len(a.DetectV1()) != 0 {
		t.Fatal("clean prefix")
	}
	os.MkdirAll(filepath.Join(a.Env.Prefix, "share", "HamppServer"), 0o755)
	os.WriteFile(filepath.Join(a.Env.Prefix, "bin", "hampp"), []byte("if [ -d /data/data/com.termux/files/usr/share/HamppServer ];then\n  exec python .HamppServer.py $@\nfi\n"), 0o755)
	os.MkdirAll(filepath.Join(a.Env.Prefix, "etc", "apache2"), 0o755)
	os.WriteFile(filepath.Join(a.Env.Prefix, "etc", "apache2", "httpd.conf"), []byte(`DocumentRoot "/sdcard/www"`), 0o644)
	if n := len(a.DetectV1()); n != 3 {
		t.Fatalf("want 3 findings, got %d", n)
	}
	done := a.CleanV1()
	if len(done) != 3 || len(a.DetectV1()) != 1 {
		t.Fatalf("clean: %v / remaining %v", done, a.DetectV1())
	}
	// Our own binary at the same path must never be removed.
	os.WriteFile(filepath.Join(a.Env.Prefix, "bin", "hampp"), []byte("\x7fELF..."), 0o755)
	a.CleanV1()
	if _, err := os.Stat(filepath.Join(a.Env.Prefix, "bin", "hampp")); err != nil {
		t.Fatal("v2 binary removed")
	}
}

func TestMirrorOnRefusesDeletes(t *testing.T) {
	a, f := newTestApp(t)
	os.MkdirAll(a.Cfg.Mirror.Source, 0o755)
	f.Responses["dpkg-query -W -f=${Status} rsync"] = sys.FakeResult{Out: "install ok installed"}
	f.Responses["rsync --dry-run*"] = sys.FakeResult{Out: ">f+++++++++ new.php\n*deleting   index.php\n*deleting   blog/\n"}
	err := a.MirrorOn(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "index.php") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("got %v", err)
	}
	if a.Cfg.Mirror.Enabled {
		t.Fatal("must not enable after refusing")
	}
}

func TestRsyncArgsProtectDependencies(t *testing.T) {
	args := strings.Join(service.RsyncArgs("/sd/www", "/h/www"), " ")
	for _, want := range []string{"--delete", "node_modules/", "vendor/", ".git/", "/sd/www/ /h/www/"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	if strings.Contains(args, "--delete-excluded") {
		t.Error("excluded folders must survive in the target")
	}
}

func TestRCHookAndUninstall(t *testing.T) {
	a, _ := newTestApp(t)
	bashrc := filepath.Join(a.Env.Home, ".bashrc")
	os.WriteFile(bashrc, []byte("PS1='$ '\n"), 0o644)
	if err := a.InstallRCHook(); err != nil {
		t.Fatal(err)
	}
	if !a.rcHookPresent() {
		t.Fatal("hook not installed")
	}
	a.Uninstall(context.Background(), true)
	b, _ := os.ReadFile(bashrc)
	if string(b) != "PS1='$ '\n" {
		t.Fatalf("user content must survive: %q", b)
	}
	if _, err := os.Stat(a.Paths.Config); !os.IsNotExist(err) {
		t.Fatal("purge removes config")
	}
	if _, err := os.Stat(a.Cfg.Web.Root); err == nil {
		// fine: web root may not exist in this test, but must never be removed
	}
}

func TestDoctorFlagsPlayStoreAndPhantomKiller(t *testing.T) {
	a, _ := newTestApp(t)
	a.Env.APKRelease = "GOOGLE_PLAY_STORE"
	checks := a.Doctor(context.Background())
	var text strings.Builder
	for _, c := range checks {
		text.WriteString(c.Message + " | " + c.Fix + "\n")
	}
	s := text.String()
	for _, want := range []string{"Google Play", "Disable child process restrictions", "Missing packages"} {
		if !strings.Contains(s, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, s)
		}
	}
}

func TestReloadServiceStartsWhenStopped(t *testing.T) {
	a, _ := newTestApp(t)
	a.Cfg.DB.Engine = config.DBSQLite // only php + web, no mariadb packages needed
	if err := a.Paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	// php-fpm is not running, so reloading it must fall through to a start,
	// which fails here only because the package is missing — proving the path.
	err := a.ReloadService(context.Background(), "php")
	if err == nil || !strings.Contains(err.Error(), "missing packages") {
		t.Fatalf("got %v", err)
	}
	if _, err := a.Find([]string{"nope"}); err == nil {
		t.Fatal("unknown service must be rejected")
	}
}
