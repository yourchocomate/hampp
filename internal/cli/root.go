//go:build unix

// Package cli defines hampp's commands. Each command is a thin wrapper over app.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/yourchocomate/hampp/internal/app"
	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/tui"
)

// Version is set at build time with -ldflags "-X .../cli.Version=...".
var Version = "dev"

var ascii bool

func Execute() int {
	root := newRoot()
	if err := root.ExecuteContext(context.Background()); err != nil {
		msg := err.Error()
		if errors.Is(err, config.ErrNotInitialized) {
			msg = "hampp is not set up yet. Run: hampp init"
		}
		fmt.Fprintln(os.Stderr, "hampp: "+msg)
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "hampp",
		Short: "Local PHP web server stack for Termux (Apache/nginx, PHP-FPM, MariaDB)",
		Long: `hampp sets up and runs a local web development stack inside Termux.

Run it without arguments for the interactive dashboard.`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := load(cmd)
			if err != nil {
				return err
			}
			if !isTTY() {
				return printStatus(cmd, a, false)
			}
			return tui.Run(cmd.Context(), a, tui.Options{ASCII: asciiMode()})
		},
	}
	root.PersistentFlags().BoolVar(&ascii, "ascii", false, "draw with plain ASCII characters (also HAMPP_ASCII=1)")
	root.AddCommand(
		initCmd(), startCmd(), stopCmd(), restartCmd(), reloadCmd(), statusCmd(), logsCmd(),
		openCmd(), shareCmd(), serveCmd(), dbCmd(), doctorCmd(), nodeCmd(), editCmd(),
		codeCmd(), mirrorCmd(), siteCmd(), sslCmd(), phpCmd(), configCmd(), uninstallCmd(), versionCmd(),
	)
	return root
}

func load(cmd *cobra.Command) (*app.App, error) {
	return app.Load(cmd.Context(), cmd.OutOrStdout())
}

// editorPath resolves an editor name to an absolute path, so the Android exec
// workaround can read its shebang when it has to.
func editorPath(a *app.App, editor string) string {
	if strings.ContainsRune(editor, '/') {
		return editor
	}
	if p, err := a.R.LookPath(editor); err == nil {
		return p
	}
	return a.Env.Bin(editor)
}

func isTTY() bool {
	return term.IsTerminal(os.Stdout.Fd()) && term.IsTerminal(os.Stdin.Fd())
}

func asciiMode() bool { return ascii || os.Getenv("HAMPP_ASCII") == "1" }

func printLines(cmd *cobra.Command, lines []string) {
	for _, l := range lines {
		fmt.Fprintln(cmd.OutOrStdout(), l)
	}
}

func printChecks(cmd *cobra.Command, checks []app.Check) (failed bool) {
	w := cmd.OutOrStdout()
	for _, c := range checks {
		fmt.Fprintf(w, "%s %s\n", c.Level.Symbol(asciiMode()), c.Message)
		if c.Fix != "" && c.Level != app.OK {
			for _, l := range strings.Split(c.Fix, "\n") {
				fmt.Fprintf(w, "    %s\n", l)
			}
		}
		if c.Level == app.Fail {
			failed = true
		}
	}
	return failed
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the hampp version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "hampp "+Version)
		},
	}
}
