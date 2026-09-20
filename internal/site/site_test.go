package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourchocomate/hampp/internal/config"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"blog":                  "blog",
		"My Blog":               "my-blog",
		"my_app.v2":             "my-app-v2",
		"--weird--":             "weird",
		"Ünïcode":               "n-code",
		"___":                   "",
		strings.Repeat("a", 70): strings.Repeat("a", 63),
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
		if got := Sanitize(in); got != "" && !config.ValidSiteName(got) {
			t.Errorf("Sanitize(%q) = %q is not a valid DNS label", in, got)
		}
	}
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDetectDocRoot(t *testing.T) {
	d := t.TempDir()
	plain := mkdir(t, d, "plain")
	laravel := mkdir(t, d, "laravel")
	mkdir(t, laravel, "public")
	drupal := mkdir(t, d, "drupal")
	mkdir(t, drupal, "web")
	if DetectDocRoot(plain) != plain {
		t.Error("plain")
	}
	if DetectDocRoot(laravel) != filepath.Join(laravel, "public") {
		t.Error("laravel")
	}
	if DetectDocRoot(drupal) != filepath.Join(drupal, "web") {
		t.Error("drupal")
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "blog", "public")
	mkdir(t, root, "Shop App")
	mkdir(t, root, "shop-app") // collides with "Shop App"
	mkdir(t, root, ".git")
	os.WriteFile(filepath.Join(root, "index.php"), nil, 0o644)
	ext := mkdir(t, t.TempDir(), "api")
	mkdir(t, ext, "public")

	sites, warnings := Discover(root, []config.Link{{Name: "api", Path: ext}, {Name: "blog", Path: ext, Root: "."}})

	if !sites[0].Default || sites[0].Host != "localhost" || sites[0].DocRoot != root {
		t.Fatalf("default site first: %+v", sites[0])
	}
	byName := map[string]Site{}
	for _, s := range sites[1:] {
		byName[s.Name] = s
	}
	if _, ok := byName["git"]; ok {
		t.Error("hidden folders must be skipped")
	}
	if s := byName["api"]; !s.Linked || s.DocRoot != filepath.Join(ext, "public") || s.Host != "api.localhost" {
		t.Errorf("api: %+v", s)
	}
	if s := byName["blog"]; !s.Linked || s.DocRoot != ext {
		t.Errorf("link must override parked folder with explicit root: %+v", s)
	}
	if _, ok := byName["shop-app"]; !ok {
		t.Error("shop-app missing")
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "shop-app") || !strings.Contains(joined, "overrides") {
		t.Errorf("expected collision and override warnings, got:\n%s", joined)
	}
	for i := 2; i < len(sites); i++ {
		if sites[i-1].Name > sites[i].Name {
			t.Error("sites must be sorted")
		}
	}
}

func TestHostsAndURL(t *testing.T) {
	s := Site{Name: "blog", Host: "blog.localhost"}
	if got := strings.Join(s.Hosts(), ","); got != "blog.localhost,localhost,127.0.0.1,::1" {
		t.Error(got)
	}
	if s.URL(8443, true) != "https://blog.localhost:8443/" {
		t.Error(s.URL(8443, true))
	}
	if (Site{Default: true, Host: "localhost"}).CertName() != "localhost" {
		t.Error("default cert name")
	}
}

func TestOnSharedStorage(t *testing.T) {
	for _, p := range []string{"/sdcard/www", "/storage/emulated/0/www", "/data/data/com.termux/files/home/storage/shared/www"} {
		if !OnSharedStorage(p) {
			t.Error(p)
		}
	}
	if OnSharedStorage("/data/data/com.termux/files/home/www") {
		t.Error("home is not shared storage")
	}
}
