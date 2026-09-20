// Package paths computes hampp's XDG-style directory layout.
package paths

import (
	"os"
	"path/filepath"
)

// Paths holds every location hampp writes to. None of them is package-owned.
type Paths struct {
	Home   string
	Config string // ~/.config/hampp
	Data   string // ~/.local/share/hampp
	State  string // ~/.local/state/hampp
}

// New resolves the layout, honouring XDG_* variables.
func New(home string) Paths {
	xdg := func(key, def string) string {
		if v := os.Getenv(key); v != "" && filepath.IsAbs(v) {
			return v
		}
		return filepath.Join(home, def)
	}
	return Paths{
		Home:   home,
		Config: filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "hampp"),
		Data:   filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "hampp"),
		State:  filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), "hampp"),
	}
}

func (p Paths) ConfigFile() string      { return filepath.Join(p.Config, "config.toml") }
func (p Paths) CredentialsFile() string { return filepath.Join(p.Config, "credentials.toml") }
func (p Paths) Conf() string            { return filepath.Join(p.Data, "conf") }
func (p Paths) Adminer() string         { return filepath.Join(p.Data, "adminer") }
func (p Paths) CA() string              { return filepath.Join(p.Data, "ca") }
func (p Paths) Certs() string           { return filepath.Join(p.Data, "certs") }
func (p Paths) NodeCurrent() string     { return filepath.Join(p.Data, "node", "current") }
func (p Paths) CodeServerData() string  { return filepath.Join(p.Data, "code-server") }
func (p Paths) Run() string             { return filepath.Join(p.State, "run") }
func (p Paths) Log() string             { return filepath.Join(p.State, "log") }
func (p Paths) Tmp() string             { return filepath.Join(p.State, "tmp") }

// PHPSocket is the php-fpm socket. It lives in hampp's state dir, not $TMPDIR,
// because Termux wipes $PREFIX/tmp on app restart.
func (p Paths) PHPSocket() string { return filepath.Join(p.Run(), "php-fpm.sock") }

// Ensure creates every directory with private permissions.
func (p Paths) Ensure() error {
	for _, d := range []string{p.Config, p.Conf(), p.Adminer(), p.CA(), p.Certs(),
		filepath.Dir(p.NodeCurrent()), p.Run(), p.Log(), p.Tmp()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// Short replaces the home directory with ~ for display.
func (p Paths) Short(path string) string {
	if rel, err := filepath.Rel(p.Home, path); err == nil && rel != ".." && !filepath.IsAbs(rel) && (len(rel) < 3 || rel[:3] != "../") {
		if rel == "." {
			return "~"
		}
		return "~/" + rel
	}
	return path
}
