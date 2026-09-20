//go:build unix

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/service"
)

// EditGuide explains how to edit ~/www from Android apps. Termux exposes its
// home folder through the Storage Access Framework ("Termux" in file pickers).
func (a *App) EditGuide() []string {
	root := a.Paths.Short(a.Cfg.Web.Root)
	return []string{
		"Your sites live in " + root + " (inside Termux, so npm, composer and git work).",
		"",
		"Acode (recommended, free):",
		"  File browser > Add path > pick \"Termux\" in the side menu > " + strings.TrimPrefix(root, "~/"),
		"Material Files:",
		"  Menu > Add storage > External storage > Termux > " + strings.TrimPrefix(root, "~/"),
		"VS Code in the browser:",
		"  hampp code start   (then open http://127.0.0.1:" + strconv.Itoa(a.Cfg.Code.Port) + ")",
		"Terminal editors:",
		"  nano " + root + "/index.php     (also: pkg install micro | neovim | helix)",
		"",
		"Editors see changes made from the terminal after you reopen the file.",
		"Must use a folder on /sdcard? `hampp mirror on` copies /sdcard/www here.",
	}
}

// CodeStart installs (if needed) and starts code-server.
func (a *App) CodeStart(ctx context.Context) (string, error) {
	if err := a.RequireInit(); err != nil {
		return "", err
	}
	if runtime.GOARCH == "386" {
		return "", service.ErrUnsupportedArch
	}
	if !a.PM.Installed(ctx, "code-server") {
		if err := a.ensureTUR(ctx); err != nil {
			return "", err
		}
		a.printf("Installing code-server (large download, pulls nodejs-24)…\n")
		if err := a.PM.Install(ctx, a.Out, "code-server"); err != nil {
			return "", err
		}
	}
	if a.Creds.CodePassword == "" {
		a.Creds.CodePassword = config.NewPassword()
		if err := a.SaveCreds(); err != nil {
			return "", err
		}
	}
	if err := a.Start(ctx, []string{"code"}); err != nil {
		return "", err
	}
	return fmt.Sprintf("http://127.0.0.1:%d/  password: %s", a.Cfg.Code.Port, a.Creds.CodePassword), nil
}

// MirrorOn enables the /sdcard → web root mirror after a dry run, refusing to
// delete files in the web root unless forced.
func (a *App) MirrorOn(ctx context.Context, force bool) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	if missing := a.PM.Missing(ctx, "rsync"); len(missing) > 0 {
		if err := a.PM.Install(ctx, a.Out, missing...); err != nil {
			return err
		}
	}
	src := a.Cfg.Mirror.Source
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("%s not found: run `termux-setup-storage`, then create a \"www\" folder in your phone's internal storage", a.Paths.Short(src))
	}
	args := append([]string{"--dry-run", "--itemize-changes"}, service.RsyncArgs(src, a.Cfg.Web.Root)...)
	out, err := a.R.Output(ctx, "", "rsync", args...)
	if err != nil {
		return err
	}
	var deletes []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "*deleting") {
			deletes = append(deletes, strings.TrimSpace(strings.TrimPrefix(l, "*deleting")))
		}
	}
	if len(deletes) > 0 && !force {
		shown := deletes
		if len(shown) > 10 {
			shown = append(shown[:10:10], fmt.Sprintf("… and %d more", len(deletes)-10))
		}
		return fmt.Errorf("the mirror makes %s an exact copy of %s and would delete:\n  %s\nCopy those files to %s first, or run `hampp mirror on --force`",
			a.Paths.Short(a.Cfg.Web.Root), a.Paths.Short(src), strings.Join(shown, "\n  "), a.Paths.Short(src))
	}
	a.Cfg.Mirror.Enabled = true
	if err := a.SaveConfig(); err != nil {
		return err
	}
	if err := a.Start(ctx, []string{"mirror"}); err != nil {
		return err
	}
	a.printf("Mirroring %s → %s every %ds. Run npm/composer/git in %s, not on /sdcard.\n",
		a.Paths.Short(src), a.Paths.Short(a.Cfg.Web.Root), a.Cfg.Mirror.Interval, a.Paths.Short(a.Cfg.Web.Root))
	return nil
}

func (a *App) MirrorOff(ctx context.Context) error {
	a.Cfg.Mirror.Enabled = false
	if err := a.SaveConfig(); err != nil {
		return err
	}
	return a.Stop(ctx, []string{"mirror"})
}

// MirrorRun is the long-running loop behind the mirror service.
func (a *App) MirrorRun(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	interval := time.Duration(a.Cfg.Mirror.Interval) * time.Second
	args := service.RsyncArgs(a.Cfg.Mirror.Source, a.Cfg.Web.Root)
	failures := 0
	for {
		if _, err := a.R.Output(ctx, "", "rsync", args...); err != nil && !errors.Is(ctx.Err(), context.Canceled) {
			failures++
			if failures == 1 || failures%30 == 0 {
				a.printf("%s rsync: %v\n", time.Now().Format(time.TimeOnly), err)
			}
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}
