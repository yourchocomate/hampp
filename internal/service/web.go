//go:build unix

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/yourchocomate/hampp/internal/proc"
	"github.com/yourchocomate/hampp/internal/render"
)

// Apache runs httpd with the worker MPM and hands PHP to php-fpm.
type Apache struct{}

func (Apache) Name() string       { return "web" }
func (Apache) Title() string      { return "apache" }
func (Apache) Packages() []string { return []string{"apache2"} }

func (a Apache) Configure(c *Ctx) error {
	mods, err := render.ResolveApache(c.Env.Prefix, c.Data.HTTPS)
	if err != nil {
		return err
	}
	c.Data.Apache = mods
	_, err = render.WriteIfChanged("httpd.conf.tmpl", conf(c, "httpd.conf"), c.Data, 0o600)
	return err
}

func (a Apache) Test(ctx context.Context, c *Ctx) error {
	_, err := c.R.Output(ctx, "", c.Env.Bin("httpd"), "-t", "-f", conf(c, "httpd.conf"))
	return err
}

func (a Apache) Start(ctx context.Context, c *Ctx) error {
	if proc.Running(run(c, "httpd.pid")) > 0 {
		return nil
	}
	if err := webPorts(c); err != nil {
		return err
	}
	if _, err := c.R.Output(ctx, "", c.Env.Bin("httpd"), "-f", conf(c, "httpd.conf"), "-k", "start"); err != nil {
		return failure(err, logf(c, "apache-error.log"))
	}
	return waitWeb(ctx, c, logf(c, "apache-error.log"))
}

func (a Apache) Stop(_ context.Context, c *Ctx) error {
	return stopPID(run(c, "httpd.pid"), syscall.SIGTERM, 10*time.Second)
}

func (a Apache) Reload(ctx context.Context, c *Ctx) error {
	if proc.Running(run(c, "httpd.pid")) == 0 {
		return nil
	}
	if err := a.Test(ctx, c); err != nil {
		return err
	}
	_, err := c.R.Output(ctx, "", c.Env.Bin("httpd"), "-f", conf(c, "httpd.conf"), "-k", "graceful")
	return err
}

func (a Apache) Status(c *Ctx) Status {
	return pidStatus(a, run(c, "httpd.pid"), webDetail(c))
}

func (Apache) Logs(c *Ctx) []string {
	return []string{logf(c, "apache-error.log"), logf(c, "apache-access.log")}
}

// Nginx runs nginx with one worker and hands PHP to php-fpm.
type Nginx struct{}

func (Nginx) Name() string       { return "web" }
func (Nginx) Title() string      { return "nginx" }
func (Nginx) Packages() []string { return []string{"nginx"} }

func (n Nginx) Configure(c *Ctx) error {
	// hampp's nginx.conf includes these package files; fail with a clear
	// message rather than a cryptic nginx error if the install is incomplete.
	for _, f := range []string{"mime.types", "fastcgi_params"} {
		p := filepath.Join(c.Env.Prefix, "etc", "nginx", f)
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("%s is missing (try: pkg reinstall nginx): %w", p, err)
		}
	}
	for _, d := range []string{"nginx-body", "nginx-proxy", "nginx-fastcgi", "nginx-uwsgi", "nginx-scgi"} {
		if err := os.MkdirAll(filepath.Join(c.Paths.Tmp(), d), 0o700); err != nil {
			return err
		}
	}
	_, err := render.WriteIfChanged("nginx.conf.tmpl", conf(c, "nginx.conf"), c.Data, 0o600)
	return err
}

// args: -p sets the prefix for relative paths; -e redirects the error log that
// nginx opens before reading the config (otherwise $PREFIX/var/log/nginx).
func (n Nginx) args(c *Ctx, extra ...string) []string {
	return append([]string{"-p", c.Paths.State, "-c", conf(c, "nginx.conf"), "-e", logf(c, "nginx-error.log")}, extra...)
}

func (n Nginx) Test(ctx context.Context, c *Ctx) error {
	_, err := c.R.Output(ctx, "", c.Env.Bin("nginx"), n.args(c, "-t")...)
	return err
}

func (n Nginx) Start(ctx context.Context, c *Ctx) error {
	if proc.Running(run(c, "nginx.pid")) > 0 {
		return nil
	}
	if err := webPorts(c); err != nil {
		return err
	}
	if _, err := c.R.Output(ctx, "", c.Env.Bin("nginx"), n.args(c)...); err != nil {
		return failure(err, logf(c, "nginx-error.log"))
	}
	return waitWeb(ctx, c, logf(c, "nginx-error.log"))
}

func (n Nginx) Stop(_ context.Context, c *Ctx) error {
	return stopPID(run(c, "nginx.pid"), syscall.SIGQUIT, 10*time.Second)
}

func (n Nginx) Reload(ctx context.Context, c *Ctx) error {
	pid := proc.Running(run(c, "nginx.pid"))
	if pid == 0 {
		return nil
	}
	if err := n.Test(ctx, c); err != nil {
		return err
	}
	return syscall.Kill(pid, syscall.SIGHUP)
}

func (n Nginx) Status(c *Ctx) Status { return pidStatus(n, run(c, "nginx.pid"), webDetail(c)) }

func (Nginx) Logs(c *Ctx) []string {
	return []string{logf(c, "nginx-error.log"), logf(c, "nginx-access.log")}
}

func webPorts(c *Ctx) error {
	host := c.Data.Listen
	if err := checkPort(host, c.Data.Port, "web"); err != nil {
		return err
	}
	if c.Data.HTTPS {
		return checkPort(host, c.Data.HTTPSPort, "https")
	}
	return nil
}

func waitWeb(ctx context.Context, c *Ctx, errLog string) error {
	wctx, cancel := waitCtx(ctx, 10*time.Second)
	defer cancel()
	if err := proc.WaitDial(wctx, "tcp", loopback(c)+":"+strconv.Itoa(c.Data.Port)); err != nil {
		return failure(err, errLog)
	}
	return nil
}

func webDetail(c *Ctx) string {
	d := fmt.Sprintf(":%d", c.Data.Port)
	if c.Data.HTTPS {
		d += fmt.Sprintf(" :%d", c.Data.HTTPSPort)
	}
	if c.Data.Listen != "127.0.0.1" {
		d += " LAN"
	}
	return d
}
