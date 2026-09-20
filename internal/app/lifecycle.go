//go:build unix

package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/yourchocomate/hampp/internal/service"
	"github.com/yourchocomate/hampp/internal/termux"
)

// Prepare issues certificates and renders the config files of svcs, so they
// always match config.toml. Only the given services are configured: starting
// the database must not require the web server's modules to be installed.
func (a *App) Prepare(ctx context.Context, svcs []service.Service) (*service.Ctx, error) {
	if err := a.Paths.Ensure(); err != nil {
		return nil, err
	}
	c, warnings := a.Ctx()
	for _, w := range warnings {
		a.printf("warning: %s\n", w)
	}
	if err := a.ensureCerts(c); err != nil {
		return nil, err
	}
	for _, s := range svcs {
		if err := s.Configure(c); err != nil {
			return nil, fmt.Errorf("%s: %w", s.Title(), err)
		}
	}
	return c, nil
}

// Start starts services in dependency order and waits until each is healthy.
func (a *App) Start(ctx context.Context, names []string) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	svcs, err := a.Find(names)
	if err != nil {
		return err
	}
	if missing := a.missingPackages(ctx, svcs); len(missing) > 0 {
		return fmt.Errorf("missing packages: %v (run: hampp init, or %v)", missing, a.PM.InstallArgs(missing...))
	}
	c, err := a.Prepare(ctx, svcs)
	if err != nil {
		return err
	}
	if a.Cfg.WakeLock {
		_ = termux.WakeLock(ctx, a.R, true)
	}
	for _, s := range svcs {
		if s.Status(c).Running {
			a.printf("  %-7s %s already running\n", s.Name(), s.Title())
			continue
		}
		if t, ok := s.(service.Tester); ok {
			if err := t.Test(ctx, c); err != nil {
				return fmt.Errorf("%s config check failed: %w", s.Title(), err)
			}
		}
		if err := s.Start(ctx, c); err != nil {
			return fmt.Errorf("%s did not start: %w", s.Title(), err)
		}
		a.printf("  %-7s %s started\n", s.Name(), s.Title())
	}
	return nil
}

// Stop stops services in reverse order. With no names it stops everything,
// including code-server and the mirror, and releases the wakelock.
func (a *App) Stop(ctx context.Context, names []string) error {
	all := len(names) == 0 || (len(names) == 1 && names[0] == "all")
	var svcs []service.Service
	if all {
		svcs = a.All()
	} else {
		var err error
		if svcs, err = a.Find(names); err != nil {
			return err
		}
	}
	c, _ := a.Ctx()
	var errs []error
	for i := len(svcs) - 1; i >= 0; i-- {
		s := svcs[i]
		if !s.Status(c).Running {
			continue
		}
		if err := s.Stop(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Title(), err))
			continue
		}
		a.printf("  %-7s %s stopped\n", s.Name(), s.Title())
	}
	if all {
		_ = termux.WakeLock(ctx, a.R, false)
	}
	return errors.Join(errs...)
}

func (a *App) Restart(ctx context.Context, names []string) error {
	if err := a.Stop(ctx, names); err != nil {
		return err
	}
	return a.Start(ctx, names)
}

// Reload re-renders configs (new sites, certificates) and reloads running
// services without dropping connections.
func (a *App) Reload(ctx context.Context) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	pre, _ := a.Ctx()
	var running []service.Service
	for _, s := range a.Core() {
		if s.Status(pre).Running {
			running = append(running, s)
		}
	}
	if len(running) == 0 {
		a.printf("  Nothing is running; changes apply on the next `hampp start`.\n")
		return nil
	}
	c, err := a.Prepare(ctx, running)
	if err != nil {
		return err
	}
	for _, s := range running {
		r, ok := s.(service.Reloader)
		if !ok {
			continue
		}
		if err := r.Reload(ctx, c); err != nil {
			return fmt.Errorf("%s reload: %w", s.Title(), err)
		}
		a.printf("  %-7s %s reloaded\n", s.Name(), s.Title())
	}
	return nil
}

// ReloadService applies config changes to one service: it reloads gracefully
// when the daemon supports it (Apache, nginx, php-fpm), restarts it otherwise
// (MariaDB), and starts it when it is not running.
func (a *App) ReloadService(ctx context.Context, name string) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	svcs, err := a.Find([]string{name})
	if err != nil {
		return err
	}
	s := svcs[0]
	pre, _ := a.Ctx()
	if !s.Status(pre).Running {
		return a.Start(ctx, []string{name})
	}
	r, ok := s.(service.Reloader)
	if !ok {
		return a.Restart(ctx, []string{name})
	}
	c, err := a.Prepare(ctx, svcs)
	if err != nil {
		return err
	}
	if err := r.Reload(ctx, c); err != nil {
		return fmt.Errorf("%s reload: %w", s.Title(), err)
	}
	a.printf("  %-7s %s reloaded\n", s.Name(), s.Title())
	return nil
}

// Statuses reports every service.
func (a *App) Statuses() []service.Status {
	c, _ := a.Ctx()
	var out []service.Status
	for _, s := range a.All() {
		out = append(out, s.Status(c))
	}
	return out
}

func (a *App) missingPackages(ctx context.Context, svcs []service.Service) []string {
	var pkgs []string
	for _, s := range svcs {
		pkgs = append(pkgs, s.Packages()...)
	}
	return a.PM.Missing(ctx, pkgs...)
}
