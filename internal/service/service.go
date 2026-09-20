//go:build unix

// Package service starts, stops and inspects the daemons hampp manages. Every
// daemon runs from a hampp-rendered config; package-owned configs are never edited.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/paths"
	"github.com/yourchocomate/hampp/internal/proc"
	"github.com/yourchocomate/hampp/internal/render"
	"github.com/yourchocomate/hampp/internal/sys"
	"github.com/yourchocomate/hampp/internal/termux"
)

// Ctx carries everything a service needs.
type Ctx struct {
	Env   termux.Env
	Paths paths.Paths
	Cfg   config.Config
	Creds config.Credentials
	R     sys.Runner
	Data  render.Data
}

type Status struct {
	Name    string
	Title   string
	Running bool
	PID     int
	Since   time.Time
	Detail  string
}

type Service interface {
	Name() string  // short id used on the command line: web, php, db, code, mirror
	Title() string // program name shown to users
	Packages() []string
	Configure(c *Ctx) error
	Start(ctx context.Context, c *Ctx) error
	Stop(ctx context.Context, c *Ctx) error
	Status(c *Ctx) Status
	Logs(c *Ctx) []string
}

// Reloader applies config changes without dropping connections.
type Reloader interface {
	Reload(ctx context.Context, c *Ctx) error
}

// Tester validates the rendered config with the daemon's own checker.
type Tester interface {
	Test(ctx context.Context, c *Ctx) error
}

// ErrPortInUse is returned when another program holds a port hampp needs.
var ErrPortInUse = errors.New("port in use")

func pidStatus(s Service, pidFile, detail string) Status {
	st := Status{Name: s.Name(), Title: s.Title(), Detail: detail}
	if pid := proc.Running(pidFile); pid > 0 {
		st.Running, st.PID, st.Since = true, pid, proc.StartTime(pidFile)
	}
	return st
}

func checkPort(host string, port int, what string) error {
	if !proc.PortFree(host, port) {
		return fmt.Errorf("%w: %s:%d is used by another program (%s). Stop it or change the port with `hampp config set`", ErrPortInUse, host, port, what)
	}
	return nil
}

func stopPID(pidFile string, sig syscall.Signal, grace time.Duration) error {
	pid := proc.Running(pidFile)
	if pid == 0 {
		return nil
	}
	if err := proc.Stop(pid, sig, grace); err != nil {
		return err
	}
	_ = os.Remove(pidFile)
	return nil
}

// failure wraps a start error with the last lines of the service log.
func failure(err error, logs ...string) error {
	var tails []string
	for _, l := range logs {
		if t := Tail(l, 12); t != "" {
			tails = append(tails, fmt.Sprintf("--- %s ---\n%s", filepath.Base(l), t))
		}
	}
	if len(tails) == 0 {
		return err
	}
	return fmt.Errorf("%w\n%s", err, strings.Join(tails, "\n"))
}

// Tail returns the last n lines of a file.
func Tail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return ""
	}
	if len(b) > 64<<10 {
		b = b[len(b)-64<<10:]
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func waitCtx(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

func conf(c *Ctx, name string) string { return filepath.Join(c.Paths.Conf(), name) }
func run(c *Ctx, name string) string  { return filepath.Join(c.Paths.Run(), name) }
func logf(c *Ctx, name string) string { return filepath.Join(c.Paths.Log(), name) }

func loopback(*Ctx) string { return "127.0.0.1" }
