// Package pkgmgr installs Termux packages with apt (via pkg) or pacman.
package pkgmgr

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yourchocomate/hampp/internal/sys"
)

const (
	Apt    = "apt"
	Pacman = "pacman"
)

type Manager struct {
	Kind   string
	Prefix string
	R      sys.Runner
}

// Detect picks the package manager. TERMUX_APP_PACKAGE_MANAGER is only exported
// by Termux app >= 0.119, so fall back to probing binaries like termux-tools does.
func Detect(prefix string, r sys.Runner) Manager {
	m := Manager{Kind: Apt, Prefix: prefix, R: r}
	for _, k := range []string{"TERMUX_APP__PACKAGE_MANAGER", "TERMUX_APP_PACKAGE_MANAGER"} {
		switch os.Getenv(k) {
		case Apt:
			return m
		case Pacman:
			m.Kind = Pacman
			return m
		}
	}
	if !exists(filepath.Join(prefix, "bin", "apt")) && exists(filepath.Join(prefix, "bin", "pacman")) {
		m.Kind = Pacman
	}
	return m
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Installed reports whether a package is installed.
func (m Manager) Installed(ctx context.Context, pkg string) bool {
	var err error
	if m.Kind == Pacman {
		_, err = m.R.Output(ctx, "", "pacman", "-Q", pkg)
	} else {
		var out string
		out, err = m.R.Output(ctx, "", "dpkg-query", "-W", "-f=${Status}", pkg)
		if err == nil && !strings.Contains(out, "install ok installed") {
			return false
		}
	}
	return err == nil
}

// Missing filters pkgs down to those not installed.
func (m Manager) Missing(ctx context.Context, pkgs ...string) []string {
	var out []string
	for _, p := range pkgs {
		if !m.Installed(ctx, p) {
			out = append(out, p)
		}
	}
	return out
}

// InstallArgs returns the command used to install pkgs, for display and execution.
func (m Manager) InstallArgs(pkgs ...string) []string {
	if m.Kind == Pacman {
		return append([]string{"pacman", "-S", "--needed", "--noconfirm"}, pkgs...)
	}
	// pkg picks a mirror and runs `apt update` when needed, then passes -y through to apt.
	return append([]string{"pkg", "install", "-y"}, pkgs...)
}

// Install installs pkgs, streaming output to w.
func (m Manager) Install(ctx context.Context, w io.Writer, pkgs ...string) error {
	if len(pkgs) == 0 {
		return nil
	}
	args := m.InstallArgs(pkgs...)
	return m.R.Stream(ctx, w, args[0], args[1:]...)
}

func (m Manager) Uninstall(ctx context.Context, w io.Writer, pkgs ...string) error {
	if m.Kind == Pacman {
		return m.R.Stream(ctx, w, "pacman", append([]string{"-R", "--noconfirm"}, pkgs...)...)
	}
	return m.R.Stream(ctx, w, "pkg", append([]string{"uninstall", "-y"}, pkgs...)...)
}

// Version returns the installed version of pkg, or "".
func (m Manager) Version(ctx context.Context, pkg string) string {
	if m.Kind == Pacman {
		out, err := m.R.Output(ctx, "", "pacman", "-Q", pkg)
		if f := strings.Fields(out); err == nil && len(f) == 2 {
			return f[1]
		}
		return ""
	}
	out, err := m.R.Output(ctx, "", "dpkg-query", "-W", "-f=${Version}", pkg)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

var nodePkgRe = regexp.MustCompile(`^nodejs-(\d+)$`)

// NodeMajors lists nodejs-NN packages known to the package index (TUR).
func (m Manager) NodeMajors(ctx context.Context) ([]int, error) {
	var out string
	var err error
	if m.Kind == Pacman {
		out, err = m.R.Output(ctx, "", "pacman", "-Ssq", "^nodejs-[0-9]+$")
	} else {
		out, err = m.R.Output(ctx, "", "apt-cache", "search", "--names-only", "^nodejs-[0-9]+$")
	}
	if err != nil {
		return nil, err
	}
	return ParseNodeMajors(out), nil
}

// ParseNodeMajors extracts majors from `apt-cache search` or `pacman -Ssq` output.
func ParseNodeMajors(out string) []int {
	seen := map[int]bool{}
	var majors []int
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		// apt prints name/repo, pacman prints repo/name.
		for _, name := range strings.Split(f[0], "/") {
			if mm := nodePkgRe.FindStringSubmatch(name); mm != nil {
				if n := atoi(mm[1]); !seen[n] {
					seen[n] = true
					majors = append(majors, n)
				}
			}
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(majors)))
	return majors
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
