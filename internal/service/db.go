//go:build unix

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yourchocomate/hampp/internal/proc"
	"github.com/yourchocomate/hampp/internal/render"
)

// MariaDB runs mariadbd against the package datadir ($PREFIX/var/lib/mysql),
// which the mariadb postinst initialises with a passwordless root.
type MariaDB struct{}

func (MariaDB) Name() string       { return "db" }
func (MariaDB) Title() string      { return "mariadb" }
func (MariaDB) Packages() []string { return []string{"mariadb"} }

func MySQLSocket(prefix string) string { return filepath.Join(prefix, "var", "run", "mysqld.sock") }
func DataDir(prefix string) string     { return filepath.Join(prefix, "var", "lib", "mysql") }

func (m MariaDB) Configure(c *Ctx) error {
	// $PREFIX/var/run does not exist on every Termux install, and mariadbd will
	// not create it: "Bind on unix socket: No such file or directory".
	if err := os.MkdirAll(filepath.Dir(c.Data.MySQLSock), 0o755); err != nil {
		return fmt.Errorf("socket directory: %w", err)
	}
	if _, err := render.WriteIfChanged("my.cnf.tmpl", conf(c, "my.cnf"), c.Data, 0o600); err != nil {
		return err
	}
	if c.Creds.DBRootPassword != "" {
		d := c.Data
		d.RootPassword = c.Creds.DBRootPassword
		if _, err := render.WriteIfChanged("client.cnf.tmpl", conf(c, "client.cnf"), d, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (m MariaDB) Start(ctx context.Context, c *Ctx) error {
	pidFile := run(c, "mariadb.pid")
	if proc.RunningAs(pidFile, dbProc...) > 0 {
		return nil
	}
	leftovers{conf: conf(c, "my.cnf")}.clean()
	if err := checkPort("127.0.0.1", c.Data.DBPort, "mariadb; is the termux-services `mysqld` running? try: sv down mysqld"); err != nil {
		return err
	}
	if err := m.ensureDataDir(ctx, c); err != nil {
		return err
	}
	// Android kills servers without letting them clean up. The port check above
	// proved no server is listening, so a socket file left here is stale.
	_ = os.Remove(c.Data.MySQLSock)
	// --defaults-file must be the first argument.
	if _, err := proc.Spawn(c.Env.Bin("mariadbd"), []string{"--defaults-file=" + conf(c, "my.cnf")}, nil, logf(c, "mariadb.log"), ""); err != nil {
		return err
	}
	wctx, cancel := waitCtx(ctx, 60*time.Second)
	defer cancel()
	if err := proc.WaitDial(wctx, "unix", c.Data.MySQLSock); err != nil {
		return failure(err, logf(c, "mariadb.log"))
	}
	return nil
}

// ensureDataDir mirrors the package postinst in case the datadir was removed.
func (m MariaDB) ensureDataDir(ctx context.Context, c *Ctx) error {
	dd := DataDir(c.Env.Prefix)
	if _, err := os.Stat(filepath.Join(dd, "mysql")); err == nil {
		return nil
	}
	_, err := c.R.Output(ctx, "", c.Env.Bin("mariadb-install-db"),
		"--user=root", "--auth-root-authentication-method=normal", "--datadir="+dd)
	if err != nil {
		return fmt.Errorf("initialising %s: %w", dd, err)
	}
	return nil
}

func (m MariaDB) Stop(_ context.Context, c *Ctx) error {
	err := stopPID(run(c, "mariadb.pid"), syscall.SIGTERM, 60*time.Second, dbProc...)
	leftovers{conf: conf(c, "my.cnf")}.clean()
	return err
}

func (m MariaDB) Status(c *Ctx) Status {
	return pidStatus(m, run(c, "mariadb.pid"), fmt.Sprintf(":%d", c.Data.DBPort), dbProc...)
}

func (MariaDB) Logs(c *Ctx) []string { return []string{logf(c, "mariadb.log")} }

// ClientArgs returns mariadb client args authenticated as root via hampp's client.cnf.
// Before `db setup` has set a root password, root still logs in without one.
func ClientArgs(c *Ctx) []string {
	if c.Creds.DBRootPassword != "" {
		return []string{"--defaults-file=" + conf(c, "client.cnf")}
	}
	return []string{"--no-defaults", "--protocol=socket", "--socket=" + c.Data.MySQLSock, "-u", "root"}
}
