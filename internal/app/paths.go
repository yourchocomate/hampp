//go:build unix

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/service"
)

// Writable reports whether a directory exists and this user can create files in
// it. Several Termux installs have a $PREFIX/tmp that the user cannot write
// (for example after running Termux as root), which breaks PHP and other tools
// in ways that are hard to read from their error messages.
func Writable(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	f, err := os.CreateTemp(dir, ".hampp-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// pathChecks verifies every directory hampp and the daemons write to.
func (a *App) pathChecks(_ context.Context) []Check {
	type target struct {
		path  string
		label string
		fix   string
	}
	targets := []target{
		{a.Paths.Config, "hampp config", ""},
		{a.Paths.Conf(), "generated configs", ""},
		{a.Paths.Run(), "pid files and sockets", ""},
		{a.Paths.Log(), "logs", ""},
		{a.Paths.Tmp(), "temporary files (PHP sessions, OPcache)", ""},
		// Termux's own tmp: PHP, apt and many packages fall back to it.
		{filepath.Join(a.Env.Prefix, "tmp"), "Termux's temporary directory",
			"Other Termux tools need it too. Restart Termux; if it stays unwritable, check its owner with `ls -ld $PREFIX/tmp` (running Termux as root can leave root-owned files behind)."},
	}
	if a.Cfg.DB.Engine == config.DBMariaDB {
		targets = append(targets,
			target{filepath.Dir(service.MySQLSocket(a.Env.Prefix)), "MariaDB socket directory", ""},
			target{service.DataDir(a.Env.Prefix), "MariaDB data directory", "pkg reinstall mariadb"},
		)
	}
	if a.Initialized {
		targets = append(targets, target{a.Cfg.Web.Root, "web root", "hampp config set web.root ~/www"})
	}

	var out []Check
	for _, t := range targets {
		if err := Writable(t.path); err != nil {
			fix := t.fix
			if fix == "" {
				fix = "hampp init   (recreates hampp's own directories)"
			}
			out = append(out, Check{Fail, fmt.Sprintf("Cannot write to %s (%s): %v", a.Paths.Short(t.path), t.label, err), fix})
		}
	}
	if len(out) == 0 {
		out = append(out, Check{OK, "All directories hampp writes to are usable", ""})
	}
	return out
}

// phpTempPaths are the settings PHP writes through. Their defaults point at
// $PREFIX/tmp (or /tmp), which is not writable everywhere.
var phpTempPaths = []string{"sys_temp_dir", "upload_tmp_dir", "session.save_path", "opcache.lockfile_path", "opcache.file_cache"}

// phpPathChecks asks PHP for the directories it will actually use and verifies
// each one, so a failure names the real path instead of a generic error like
// "Cannot create lock - Permission denied".
func (a *App) phpPathChecks(ctx context.Context) []Check {
	vals, err := a.PHPGet(ctx, phpTempPaths)
	if err != nil {
		return nil
	}
	var out []Check
	for _, k := range phpTempPaths {
		dir := vals[k][0]
		if dir == "" || dir == "(empty)" || dir == "(unknown)" {
			continue
		}
		if err := Writable(dir); err != nil {
			out = append(out, Check{Fail, fmt.Sprintf("PHP setting %s points at %s, which is not usable: %v", k, dir, err),
				"hampp reload   (re-points PHP at hampp's own directory), or set it in `hampp php edit`"})
		}
	}
	return out
}
