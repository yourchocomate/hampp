//go:build unix

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/yourchocomate/hampp/internal/app"
	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/sys"
	"github.com/yourchocomate/hampp/internal/termux"
	"github.com/yourchocomate/hampp/internal/tui"
)

func initCmd() *cobra.Command {
	var o app.InitOptions
	var noRC, noStart, yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Install packages and set up hampp (safe to run again)",
		Long: `Installs PHP, php-fpm, a web server and a database, writes hampp's own
config files and starts everything. Run it with no flags for a guided setup.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := load(cmd)
			if err != nil {
				return err
			}
			if err := a.RequireTermux(); err != nil {
				return err
			}
			if !yes && cmd.Flags().NFlag() == 0 && isTTY() {
				opts, ok, err := tui.Wizard(a, asciiMode())
				if err != nil || !ok {
					return err
				}
				o = opts
			} else {
				d := a.DefaultInitOptions()
				if !cmd.Flags().Changed("web") {
					o.Web = d.Web
				}
				if !cmd.Flags().Changed("db") {
					o.DB = d.DB
				}
				if !cmd.Flags().Changed("root") {
					o.Root = d.Root
				}
				if !cmd.Flags().Changed("port") {
					o.Port = d.Port
				}
				o.RCHook, o.Start = !noRC, !noStart
			}
			return a.Init(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Web, "web", config.WebApache, "web server: apache or nginx")
	f.StringVar(&o.DB, "db", config.DBMariaDB, "database: mariadb, sqlite or none")
	f.StringVar(&o.Root, "root", "", "web root (default ~/www)")
	f.IntVar(&o.Port, "port", 8080, "HTTP port (1024-65535)")
	f.BoolVar(&o.HTTPS, "https", false, "enable local HTTPS with a hampp CA")
	f.IntVar(&o.Node, "node", 0, "also install this Node.js major from TUR (e.g. 22)")
	f.BoolVar(&noRC, "no-rc", false, "do not add hampp's block to ~/.bashrc")
	f.BoolVar(&noStart, "no-start", false, "do not start services after setup")
	f.BoolVarP(&yes, "yes", "y", false, "use defaults without asking")
	return cmd
}

func svcArgs(use, short string, fn func(cmd *cobra.Command, a *app.App, names []string) error) *cobra.Command {
	return &cobra.Command{
		Use:       use + " [web|php|db|code|mirror|all]...",
		Short:     short,
		ValidArgs: []string{"web", "php", "db", "code", "mirror", "all"},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load(cmd)
			if err != nil {
				return err
			}
			return fn(cmd, a, args)
		},
	}
}

func startCmd() *cobra.Command {
	return svcArgs("start", "Start services (default: db, php, web)", func(cmd *cobra.Command, a *app.App, n []string) error {
		if err := a.Start(cmd.Context(), n); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Open http://localhost:%d/\n", a.Cfg.Web.Port)
		return nil
	})
}

func stopCmd() *cobra.Command {
	return svcArgs("stop", "Stop services (default: everything)", func(cmd *cobra.Command, a *app.App, n []string) error {
		return a.Stop(cmd.Context(), n)
	})
}

func restartCmd() *cobra.Command {
	return svcArgs("restart", "Restart services", func(cmd *cobra.Command, a *app.App, n []string) error {
		return a.Restart(cmd.Context(), n)
	})
}

func reloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "reload [web|php|db|code|mirror]...",
		Short:     "Re-scan sites, refresh certificates and reload running servers",
		Long:      "With no arguments this reloads every running service. Name services to reload only those.",
		ValidArgs: []string{"web", "php", "db", "code", "mirror", "all"},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load(cmd)
			if err != nil {
				return err
			}
			if len(args) == 0 || (len(args) == 1 && args[0] == "all") {
				return a.Reload(cmd.Context())
			}
			for _, n := range args {
				if err := a.ReloadService(cmd.Context(), n); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show which services are running",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := load(cmd)
			if err != nil {
				return err
			}
			return printStatus(cmd, a, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func printStatus(cmd *cobra.Command, a *app.App, asJSON bool) error {
	sts := a.Statuses()
	if asJSON {
		type row struct {
			Name    string `json:"name"`
			Program string `json:"program"`
			Running bool   `json:"running"`
			PID     int    `json:"pid,omitempty"`
			Since   string `json:"since,omitempty"`
			Detail  string `json:"detail"`
		}
		var rows []row
		for _, s := range sts {
			r := row{Name: s.Name, Program: s.Title, Running: s.Running, PID: s.PID, Detail: s.Detail}
			if s.Running {
				r.Since = s.Since.Format(time.RFC3339)
			}
			rows = append(rows, r)
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	w := cmd.OutOrStdout()
	if !a.Initialized {
		fmt.Fprintln(w, "hampp is not set up yet. Run: hampp init")
		return nil
	}
	for _, s := range sts {
		dot, state := "○", "stopped"
		if asciiMode() {
			dot = "o"
		}
		if s.Running {
			dot, state = "●", "running "+tui.Uptime(time.Since(s.Since))
			if asciiMode() {
				dot = "*"
			}
		}
		fmt.Fprintf(w, "%s %-7s %-12s %-18s %s\n", dot, s.Name, s.Title, s.Detail, state)
	}
	sites, _ := a.Sites()
	fmt.Fprintln(w)
	for _, s := range sites {
		fmt.Fprintf(w, "  %s  %s\n", s.URL(a.Cfg.Web.Port, false), a.Paths.Short(s.DocRoot))
	}
	return nil
}

func logsCmd() *cobra.Command {
	var follow bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs [web|php|db|code|mirror]",
		Short: "Show service logs",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load(cmd)
			if err != nil {
				return err
			}
			names := args
			if len(names) == 0 {
				names = []string{"all"}
			}
			svcs, err := a.Find(names)
			if err != nil {
				return err
			}
			c, _ := a.Ctx()
			var files []string
			for _, s := range svcs {
				files = append(files, s.Logs(c)...)
			}
			var existing []string
			for _, f := range files {
				if _, err := os.Stat(f); err == nil {
					existing = append(existing, f)
				}
			}
			if len(existing) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No logs yet.")
				return nil
			}
			args2 := []string{"-n", strconv.Itoa(lines)}
			if follow {
				args2 = append(args2, "-F")
			}
			name, tailArgs := sys.Command(a.Env.Bin("tail"), append(args2, existing...))
			t := exec.CommandContext(cmd.Context(), name, tailArgs...)
			t.Stdout, t.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
			return t.Run()
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines")
	cmd.Flags().IntVarP(&lines, "lines", "n", 40, "number of lines")
	return cmd
}

func openCmd() *cobra.Command {
	var https bool
	cmd := &cobra.Command{
		Use:   "open [site]",
		Short: "Open a site in the Android browser",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load(cmd)
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			url, err := a.OpenURL(name, https)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), url)
			return termux.OpenURL(cmd.Context(), a.R, url)
		},
	}
	cmd.Flags().BoolVar(&https, "https", false, "open the HTTPS URL")
	return cmd
}

func shareCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "share on|off",
		Short:     "Let other devices on your Wi-Fi open your sites",
		ValidArgs: []string{"on", "off"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load(cmd)
			if err != nil {
				return err
			}
			urls, err := a.Share(cmd.Context(), args[0] == "on")
			if err != nil {
				return err
			}
			if args[0] == "off" {
				fmt.Fprintln(cmd.OutOrStdout(), "Sharing off: sites listen on 127.0.0.1 only.")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Sharing on. From another device on the same Wi-Fi open:")
			printLines(cmd, indent(urls))
			fmt.Fprintln(cmd.OutOrStdout(), "The database and /adminer stay reachable from this phone only. Turn off with: hampp share off")
			return nil
		},
	}
}

func serveCmd() *cobra.Command {
	var port int
	var root string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Quick mode: PHP's built-in server in the foreground (Ctrl+C to stop)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := load(cmd)
			if err != nil {
				return err
			}
			if root == "" {
				root, _ = os.Getwd()
			}
			if err := config.ValidPort(port); err != nil {
				return err
			}
			addr := "127.0.0.1:" + strconv.Itoa(port)
			fmt.Fprintf(cmd.OutOrStdout(), "Serving %s at http://%s/ (single-threaded, for quick tests)\n", a.Paths.Short(root), addr)
			return a.R.Stream(cmd.Context(), cmd.OutOrStdout(), a.Env.Bin("php"), "-S", addr, "-t", root)
		},
	}
	cmd.Flags().IntVarP(&port, "port", "p", 8000, "port")
	cmd.Flags().StringVarP(&root, "root", "t", "", "document root (default: current folder)")
	return cmd
}

func doctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the setup and explain how to fix problems",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := load(cmd)
			if err != nil {
				return err
			}
			if fix {
				printLines(cmd, a.CleanV1())
			}
			if printChecks(cmd, a.Doctor(cmd.Context())) {
				return fmt.Errorf("some checks failed")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "clean up HamppServer v1 leftovers")
	return cmd
}

func uninstallCmd() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop services and remove hampp's generated files (keeps ~/www and databases)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := load(cmd)
			if err != nil {
				return err
			}
			printLines(cmd, a.Uninstall(cmd.Context(), purge))
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete config, passwords, the local CA and Adminer")
	return cmd
}

func indent(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "  " + strings.TrimRight(l, " ")
	}
	return out
}
