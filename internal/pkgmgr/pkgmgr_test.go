package pkgmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yourchocomate/hampp/internal/sys"
)

func TestParseNodeMajors(t *testing.T) {
	apt := `nodejs-12/tur-packages 12.18.3 aarch64
nodejs-16 - Open Source, cross-platform JavaScript runtime environment
nodejs-24 - Open Source, cross-platform JavaScript runtime environment
nodejs-20 - Open Source, cross-platform JavaScript runtime environment
nodejs-lts - not a numbered package
nodejs-22 - Open Source`
	if got := ParseNodeMajors(apt); !reflect.DeepEqual(got, []int{24, 22, 20, 16, 12}) {
		t.Fatalf("apt: %v", got)
	}
	pacman := "tur/nodejs-18\ntur/nodejs-20\n"
	if got := ParseNodeMajors(pacman); !reflect.DeepEqual(got, []int{20, 18}) {
		t.Fatalf("pacman: %v", got)
	}
}

func TestDetectAndArgs(t *testing.T) {
	t.Setenv("TERMUX_APP_PACKAGE_MANAGER", "")
	t.Setenv("TERMUX_APP__PACKAGE_MANAGER", "")
	pre := t.TempDir()
	os.MkdirAll(filepath.Join(pre, "bin"), 0o755)
	os.WriteFile(filepath.Join(pre, "bin", "pacman"), nil, 0o755)
	m := Detect(pre, &sys.Fake{})
	if m.Kind != Pacman {
		t.Fatal("pacman-only prefix")
	}
	if got := strings.Join(m.InstallArgs("php"), " "); got != "pacman -S --needed --noconfirm php" {
		t.Fatal(got)
	}
	os.WriteFile(filepath.Join(pre, "bin", "apt"), nil, 0o755)
	m = Detect(pre, &sys.Fake{})
	if m.Kind != Apt || strings.Join(m.InstallArgs("php", "php-fpm"), " ") != "pkg install -y php php-fpm" {
		t.Fatal(m.Kind, m.InstallArgs("php"))
	}
	t.Setenv("TERMUX_APP_PACKAGE_MANAGER", "pacman")
	if Detect(pre, &sys.Fake{}).Kind != Pacman {
		t.Fatal("env wins")
	}
}

func TestInstalledAndMissing(t *testing.T) {
	f := &sys.Fake{Responses: map[string]sys.FakeResult{
		"dpkg-query -W -f=${Status} php":      {Out: "install ok installed"},
		"dpkg-query -W -f=${Status} mariadb":  {Out: "deinstall ok config-files"},
		"dpkg-query -W -f=${Status} apache2*": {Err: errors.New("no packages found")},
	}}
	m := Manager{Kind: Apt, R: f}
	got := m.Missing(context.Background(), "php", "mariadb", "apache2")
	if !reflect.DeepEqual(got, []string{"mariadb", "apache2"}) {
		t.Fatal(got)
	}
}
