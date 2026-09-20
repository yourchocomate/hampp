//go:build unix

package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/yourchocomate/hampp/internal/app"
	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/paths"
	"github.com/yourchocomate/hampp/internal/service"
	"github.com/yourchocomate/hampp/internal/site"
	"github.com/yourchocomate/hampp/internal/termux"
)

var update = flag.Bool("update", false, "rewrite TUI snapshots in testdata/")

// snapshotModel builds a dashboard with fixed data so screens can be reviewed
// as plain text (testdata/*.txt) and guarded against layout regressions.
func snapshotModel(g glyphs) *model {
	home := "/data/data/com.termux/files/home"
	cfg := config.Default(home)
	cfg.Web.HTTPS = true
	a := &app.App{
		Env:   termux.Env{Prefix: termux.DefaultPrefix, Home: home, IsTermux: true, SDK: 34, Model: "Pixel 7", APKRelease: "F_DROID"},
		Paths: paths.New(home),
		Cfg:   cfg,
	}
	since := time.Now().Add(-(2*time.Hour + 14*time.Minute + 5*time.Second))
	return &model{
		a: a, g: g, w: 44, h: 40, out: &syncBuffer{},
		statuses: []service.Status{
			{Name: "db", Title: "mariadb", Running: true, Since: since, Detail: ":3306"},
			{Name: "php", Title: "php-fpm", Running: true, Since: since, Detail: "socket"},
			{Name: "web", Title: "apache", Running: true, Since: since, Detail: ":8080 :8443"},
			{Name: "code", Title: "code-server", Detail: ":8090"},
			{Name: "mirror", Title: "mirror", Detail: "~/storage/shared/www"},
		},
		sites: []site.Site{
			{Host: "localhost", DocRoot: home + "/www", Default: true},
			{Name: "blog", Host: "blog.localhost", DocRoot: home + "/www/blog/public"},
			{Name: "api", Host: "api.localhost", DocRoot: home + "/projects/api/public", Linked: true},
		},
		nodeSt: app.NodeState{Installed: []int{22, 20}, Active: 22},
		remote: []int{24, 22, 20, 18, 16, 12},
		phpVals: map[string][2]string{
			"memory_limit": {"256M", "128M"}, "upload_max_filesize": {"128M", "2M"}, "post_max_size": {"128M", "8M"},
			"max_execution_time": {"300", "0"}, "display_errors": {"1", "1"}, "date.timezone": {"Asia/Dhaka", "Asia/Dhaka"}, "opcache.enable": {"1", "1"},
		},
		phpExts: []app.Ext{
			{Name: "core", State: app.ExtBuiltIn}, {Name: "mbstring", State: app.ExtBuiltIn},
			{Name: "gd", Package: "php-gd", State: app.ExtEnabled, Description: "gd module for PHP"},
			{Name: "apcu", Package: "php-apcu", State: app.ExtInstalled, Description: "APCu - APC User Cache"},
			{Name: "imagick", Package: "php-imagick", State: app.ExtAvailable, Description: "The Imagick PHP extension"},
			{Name: "redis", Package: "php-redis", State: app.ExtAvailable, Description: "PHP extension for interfacing with Redis"},
		},
		doctor: []app.Check{
			{Level: app.OK, Message: "Termux 0.118.3 (F-Droid)"},
			{Level: app.OK, Message: "Packages installed: mariadb php php-fpm composer apache2"},
			{Level: app.Warn, Message: "Android 14 may kill background servers (phantom process limit: 32 across all apps)", Fix: strings.Join(termux.PhantomKillerFix(34), "\n")},
			{Level: app.OK, Message: "Web root ~/www is in Termux storage"},
		},
	}
}

func TestScreenSnapshots(t *testing.T) {
	screens := map[string]screen{"dashboard": scrDash, "php": scrPHP, "node": scrNode, "doctor": scrDoctor, "help": scrHelp}
	for _, mode := range []struct {
		name string
		g    glyphs
	}{{"", unicodeGlyphs}, {"-ascii", asciiGlyphs}} {
		for name, scr := range screens {
			m := snapshotModel(mode.g)
			m.scr = scr
			got := ansi.Strip(m.render())
			for i, l := range strings.Split(got, "\n") {
				if w := ansi.StringWidth(l); w > 44 {
					t.Errorf("%s%s line %d is %d cells wide (Termux portrait is ~44): %q", name, mode.name, i, w, l)
				}
			}
			file := filepath.Join("testdata", name+mode.name+".txt")
			if *update {
				os.WriteFile(file, []byte(got+"\n"), 0o644)
				continue
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v (run: go test ./internal/tui -update)", err)
			}
			// Uptime and the .nvmrc hint depend on the environment; compare the rest.
			if norm(string(want)) != norm(got+"\n") {
				t.Errorf("%s changed; review and run: go test ./internal/tui -update\n%s", file, got)
			}
		}
	}
}

func norm(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, ".nvmrc") || strings.Contains(l, ".node-version") {
			continue
		}
		out = append(out, strings.TrimRight(l, " "))
	}
	return strings.Join(out, "\n")
}

func TestLifecycleTargetFollowsSelection(t *testing.T) {
	m := snapshotModel(unicodeGlyphs)
	// Services panel focused: s/x/r act on the highlighted service.
	m.focus, m.svcIdx = 0, 1
	if got := m.target(false); len(got) != 1 || got[0] != "php" {
		t.Fatalf("target: %v", got)
	}
	if m.targetLabel() != "php" {
		t.Fatal(m.targetLabel())
	}
	// Shift applies to everything, whatever is selected.
	if m.target(true) != nil {
		t.Fatal("shift must target all services")
	}
	// On the Sites panel there is no service to single out.
	m.focus = 1
	if m.target(false) != nil || m.targetLabel() != "all" {
		t.Fatal("sites panel targets all services")
	}
	m.focus, m.statuses = 0, nil
	if m.target(false) != nil {
		t.Fatal("no services: target all")
	}
}

func TestFooterNamesTheTarget(t *testing.T) {
	m := snapshotModel(unicodeGlyphs)
	m.focus, m.svcIdx = 0, 2 // web
	out := ansi.Strip(m.render())
	for _, want := range []string{"s start web", "x stop web", "r reload web", "S X R all"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing %q:\n%s", want, out)
		}
	}
}
