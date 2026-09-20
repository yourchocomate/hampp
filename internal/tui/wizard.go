//go:build unix

package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"charm.land/huh/v2"

	"github.com/yourchocomate/hampp/internal/app"
	"github.com/yourchocomate/hampp/internal/config"
)

// Wizard asks the setup questions one screen at a time and returns the options.
// ok is false if the user cancelled.
func Wizard(a *app.App, ascii bool) (app.InitOptions, bool, error) {
	o := a.DefaultInitOptions()
	root := a.Paths.Short(o.Root)
	node := "0"
	confirm := true

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().Title("hampp setup").
				Description("A local web server for PHP sites, inside Termux.\n\nYour sites will live in ~/www and open at\nhttp://localhost:8080 in your phone's browser."),
		),
		huh.NewGroup(
			huh.NewSelect[string]().Title("Web server").
				Options(
					huh.NewOption("Apache  (recommended, .htaccess works)", config.WebApache),
					huh.NewOption("nginx   (lighter, no .htaccess)", config.WebNginx),
				).Value(&o.Web),
		),
		huh.NewGroup(
			huh.NewSelect[string]().Title("Database").
				Options(
					huh.NewOption("MariaDB  (MySQL-compatible)", config.DBMariaDB),
					huh.NewOption("SQLite   (no server, a single file)", config.DBSQLite),
					huh.NewOption("None", config.DBNone),
				).Value(&o.DB),
		),
		huh.NewGroup(
			huh.NewInput().Title("Web root").
				Description("Keep it inside Termux (~) so npm and composer work.").
				Value(&root).
				Validate(func(s string) error {
					if !strings.HasPrefix(s, "~/") && !strings.HasPrefix(s, "/") {
						return errors.New("use a path like ~/www")
					}
					return nil
				}),
		),
		huh.NewGroup(
			huh.NewConfirm().Title("Enable HTTPS?").
				Description("Creates a local certificate authority. You then trust it once in Android Settings (hampp guides you).").
				Affirmative("Yes").Negative("Not now").Value(&o.HTTPS),
		),
		huh.NewGroup(
			huh.NewSelect[string]().Title("Node.js").
				Description("Installed from the Termux User Repository; switch later with `hampp node`.").
				Options(
					huh.NewOption("None for now", "0"),
					huh.NewOption("24 (LTS)", "24"),
					huh.NewOption("22 (LTS)", "22"),
					huh.NewOption("20 (LTS)", "20"),
				).Value(&node),
		),
		huh.NewGroup(
			huh.NewConfirm().TitleFunc(func() string { return "Install and start?" }, &o).
				DescriptionFunc(func() string {
					return fmt.Sprintf("Will run:\n  %s\n\nThen start %s, php-fpm%s.",
						strings.Join(a.PM.InstallArgs(a.Packages(o)...), " "), o.Web, dbSuffix(o.DB))
				}, &o).
				Affirmative("Install").Negative("Cancel").Value(&confirm),
		),
	).WithShowHelp(true).WithAccessible(ascii).WithWidth(44)
	if !ascii {
		form = form.WithTheme(huh.ThemeFunc(huh.ThemeCharm))
	}
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return o, false, nil
		}
		return o, false, err
	}
	if !confirm {
		return o, false, nil
	}
	if strings.HasPrefix(root, "~/") {
		root = a.Env.Home + root[1:]
	}
	o.Root = root
	o.Node, _ = strconv.Atoi(node)
	o.RCHook, o.Start = true, true
	return o, true, nil
}

func dbSuffix(db string) string {
	if db == config.DBMariaDB {
		return " and MariaDB"
	}
	return ""
}
