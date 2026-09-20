//go:build unix

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yourchocomate/hampp/internal/app"
	"github.com/yourchocomate/hampp/internal/service"
	"github.com/yourchocomate/hampp/internal/sys"
)

func phpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "php",
		Short: "PHP version, extensions and php.ini settings",
		Long: `Manage PHP without editing ini files by hand. Settings apply to both
websites (php-fpm) and the command line (composer, artisan).

  hampp php                       overview: version, key settings, ini files
  hampp php ext                   list extensions (built-in, enabled, available)
  hampp php ext install redis     install and enable an extension
  hampp php set memory_limit 512M change a php.ini value
  hampp php edit                  open your own php.ini overrides`,
		RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
			if err := a.RequireInit(); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			c, _ := a.Ctx()
			fmt.Fprintf(w, "PHP %s\n\n", service.PHPVersion(cmd.Context(), c))
			if err := printPHPSettings(cmd, a, nil); err != nil {
				return err
			}
			fmt.Fprintln(w, "\nini files, in load order (later wins):")
			for i, f := range a.PHPIniFiles() {
				fmt.Fprintf(w, "  %d. %s\n", i+1, a.Paths.Short(f))
			}
			fmt.Fprintln(w, "\nExtensions: hampp php ext")
			return nil
		}),
	}
	var force bool
	set := &cobra.Command{Use: "set <key> <value>", Short: "Set a php.ini value for websites and the CLI", Args: cobra.ExactArgs(2),
		RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
			if err := a.PHPSet(cmd.Context(), args[0], args[1], force); err != nil {
				return err
			}
			return printPHPSettings(cmd, a, []string{args[0]})
		})}
	set.Flags().BoolVar(&force, "force", false, "set even if PHP does not know the key yet")
	cmd.AddCommand(
		set,
		&cobra.Command{Use: "get [key]...", Short: "Show effective values (websites vs CLI)",
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error { return printPHPSettings(cmd, a, args) })},
		&cobra.Command{Use: "unset <key>", Short: "Remove a value set with `hampp php set`", Args: cobra.ExactArgs(1),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error { return a.PHPUnset(cmd.Context(), args[0]) })},
		&cobra.Command{Use: "edit", Short: "Edit your own php.ini overrides ($EDITOR, default nano)",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				if err := a.RequireInit(); err != nil {
					return err
				}
				f, err := a.PHPUserIni()
				if err != nil {
					return err
				}
				editor := os.Getenv("EDITOR")
				if editor == "" {
					editor = "nano"
				}
				name, eArgs := sys.Command(editorPath(a, editor), []string{f})
				e := exec.Command(name, eArgs...)
				e.Stdin, e.Stdout, e.Stderr = os.Stdin, os.Stdout, os.Stderr
				if err := e.Run(); err != nil {
					return err
				}
				if err := a.PHPCheckUserIni(cmd.Context()); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Saved and applied.")
				return nil
			})},
		phpExtCmd(),
	)
	return cmd
}

func printPHPSettings(cmd *cobra.Command, a *app.App, keys []string) error {
	vals, err := a.PHPGet(cmd.Context(), keys)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		keys = app.CommonPHPSettings
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "%-22s %-14s %s\n", "setting", "websites", "cli")
	for _, k := range keys {
		v := vals[k]
		fmt.Fprintf(w, "%-22s %-14s %s\n", k, v[0], v[1])
	}
	return nil
}

func phpExtCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ext",
		Short: "List PHP extensions",
		RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
			exts, err := a.PHPExtList(cmd.Context())
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			var builtin []string
			for _, e := range exts {
				if e.State == app.ExtBuiltIn {
					builtin = append(builtin, e.Name)
					continue
				}
				mark := map[app.ExtState]string{app.ExtEnabled: "on ", app.ExtInstalled: "off", app.ExtAvailable: "   "}[e.State]
				fmt.Fprintf(w, "%s %-15s %-15s %s\n", mark, e.Name, e.State, e.Description)
			}
			fmt.Fprintf(w, "\nBuilt in (always on): %s\n", strings.Join(builtin, ", "))
			fmt.Fprintln(w, "\nInstall: hampp php ext install <name>   Turn off: hampp php ext disable <name>")
			return nil
		}),
	}
	cmd.AddCommand(
		&cobra.Command{Use: "install <name>...", Short: "Install and enable extensions (e.g. redis gd imagick)", Args: cobra.MinimumNArgs(1),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
				return a.PHPExtInstall(cmd.Context(), args...)
			})},
		&cobra.Command{Use: "enable <name>", Short: "Enable an installed extension", Args: cobra.ExactArgs(1),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
				return a.PHPExtEnable(cmd.Context(), args[0])
			})},
		&cobra.Command{Use: "disable <name>", Short: "Disable an extension hampp enabled", Args: cobra.ExactArgs(1),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
				return a.PHPExtDisable(cmd.Context(), args[0])
			})},
		&cobra.Command{Use: "remove <name>", Short: "Uninstall an extension package", Args: cobra.ExactArgs(1),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
				return a.PHPExtRemove(cmd.Context(), args[0])
			})},
	)
	return cmd
}
