//go:build unix

package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yourchocomate/hampp/internal/app"
	"github.com/yourchocomate/hampp/internal/node"
	"github.com/yourchocomate/hampp/internal/termux"
)

func run(fn func(cmd *cobra.Command, a *app.App, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		a, err := load(cmd)
		if err != nil {
			return err
		}
		return fn(cmd, a, args)
	}
}

func dbCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "db", Short: "Database helpers"}
	cmd.AddCommand(
		&cobra.Command{Use: "setup", Short: "Create the hampp MariaDB user and set a root password",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error { return a.DBSetup(cmd.Context()) })},
		&cobra.Command{Use: "info", Short: "Show connection details",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				printLines(cmd, a.DBInfo())
				return nil
			})},
		&cobra.Command{Use: "create <name>", Short: "Create a MariaDB database", Args: cobra.ExactArgs(1),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error { return a.DBCreate(cmd.Context(), args[0]) })},
	)
	return cmd
}

func nodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Switch Node.js versions (nvm-style, using Termux TUR packages)",
		Long: `nvm itself cannot work on Termux: it refuses to run while $PREFIX is set and
downloads glibc builds that do not run on Android. hampp installs the TUR
packages nodejs-NN side by side and switches between them instead.`,
	}
	var install bool
	use := &cobra.Command{
		Use:   "use [major|system]",
		Short: "Use a Node.js version (no argument: read .nvmrc / .node-version)",
		Args:  cobra.MaximumNArgs(1),
		RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
			if len(args) == 0 {
				wd, _ := os.Getwd()
				return a.NodeUseRC(cmd.Context(), wd, install)
			}
			sp, err := node.ParseSpec(args[0])
			if err != nil {
				return err
			}
			major, err := sp.Resolve(a.Node().Installed)
			if err != nil {
				return fmt.Errorf("%w (install it with: hampp node install %s)", err, args[0])
			}
			return a.NodeUse(major)
		}),
	}
	use.Flags().BoolVar(&install, "install", false, "install the version from .nvmrc if missing")
	cmd.AddCommand(
		&cobra.Command{Use: "ls", Short: "List installed versions",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				st := a.Node()
				w := cmd.OutOrStdout()
				mark := func(on bool) string {
					if on {
						return "->"
					}
					return "  "
				}
				for _, m := range st.Installed {
					fmt.Fprintf(w, "%s %d\n", mark(m == st.Active), m)
				}
				sys := "system (not installed)"
				if st.HasSystem {
					sys = "system"
				}
				fmt.Fprintf(w, "%s %s\n", mark(st.Active == 0), sys)
				return nil
			})},
		&cobra.Command{Use: "ls-remote", Short: "List versions available from TUR",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				majors, err := a.NodeRemote(cmd.Context())
				if err != nil {
					return err
				}
				for _, m := range majors {
					lts := ""
					if m%2 == 0 {
						lts = "  (LTS line)"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%d%s\n", m, lts)
				}
				return nil
			})},
		&cobra.Command{Use: "install <major>", Short: "Install a Node.js major from TUR", Args: cobra.ExactArgs(1),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
				sp, err := node.ParseSpec(args[0])
				if err != nil {
					return err
				}
				major := sp.Major
				if sp.Latest || sp.LTS {
					remote, err := a.NodeRemote(cmd.Context())
					if err != nil {
						return err
					}
					if major, err = sp.Resolve(remote); err != nil {
						return err
					}
				}
				return a.NodeInstall(cmd.Context(), major)
			})},
		use,
		&cobra.Command{Use: "current", Short: "Print the active version",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				if st := a.Node(); st.Active > 0 {
					fmt.Fprintln(cmd.OutOrStdout(), st.Active)
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "system")
				}
				return nil
			})},
		&cobra.Command{Use: "uninstall <major>", Short: "Remove a Node.js major", Args: cobra.ExactArgs(1),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
				m, err := strconv.Atoi(strings.TrimPrefix(args[0], "v"))
				if err != nil {
					return fmt.Errorf("give a major version, e.g. 20")
				}
				return a.NodeUninstall(cmd.Context(), m)
			})},
	)
	return cmd
}

func editCmd() *cobra.Command {
	return &cobra.Command{Use: "edit", Short: "How to edit your sites with Android editors",
		RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
			printLines(cmd, a.EditGuide())
			return nil
		})}
}

func codeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "code", Short: "VS Code in the browser (code-server)"}
	cmd.AddCommand(
		&cobra.Command{Use: "start", Short: "Install and start code-server on ~/www",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				info, err := a.CodeStart(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "VS Code: "+info)
				return nil
			})},
		&cobra.Command{Use: "stop", Short: "Stop code-server",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error { return a.Stop(cmd.Context(), []string{"code"}) })},
	)
	return cmd
}

func mirrorCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "mirror", Short: "Copy /sdcard/www into ~/www continuously (one-way)"}
	var force bool
	on := &cobra.Command{Use: "on", Short: "Start mirroring",
		RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error { return a.MirrorOn(cmd.Context(), force) })}
	on.Flags().BoolVar(&force, "force", false, "allow deleting files in ~/www that are not in the source")
	cmd.AddCommand(on,
		&cobra.Command{Use: "off", Short: "Stop mirroring",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error { return a.MirrorOff(cmd.Context()) })},
		&cobra.Command{Use: "run", Hidden: true,
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error { return a.MirrorRun(cmd.Context()) })},
	)
	return cmd
}

func siteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Sites on <name>.localhost (folders in ~/www are added automatically)",
	}
	var root string
	var force bool
	link := &cobra.Command{Use: "link [name] [path]", Short: "Serve a folder outside ~/www at <name>.localhost",
		Args: cobra.MaximumNArgs(2),
		RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
			name, path := "", "."
			if len(args) >= 1 {
				name = args[0]
			}
			if len(args) == 2 {
				path = args[1]
			}
			return a.SiteLink(cmd.Context(), name, path, root, force)
		})}
	link.Flags().StringVar(&root, "root", "", "document root inside the folder (default: public/ or web/ if present)")
	link.Flags().BoolVar(&force, "force", false, "allow folders on shared storage")
	cmd.AddCommand(
		&cobra.Command{Use: "ls", Short: "List sites",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				sites, warnings := a.Sites()
				for _, s := range sites {
					kind := "parked"
					if s.Default {
						kind = "default"
					} else if s.Linked {
						kind = "linked"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%-32s %-8s %s\n", s.URL(a.Cfg.Web.Port, false), kind, a.Paths.Short(s.DocRoot))
				}
				for _, w := range warnings {
					fmt.Fprintln(cmd.OutOrStdout(), "warning: "+w)
				}
				return nil
			})},
		link,
		&cobra.Command{Use: "unlink <name>", Short: "Remove a linked site", Args: cobra.ExactArgs(1),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error { return a.SiteUnlink(cmd.Context(), args[0]) })},
		&cobra.Command{Use: "sync", Short: "Pick up new or removed folders (same as reload)",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error { return a.Reload(cmd.Context()) })},
		&cobra.Command{Use: "open <name>", Short: "Open a site in the browser", Args: cobra.ExactArgs(1),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
				url, err := a.OpenURL(args[0], false)
				if err != nil {
					return err
				}
				return termux.OpenURL(cmd.Context(), a.R, url)
			})},
	)
	return cmd
}

func sslCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ssl", Short: "Local HTTPS with a hampp certificate authority"}
	cmd.AddCommand(
		&cobra.Command{Use: "init", Short: "Enable HTTPS and create the local CA",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				if err := a.SSLInit(cmd.Context()); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Next: hampp ssl trust")
				return nil
			})},
		&cobra.Command{Use: "trust", Short: "Trust the local CA in Termux and guide you through Android Settings",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				res, err := a.SSLTrust(cmd.Context())
				if err != nil {
					return err
				}
				printTrust(cmd, a, res)
				return nil
			})},
		&cobra.Command{Use: "status", Short: "Check certificates and HTTPS",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				if printChecks(cmd, a.SSLStatus(cmd.Context())) {
					return fmt.Errorf("some checks failed")
				}
				return nil
			})},
		&cobra.Command{Use: "uninstall", Short: "Disable HTTPS and delete the local CA",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				steps, err := a.SSLUninstall(cmd.Context())
				printLines(cmd, steps)
				return err
			})},
	)
	return cmd
}

func printTrust(cmd *cobra.Command, a *app.App, r app.TrustResult) {
	w := cmd.OutOrStdout()
	ok := app.OK.Symbol(asciiMode())
	bad := app.Fail.Symbol(asciiMode())
	if r.CopyErr != nil {
		fmt.Fprintf(w, "%s %v\n", bad, r.CopyErr)
	} else {
		fmt.Fprintf(w, "%s Copied the CA to %s\n", ok, r.CopiedTo)
	}
	if r.TermuxTrusted {
		fmt.Fprintf(w, "%s Termux tools trust it (curl, php, composer via SSL_CERT_FILE)\n", ok)
	} else if r.TermuxErr != nil {
		fmt.Fprintf(w, "%s Termux trust: %v\n", bad, r.TermuxErr)
	}
	fmt.Fprintln(w, "\nNow in Android (Chrome and most browsers):")
	for i, s := range r.AndroidSteps {
		fmt.Fprintf(w, "  %d. %s\n", i+1, s)
	}
	fmt.Fprintln(w, "\nFirefox:")
	for _, s := range r.FirefoxSteps {
		fmt.Fprintln(w, "  "+s)
	}
	fmt.Fprintf(w, "\nCertificate SHA-256: %s\n", r.Fingerprint)
	fmt.Fprintf(w, "Then open https://localhost:%d/ and look for the padlock.\n", a.Cfg.Web.HTTPSPort)
	if isTTY() {
		fmt.Fprint(w, "\nOpen Android Settings now? [Y/n] ")
		var ans string
		_, _ = fmt.Fscanln(os.Stdin, &ans)
		if ans == "" || strings.HasPrefix(strings.ToLower(ans), "y") {
			if err := termux.OpenSecuritySettings(cmd.Context(), a.R); err != nil {
				fmt.Fprintln(w, "Could not open Settings automatically; open it yourself.")
			}
		}
	}
}
