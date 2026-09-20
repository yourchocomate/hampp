//go:build unix

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yourchocomate/hampp/internal/node"
	"github.com/yourchocomate/hampp/internal/rcfile"
	"github.com/yourchocomate/hampp/internal/service"
)

// ensureTUR installs tur-repo, which provides nodejs-NN and code-server.
func (a *App) ensureTUR(ctx context.Context) error {
	if a.PM.Installed(ctx, "tur-repo") {
		return nil
	}
	a.printf("Adding the Termux User Repository (tur-repo)…\n")
	return a.PM.Install(ctx, a.Out, "tur-repo")
}

// NodeRemote lists Node majors available from TUR, newest first.
func (a *App) NodeRemote(ctx context.Context) ([]int, error) {
	if err := a.RequireTermux(); err != nil {
		return nil, err
	}
	if err := a.ensureTUR(ctx); err != nil {
		return nil, err
	}
	return a.PM.NodeMajors(ctx)
}

// NodeInstall installs nodejs-<major> and activates it if nothing is active.
func (a *App) NodeInstall(ctx context.Context, major int) error {
	if err := a.RequireTermux(); err != nil {
		return err
	}
	if err := a.ensureTUR(ctx); err != nil {
		return err
	}
	if err := a.PM.Install(ctx, a.Out, node.Package(major)); err != nil {
		return err
	}
	if _, active := node.Current(a.Paths.NodeCurrent()); !active {
		return a.NodeUse(major)
	}
	return nil
}

func (a *App) NodeUninstall(ctx context.Context, major int) error {
	if cur, ok := node.Current(a.Paths.NodeCurrent()); ok && cur == major {
		if err := node.Use(a.Env.Prefix, a.Paths.NodeCurrent(), 0); err != nil {
			return err
		}
	}
	return a.PM.Uninstall(ctx, a.Out, node.Package(major))
}

// NodeUse activates a major (0 = system). New shells pick it up via the rc hook.
func (a *App) NodeUse(major int) error {
	if err := node.Use(a.Env.Prefix, a.Paths.NodeCurrent(), major); err != nil {
		return err
	}
	if major == 0 {
		a.printf("Using system Node.js (%s)\n", a.Env.Bin("node"))
	} else {
		a.printf("Now using Node.js %d (%s)\n", major, a.Paths.Short(node.InstallDir(a.Env.Prefix, major)))
	}
	if !a.rcHookPresent() {
		a.printf("Add hampp to your shell so `node` follows this: hampp config set rc_hook true && hampp init\n")
	} else if !a.pathActive() {
		a.printf("Open a new Termux session (or run: exec $SHELL) to use it here.\n")
	}
	return nil
}

// NodeUseRC activates the version from .nvmrc/.node-version in dir.
func (a *App) NodeUseRC(ctx context.Context, dir string, install bool) error {
	sp, file, err := node.FindRC(dir)
	if err != nil {
		return err
	}
	cands := node.Installed(a.Env.Prefix)
	major, err := sp.Resolve(cands)
	if err != nil && install {
		remote, rerr := a.NodeRemote(ctx)
		if rerr != nil {
			return rerr
		}
		if major, err = sp.Resolve(remote); err == nil {
			if err = a.NodeInstall(ctx, major); err != nil {
				return err
			}
		}
	}
	if err != nil {
		return fmt.Errorf("%s asks for %q: %w", a.Paths.Short(file), sp.Raw, err)
	}
	if sp.Exact {
		a.printf("Note: %s pins %s; TUR ships one release per major, so Node.js %d is used.\n", a.Paths.Short(file), sp.Raw, major)
	}
	return a.NodeUse(major)
}

// NodeState describes versions for `node ls` and the TUI.
type NodeState struct {
	Installed []int
	Active    int  // 0 = system/none
	HasSystem bool // nodejs or nodejs-lts installed
}

func (a *App) Node() NodeState {
	st := NodeState{Installed: node.Installed(a.Env.Prefix)}
	st.Active, _ = node.Current(a.Paths.NodeCurrent())
	if !slices.Contains(st.Installed, st.Active) {
		st.Active = 0
	}
	_, err := os.Stat(a.Env.Bin("node"))
	st.HasSystem = err == nil
	return st
}

// InstallRCHook writes hampp's block to ~/.bashrc (and ~/.zshrc if present).
func (a *App) InstallRCHook() error {
	block := rcfile.Block(rcfile.Options{
		NodeBin:    filepath.Join(a.Paths.NodeCurrent(), "bin"),
		CABundle:   a.bundlePath(),
		PHPScanDir: service.ScanDir(a.Paths),
		// Composer's global bin dir: ~/.composer, or ~/.config/composer when XDG vars are set.
		AfterPath: []string{
			filepath.Join(a.Env.Home, ".composer", "vendor", "bin"),
			filepath.Join(a.Env.Home, ".config", "composer", "vendor", "bin"),
		},
	})
	for _, f := range a.rcFiles() {
		changed, err := rcfile.Install(f, block)
		if err != nil {
			return err
		}
		if changed {
			a.printf("Updated %s (open a new session to apply)\n", a.Paths.Short(f))
		}
	}
	return nil
}

func (a *App) rcFiles() []string {
	files := []string{filepath.Join(a.Env.Home, ".bashrc")}
	if _, err := os.Stat(filepath.Join(a.Env.Home, ".zshrc")); err == nil {
		files = append(files, filepath.Join(a.Env.Home, ".zshrc"))
	}
	return files
}

func (a *App) rcHookPresent() bool {
	b, err := os.ReadFile(filepath.Join(a.Env.Home, ".bashrc"))
	return err == nil && strings.Contains(string(b), rcfile.Begin)
}

func (a *App) pathActive() bool {
	return strings.Contains(":"+os.Getenv("PATH")+":", ":"+filepath.Join(a.Paths.NodeCurrent(), "bin")+":")
}
