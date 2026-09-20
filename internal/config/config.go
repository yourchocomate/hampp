// Package config loads, validates and saves ~/.config/hampp/config.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	WebApache = "apache"
	WebNginx  = "nginx"

	DBMariaDB = "mariadb"
	DBSQLite  = "sqlite"
	DBNone    = "none"
)

type Config struct {
	Web      Web    `toml:"web"`
	DB       DB     `toml:"db"`
	PHP      PHP    `toml:"php"`
	Sites    Sites  `toml:"sites"`
	Code     Code   `toml:"code"`
	Mirror   Mirror `toml:"mirror"`
	WakeLock bool   `toml:"wake_lock" comment:"Hold a Termux wakelock while services run"`
	Adminer  bool   `toml:"adminer" comment:"Serve Adminer at /adminer (localhost only)"`
	RCHook   bool   `toml:"rc_hook" comment:"Manage a PATH/SSL block in ~/.bashrc and ~/.zshrc"`
}

type Web struct {
	Server    string `toml:"server" comment:"apache or nginx"`
	Root      string `toml:"root" comment:"Default site; subfolders become <name>.localhost"`
	Port      int    `toml:"port"`
	HTTPS     bool   `toml:"https"`
	HTTPSPort int    `toml:"https_port"`
	Share     bool   `toml:"share" comment:"Listen on all interfaces (LAN) instead of 127.0.0.1"`
}

type DB struct {
	Engine string `toml:"engine" comment:"mariadb, sqlite or none"`
	Port   int    `toml:"port"`
}

type PHP struct {
	MemoryLimit  string            `toml:"memory_limit"`
	UploadMax    string            `toml:"upload_max_filesize"`
	DisplayError bool              `toml:"display_errors"`
	Extensions   []string          `toml:"extensions" comment:"Extensions hampp enables (hampp php ext enable <name>)"`
	Settings     map[string]string `toml:"settings" comment:"php.ini overrides for php-fpm and the CLI (hampp php set <key> <value>)"`
}

type Sites struct {
	Links []Link `toml:"links"`
}

// Link is a project outside the web root, served at <Name>.localhost.
type Link struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
	Root string `toml:"root,omitempty" comment:"Document root relative to path; empty = auto-detect"`
}

type Code struct {
	Port int `toml:"port"`
}

type Mirror struct {
	Enabled  bool   `toml:"enabled"`
	Source   string `toml:"source"`
	Interval int    `toml:"interval_seconds"`
}

// Default returns the configuration used by a fresh `hampp init`.
func Default(home string) Config {
	return Config{
		Web:      Web{Server: WebApache, Root: filepath.Join(home, "www"), Port: 8080, HTTPSPort: 8443},
		DB:       DB{Engine: DBMariaDB, Port: 3306},
		PHP:      PHP{MemoryLimit: "256M", UploadMax: "64M", DisplayError: true},
		Code:     Code{Port: 8090},
		Mirror:   Mirror{Source: filepath.Join(home, "storage", "shared", "www"), Interval: 2},
		WakeLock: true,
		Adminer:  true,
		RCHook:   true,
	}
}

var ErrNotInitialized = errors.New("hampp is not set up yet; run: hampp init")

// Load reads the config file, filling unset fields from defaults.
func Load(path, home string) (Config, error) {
	c := Default(home)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, ErrNotInitialized
	}
	if err != nil {
		return c, err
	}
	if err := toml.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	c.Web.Root = expandHome(c.Web.Root, home)
	c.Mirror.Source = expandHome(c.Mirror.Source, home)
	for i := range c.Sites.Links {
		c.Sites.Links[i].Path = expandHome(c.Sites.Links[i].Path, home)
	}
	return c, c.Validate()
}

// Save writes the config atomically with 0600 permissions.
func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	header := "# hampp configuration. Edit with `hampp config edit` or `hampp config set`.\n\n"
	return WriteFileAtomic(path, append([]byte(header), b...), 0o600)
}

func (c Config) Validate() error {
	var errs []error
	switch c.Web.Server {
	case WebApache, WebNginx:
	default:
		errs = append(errs, fmt.Errorf("web.server must be apache or nginx, got %q", c.Web.Server))
	}
	switch c.DB.Engine {
	case DBMariaDB, DBSQLite, DBNone:
	default:
		errs = append(errs, fmt.Errorf("db.engine must be mariadb, sqlite or none, got %q", c.DB.Engine))
	}
	ports := map[string]int{"web.port": c.Web.Port, "web.https_port": c.Web.HTTPSPort, "db.port": c.DB.Port, "code.port": c.Code.Port}
	seen := map[int]string{}
	for k, p := range ports {
		if err := ValidPort(p); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", k, err))
		}
		if other, dup := seen[p]; dup {
			errs = append(errs, fmt.Errorf("%s and %s both use port %d", k, other, p))
		}
		seen[p] = k
	}
	if !filepath.IsAbs(c.Web.Root) {
		errs = append(errs, fmt.Errorf("web.root must be an absolute path, got %q", c.Web.Root))
	}
	names := map[string]bool{}
	for _, l := range c.Sites.Links {
		if !ValidSiteName(l.Name) {
			errs = append(errs, fmt.Errorf("site link %q: name must be lowercase letters, digits and dashes", l.Name))
		}
		if names[l.Name] {
			errs = append(errs, fmt.Errorf("site link %q is defined twice", l.Name))
		}
		names[l.Name] = true
		if !filepath.IsAbs(l.Path) {
			errs = append(errs, fmt.Errorf("site link %q: path must be absolute", l.Name))
		}
	}
	for k, v := range c.PHP.Settings {
		if !ValidINIKey(k) {
			errs = append(errs, fmt.Errorf("php.settings: %q is not a valid php.ini key", k))
		}
		if strings.ContainsAny(v, "\n\r\"") {
			errs = append(errs, fmt.Errorf("php.settings.%s: value must be one line without double quotes", k))
		}
	}
	for _, e := range c.PHP.Extensions {
		if !ValidINIKey(e) {
			errs = append(errs, fmt.Errorf("php.extensions: %q is not a valid extension name", e))
		}
	}
	if c.Mirror.Interval < 1 {
		errs = append(errs, errors.New("mirror.interval_seconds must be at least 1"))
	}
	return errors.Join(errs...)
}

// ValidPort accepts ports an unprivileged Android app can bind.
func ValidPort(p int) error {
	if p < 1024 || p > 65535 {
		return fmt.Errorf("port %d is outside 1024-65535 (Android apps cannot bind ports below 1024 without root)", p)
	}
	return nil
}

var siteNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func ValidSiteName(s string) bool { return siteNameRe.MatchString(s) }

var iniKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,79}$`)

// ValidINIKey accepts php.ini directive and extension names (e.g. opcache.enable, pdo_pgsql).
func ValidINIKey(s string) bool { return iniKeyRe.MatchString(s) }

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// WriteFileAtomic writes via a temp file + rename so readers never see partial files.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
