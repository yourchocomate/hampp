package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default("/data/home").Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"privileged port": func(c *Config) { c.Web.Port = 80 },
		"port too high":   func(c *Config) { c.Web.Port = 70000 },
		"duplicate port":  func(c *Config) { c.DB.Port = c.Web.Port },
		"bad server":      func(c *Config) { c.Web.Server = "caddy" },
		"bad db":          func(c *Config) { c.DB.Engine = "postgres" },
		"relative root":   func(c *Config) { c.Web.Root = "www" },
		"bad link name":   func(c *Config) { c.Sites.Links = []Link{{Name: "My_App", Path: "/x"}} },
		"dup link":        func(c *Config) { c.Sites.Links = []Link{{Name: "a", Path: "/x"}, {Name: "a", Path: "/y"}} },
		"zero interval":   func(c *Config) { c.Mirror.Interval = 0 },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			c := Default("/h")
			mut(&c)
			if c.Validate() == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestPrivilegedPortMessageExplainsAndroid(t *testing.T) {
	err := ValidPort(80)
	if err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadMissingIsNotInitialized(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.toml"), "/h")
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("got %v", err)
	}
}

func TestSaveLoadRoundTripAndTilde(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	c := Default(dir)
	c.Web.Server = WebNginx
	c.Web.HTTPS = true
	c.Sites.Links = []Link{{Name: "api", Path: filepath.Join(dir, "api"), Root: "public"}}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("perm %v", st.Mode().Perm())
	}
	got, err := Load(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Web.Server != WebNginx || !got.Web.HTTPS || len(got.Sites.Links) != 1 || got.Sites.Links[0].Root != "public" {
		t.Fatalf("round trip lost data: %+v", got)
	}

	// Hand-edited files may use ~.
	b, _ := os.ReadFile(p)
	b = []byte(strings.Replace(string(b), "root = '"+filepath.Join(dir, "www")+"'", "root = '~/sites'", 1))
	os.WriteFile(p, b, 0o600)
	got, err = Load(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Web.Root != filepath.Join(dir, "sites") {
		t.Fatalf("tilde not expanded: %s\n%s", got.Web.Root, b)
	}
}

func TestCredentials(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	c, err := LoadCredentials(p)
	if err != nil || c.DBPassword != "" {
		t.Fatal(c, err)
	}
	c.DBPassword = NewPassword()
	if len(c.DBPassword) < 20 || strings.ContainsAny(c.DBPassword, `'"\ `) {
		t.Fatalf("unsafe password %q", c.DBPassword)
	}
	if err := SaveCredentials(p, c); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatal("credentials must be 0600")
	}
	got, _ := LoadCredentials(p)
	if got.DBPassword != c.DBPassword {
		t.Fatal("round trip")
	}
}
