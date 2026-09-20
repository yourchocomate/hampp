package render

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/site"
)

const prefix = "/data/data/com.termux/files/usr"

func data(https bool) Data {
	return Data{
		Prefix: prefix, RunDir: "/h/.local/state/hampp/run", LogDir: "/h/.local/state/hampp/log",
		TmpDir: "/h/.local/state/hampp/tmp", AdminerDir: "/h/.local/share/hampp/adminer",
		Listen: "127.0.0.1", Port: 8080, HTTPSPort: 8443, HTTPS: https, Adminer: true,
		PHPSock: "/h/.local/state/hampp/run/php-fpm.sock", MySQLSock: prefix + "/var/run/mysqld.sock",
		DBPort: 3306, PHP: config.Default("/h").PHP, TimeZone: "Asia/Dhaka",
		Sites: []Site{
			{Site: site.Site{Host: "localhost", DocRoot: "/h/www", Default: true}, CertFile: "/c/localhost.crt", KeyFile: "/c/localhost.key"},
			{Site: site.Site{Name: "blog", Host: "blog.localhost", DocRoot: "/h/www/blog/public"}, CertFile: "/c/blog.crt", KeyFile: "/c/blog.key",
				Aliases: []string{"blog.192.168.1.5.nip.io"}},
		},
		Apache: Apache{Modules: []Module{{"mpm_worker_module", prefix + "/libexec/apache2/mod_mpm_worker.so"}}, MimeTypes: prefix + "/etc/apache2/mime.types", HTTP2: true},
	}
}

func render(t *testing.T, name string, d Data) string {
	t.Helper()
	b, err := Render(name, d)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustContain(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("missing %q", sub)
		}
	}
}

func mustNotMatch(t *testing.T, s, re string) {
	t.Helper()
	if regexp.MustCompile(re).MatchString(s) {
		t.Errorf("must not match %q", re)
	}
}

func TestApacheConf(t *testing.T) {
	s := render(t, "httpd.conf.tmpl", data(true))
	mustContain(t, s,
		`ServerRoot "`+prefix+`"`,
		`LoadModule mpm_worker_module "`+prefix+`/libexec/apache2/mod_mpm_worker.so"`,
		"Listen 127.0.0.1:8080", "Listen 127.0.0.1:8443",
		"ServerLimit             1",
		`SetHandler "proxy:unix:/h/.local/state/hampp/run/php-fpm.sock|fcgi://localhost"`,
		`<Directory "/h/www/blog/public">`, "AllowOverride All",
		"ServerName blog.localhost", "ServerAlias blog.192.168.1.5.nip.io",
		`SSLCertificateFile "/c/blog.crt"`, "Protocols h2 http/1.1",
		"Require local", `PidFile "/h/.local/state/hampp/run/httpd.pid"`,
	)
	// Android is single-user and seccomp blocks setuid.
	mustNotMatch(t, s, `(?m)^\s*(User|Group)\s`)
	mustNotMatch(t, s, `mpm_event|mpm_prefork|php_module`)
	// The default site must be the first vhost so unknown hosts fall back to it.
	if strings.Index(s, "ServerName localhost\n    DocumentRoot") > strings.Index(s, "ServerName blog.localhost") {
		t.Error("default vhost must come first")
	}
	if strings.Count(s, "<VirtualHost") != 4 {
		t.Errorf("want 2 sites × http+https vhosts, got %d", strings.Count(s, "<VirtualHost"))
	}
}

func TestApacheHTTPOnly(t *testing.T) {
	d := data(false)
	d.Adminer = false
	s := render(t, "httpd.conf.tmpl", d)
	mustNotMatch(t, s, `8443|SSLEngine|Alias /adminer`)
}

func TestNginxConf(t *testing.T) {
	s := render(t, "nginx.conf.tmpl", data(true))
	mustContain(t, s,
		"worker_processes 1;",
		"listen 127.0.0.1:8080 default_server;", "listen 127.0.0.1:8443 ssl default_server;",
		"http2 on;", "server_name blog.localhost blog.192.168.1.5.nip.io;",
		`root "/h/www/blog/public";`, "fastcgi_pass unix:/h/.local/state/hampp/run/php-fpm.sock;",
		"try_files $uri $uri/ /index.php?$query_string;",
		`client_body_temp_path "/h/.local/state/hampp/tmp/nginx-body";`,
		"allow 127.0.0.1;", "client_max_body_size 64M;", "server_names_hash_bucket_size 128;",
	)
	if strings.Count(s, "default_server") != 2 {
		t.Error("only the default site may be default_server (http + https)")
	}
	mustNotMatch(t, s, `(?m)^\s*user\s`)
	mustNotMatch(t, s, `http3|quic`) // Chrome ignores user CAs over QUIC
}

func TestPHPFPM(t *testing.T) {
	s := render(t, "php-fpm.conf.tmpl", data(false))
	mustContain(t, s, "listen = /h/.local/state/hampp/run/php-fpm.sock", "pm = ondemand", "pm.max_children = 3", "daemonize = yes")
	mustNotMatch(t, s, `(?m)^\s*(user|group)\s*=`)
	mustNotMatch(t, s, `(?m)^\s*include\s*=`)
}

func TestPHPIni(t *testing.T) {
	d := data(false)
	d.CABundle = "/h/.local/share/hampp/ca/bundle.pem"
	s := render(t, "php.ini.tmpl", d)
	mustContain(t, s, `date.timezone = "Asia/Dhaka"`, "memory_limit = 256M", "post_max_size = 64M",
		`session.save_path = "/h/.local/state/hampp/tmp/php-sessions"`)
	d.TimeZone = ""
	s = render(t, "php.ini.tmpl", d)
	mustNotMatch(t, s, `date\.timezone`)
}

func TestSharedPHPIni(t *testing.T) {
	d := data(false)
	d.CABundle = "/b.pem"
	d.PHPExtensions = []string{"redis", "apcu"}
	d.PHPSettings = []KV{{"max_execution_time", "300"}, {"opcache.enable", "0"}}
	s := render(t, "php-hampp.ini.tmpl", d)
	mustContain(t, s, `pdo_mysql.default_socket = "`+prefix+`/var/run/mysqld.sock"`, `curl.cainfo = "/b.pem"`)
	mustNotMatch(t, s, `memory_limit|display_errors`)
	mustContain(t, render(t, "php-extensions.ini.tmpl", d), "extension=redis\nextension=apcu\n")
	mustContain(t, render(t, "php-settings.ini.tmpl", d), `max_execution_time = "300"`, `opcache.enable = "0"`)
	mustNotMatch(t, render(t, "php.ini.tmpl", d), `default_socket|cafile`) // moved to the shared dir
}

func TestMyCnf(t *testing.T) {
	s := render(t, "my.cnf.tmpl", data(false))
	mustContain(t, s, "datadir = "+prefix+"/var/lib/mysql", "bind-address = 127.0.0.1", "socket = "+prefix+"/var/run/mysqld.sock", "pid-file = /h/.local/state/hampp/run/mariadb.pid")
	// Even with LAN sharing the database must stay local.
	d := data(false)
	d.Listen = "0.0.0.0"
	mustContain(t, render(t, "my.cnf.tmpl", d), "bind-address = 127.0.0.1")
}

func TestIndexAndAdminerPlugin(t *testing.T) {
	d := data(false)
	d.DBEngine = "mariadb"
	s := render(t, "index.php.tmpl", d)
	mustContain(t, s, "<?php", "hampp is running", prefix+"/var/run/mysqld.sock", "http://127.0.0.1:8080")
	d.DBEngine = "sqlite"
	mustContain(t, render(t, "index.php.tmpl", d), "SQLite (no server)")
	mustNotMatch(t, s, `phpinfo\(`) // don't expose phpinfo when sharing on the LAN
	p := render(t, "adminer-plugins.php.tmpl", Data{PasswordHash: "$2y$10$abc"})
	mustContain(t, p, "new Adminer\\Password('$2y$10$abc')", "return array(")
}

func TestResolveApache(t *testing.T) {
	pre := t.TempDir()
	dir := filepath.Join(pre, "libexec", "apache2")
	os.MkdirAll(dir, 0o755)
	if _, err := ResolveApache(pre, false); err == nil || !strings.Contains(err.Error(), "pkg install apache2") {
		t.Fatalf("missing modules must error helpfully: %v", err)
	}
	for _, m := range apacheModules {
		os.WriteFile(filepath.Join(dir, m.file), nil, 0o644)
	}
	a, err := ResolveApache(pre, true)
	if err != nil {
		t.Fatal(err)
	}
	if a.HTTP2 {
		t.Error("http2 must be optional")
	}
	os.WriteFile(filepath.Join(dir, "mod_http2.so"), nil, 0o644)
	a, _ = ResolveApache(pre, true)
	if !a.HTTP2 || a.Modules[len(a.Modules)-1].Name != "http2_module" {
		t.Error("http2 should load when present")
	}
	plain, _ := ResolveApache(pre, false)
	for _, m := range plain.Modules {
		if m.Name == "ssl_module" || m.Name == "http2_module" {
			t.Error("no TLS modules without https")
		}
	}
}

func TestWriteIfChanged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.conf")
	d := data(false)
	changed, err := WriteIfChanged("my.cnf.tmpl", p, d, 0o600)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	changed, _ = WriteIfChanged("my.cnf.tmpl", p, d, 0o600)
	if changed {
		t.Fatal("identical render must not rewrite")
	}
}
