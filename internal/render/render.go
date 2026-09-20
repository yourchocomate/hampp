// Package render turns embedded templates into hampp-owned config files.
package render

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/site"
	"github.com/yourchocomate/hampp/templates"
)

var tmpl = template.Must(template.New("").ParseFS(templates.FS, "*.tmpl"))

// Site is a site plus the files and aliases the web server needs.
type Site struct {
	site.Site
	CertFile string
	KeyFile  string
	Aliases  []string
}

type Module struct{ Name, Path string }

// KV is one php.ini setting.
type KV struct{ Key, Value string }

type Apache struct {
	Modules   []Module
	MimeTypes string
	HTTP2     bool
}

// Data is the single value passed to every template.
type Data struct {
	Prefix     string
	RunDir     string
	LogDir     string
	TmpDir     string
	AdminerDir string
	Listen     string
	Port       int
	HTTPSPort  int
	HTTPS      bool
	Adminer    bool
	PHPSock    string
	MySQLSock  string
	DBEngine   string
	DBPort     int
	PHP        config.PHP
	TimeZone   string
	CABundle   string
	Sites      []Site
	Apache     Apache

	PHPExtensions []string
	PHPSettings   []KV

	RootPassword string // client.cnf only
	PasswordHash string // adminer-plugins.php only
}

// Render executes the named template.
func Render(name string, d Data) ([]byte, error) {
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, name, d); err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	return b.Bytes(), nil
}

// WriteIfChanged renders name to path and reports whether the content changed.
func WriteIfChanged(name, path string, d Data, perm os.FileMode) (bool, error) {
	b, err := Render(name, d)
	if err != nil {
		return false, err
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
		return false, nil
	}
	return true, config.WriteFileAtomic(path, b, perm)
}

// apacheModules lists what hampp's httpd.conf needs. Termux builds every module
// shared (--enable-mods-shared=all); mpm_event is NOT built there, so worker is used.
var apacheModules = []struct {
	name, file string
	https      bool
}{
	{"mpm_worker_module", "mod_mpm_worker.so", false},
	{"unixd_module", "mod_unixd.so", false},
	{"authz_core_module", "mod_authz_core.so", false},
	{"authz_host_module", "mod_authz_host.so", false},
	{"dir_module", "mod_dir.so", false},
	{"mime_module", "mod_mime.so", false},
	{"log_config_module", "mod_log_config.so", false},
	{"alias_module", "mod_alias.so", false},
	{"rewrite_module", "mod_rewrite.so", false},
	{"headers_module", "mod_headers.so", false},
	{"proxy_module", "mod_proxy.so", false},
	{"proxy_fcgi_module", "mod_proxy_fcgi.so", false},
	{"socache_shmcb_module", "mod_socache_shmcb.so", true},
	{"ssl_module", "mod_ssl.so", true},
}

// ResolveApache locates modules under $PREFIX/libexec/apache2 and fails with a
// clear message if one is missing. mod_http2 is optional.
func ResolveApache(prefix string, https bool) (Apache, error) {
	dir := filepath.Join(prefix, "libexec", "apache2")
	var a Apache
	for _, m := range apacheModules {
		if m.https && !https {
			continue
		}
		p := filepath.Join(dir, m.file)
		if _, err := os.Stat(p); err != nil {
			return a, fmt.Errorf("apache module %s not found in %s (try: pkg install apache2)", m.file, dir)
		}
		a.Modules = append(a.Modules, Module{m.name, p})
	}
	if https {
		if p := filepath.Join(dir, "mod_http2.so"); fileExists(p) {
			a.Modules = append(a.Modules, Module{"http2_module", p})
			a.HTTP2 = true
		}
	}
	if p := filepath.Join(prefix, "etc", "apache2", "mime.types"); fileExists(p) {
		a.MimeTypes = p
	}
	return a, nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
