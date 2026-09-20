//go:build unix

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/service"
)

const dbUser = "hampp"

func (a *App) mariadb(ctx context.Context, sql string, args ...string) (string, error) {
	login, err := a.rootLogin(ctx)
	if err != nil {
		return "", err
	}
	return a.R.Output(ctx, sql, a.Env.Bin("mariadb"), append(login, args...)...)
}

// rootLogin finds working root credentials: the saved password first, then the
// passwordless root the mariadb postinst creates. Trying both keeps hampp able
// to log in even if a previous `db setup` was interrupted halfway.
func (a *App) rootLogin(ctx context.Context) ([]string, error) {
	c, _ := a.Ctx()
	var candidates [][]string
	if a.Creds.DBRootPassword != "" {
		if err := (service.MariaDB{}).Configure(c); err != nil {
			return nil, err
		}
		candidates = append(candidates, service.ClientArgs(c))
	}
	c.Creds.DBRootPassword = ""
	candidates = append(candidates, service.ClientArgs(c))
	var lastErr error
	for _, args := range candidates {
		if _, lastErr = a.R.Output(ctx, "SELECT 1;", a.Env.Bin("mariadb"), args...); lastErr == nil {
			return args, nil
		}
	}
	return nil, fmt.Errorf("cannot log in to MariaDB as root; if you set a root password yourself, put it in %s as db_root_password: %w", a.Paths.CredentialsFile(), lastErr)
}

// DBSetup creates the `hampp` user and replaces the passwordless root created
// by the mariadb postinst with a random password. Adminer 6 refuses empty
// passwords, and a passwordless root would be open to every app on the phone.
func (a *App) DBSetup(ctx context.Context) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	if a.Cfg.DB.Engine != config.DBMariaDB {
		return errors.New("db setup only applies to MariaDB (db.engine = mariadb)")
	}
	if err := a.Start(ctx, []string{"db"}); err != nil {
		return err
	}
	login, err := a.rootLogin(ctx)
	if err != nil {
		return err
	}
	if a.Creds.DBPassword == "" {
		a.Creds.DBPassword = config.NewPassword()
	}
	if a.Creds.DBRootPassword == "" {
		a.Creds.DBRootPassword = config.NewPassword()
	}
	a.Creds.DBUser = dbUser
	// Save before changing anything so the new passwords can never be lost.
	if err := a.SaveCreds(); err != nil {
		return err
	}
	sql := SetupSQL(dbUser, a.Creds.DBPassword, a.Creds.DBRootPassword)
	if _, err := a.R.Output(ctx, sql, a.Env.Bin("mariadb"), login...); err != nil {
		return err
	}
	c, _ := a.Ctx()
	if err := (service.MariaDB{}).Configure(c); err != nil {
		return err
	}
	if err := a.writeMyCnf(); err != nil {
		a.printf("! %v\n", err)
	}
	a.printf("MariaDB user %q created; root now has a password (see `hampp db info`).\n", dbUser)
	return nil
}

// SetupSQL is the idempotent account setup batch. The cleanup statements are
// the ones MariaDB's mysql_secure_installation runs: remove anonymous users and
// root accounts reachable from other hosts (hostname-based accounts are also
// ignored under skip-name-resolve, so ALTER USER would fail on them).
func SetupSQL(user, pw, rootPw string) string {
	var b strings.Builder
	for _, h := range []string{"localhost", "127.0.0.1"} {
		fmt.Fprintf(&b, "CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s';\n", user, h, pw)
		fmt.Fprintf(&b, "ALTER USER '%s'@'%s' IDENTIFIED BY '%s';\n", user, h, pw)
		fmt.Fprintf(&b, "GRANT ALL PRIVILEGES ON *.* TO '%s'@'%s' WITH GRANT OPTION;\n", user, h)
	}
	b.WriteString("DELETE FROM mysql.global_priv WHERE User='';\n")
	b.WriteString("DELETE FROM mysql.global_priv WHERE User='root' AND Host NOT IN ('localhost', '127.0.0.1', '::1');\n")
	b.WriteString("FLUSH PRIVILEGES;\n")
	for _, h := range []string{"localhost", "127.0.0.1", "::1"} {
		fmt.Fprintf(&b, "ALTER USER IF EXISTS 'root'@'%s' IDENTIFIED BY '%s';\n", h, rootPw)
	}
	b.WriteString("FLUSH PRIVILEGES;\n")
	return b.String()
}

var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// DBCreate creates a utf8mb4 database.
func (a *App) DBCreate(ctx context.Context, name string) error {
	if !dbNameRe.MatchString(name) {
		return fmt.Errorf("database name %q: use letters, digits and _ (max 64)", name)
	}
	if err := a.RequireInit(); err != nil {
		return err
	}
	if a.Cfg.DB.Engine != config.DBMariaDB {
		return errors.New("db create needs MariaDB; with SQLite just use a file such as ~/www/app/database.sqlite")
	}
	if err := a.Start(ctx, []string{"db"}); err != nil {
		return err
	}
	_, err := a.mariadb(ctx, fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", name))
	if err == nil {
		a.printf("Database %s ready.\n", name)
	}
	return err
}

// DBInfo returns connection details for the user's apps.
func (a *App) DBInfo() []string {
	switch a.Cfg.DB.Engine {
	case config.DBSQLite:
		pw := a.Creds.AdminerSQLitePwd
		return []string{
			"Engine:   SQLite (no server)",
			"Database: any file, e.g. ~/www/myapp/database.sqlite",
			"Laravel:  DB_CONNECTION=sqlite",
			"Adminer:  http://localhost:" + fmt.Sprint(a.Cfg.Web.Port) + "/adminer/  (System: SQLite, password: " + pw + ")",
		}
	case config.DBNone:
		return []string{"No database configured (db.engine = none)."}
	}
	sock := service.MySQLSocket(a.Env.Prefix)
	user, pw := a.Creds.DBUser, a.Creds.DBPassword
	if user == "" {
		user, pw = "root", "(none yet; run `hampp db setup`)"
	}
	return []string{
		"Engine:   MariaDB",
		fmt.Sprintf("Host:     127.0.0.1  Port: %d", a.Cfg.DB.Port),
		"Socket:   " + sock,
		"User:     " + user,
		"Password: " + pw,
		"Laravel:  DB_HOST=127.0.0.1 DB_PORT=" + fmt.Sprint(a.Cfg.DB.Port) + " DB_USERNAME=" + user,
		"Adminer:  http://localhost:" + fmt.Sprint(a.Cfg.Web.Port) + "/adminer/",
		"Shell:    mariadb   (after `hampp db setup`, logs in via ~/.my.cnf)",
	}
}

const myCnfMarker = "# managed by hampp"

// writeMyCnf lets the `mariadb` command log in as the hampp user. An existing
// ~/.my.cnf that hampp did not write is left untouched.
func (a *App) writeMyCnf() error {
	p := filepath.Join(a.Env.Home, ".my.cnf")
	if b, err := os.ReadFile(p); err == nil && !strings.Contains(string(b), myCnfMarker) {
		return fmt.Errorf("%s exists and was not written by hampp; leaving it alone", p)
	}
	content := fmt.Sprintf("%s\n[client]\nuser = %s\npassword = \"%s\"\nsocket = %s\n",
		myCnfMarker, a.Creds.DBUser, a.Creds.DBPassword, service.MySQLSocket(a.Env.Prefix))
	return config.WriteFileAtomic(p, []byte(content), 0o600)
}
