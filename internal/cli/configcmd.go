//go:build unix

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/yourchocomate/hampp/internal/app"
	"github.com/yourchocomate/hampp/internal/config"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Read or change settings (then run `hampp restart`)"}
	cmd.AddCommand(
		&cobra.Command{Use: "path", Short: "Print the config file path",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				fmt.Fprintln(cmd.OutOrStdout(), a.Paths.ConfigFile())
				return nil
			})},
		&cobra.Command{Use: "get [key]", Short: "Print a setting (e.g. web.port) or all settings", Args: cobra.MaximumNArgs(1),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
				m, err := toMap(a.Cfg)
				if err != nil {
					return err
				}
				if len(args) == 0 {
					b, _ := toml.Marshal(a.Cfg)
					fmt.Fprint(cmd.OutOrStdout(), string(b))
					return nil
				}
				v, err := getKey(m, args[0])
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), v)
				return nil
			})},
		&cobra.Command{Use: "set <key> <value>", Short: "Change a setting (e.g. web.port 8081)", Args: cobra.ExactArgs(2),
			RunE: run(func(cmd *cobra.Command, a *app.App, args []string) error {
				if err := a.RequireInit(); err != nil {
					return err
				}
				cfg, err := SetKey(a.Cfg, args[0], args[1])
				if err != nil {
					return err
				}
				a.Cfg = cfg
				if err := a.SaveConfig(); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\nApply with: hampp restart\n", args[0], args[1])
				return nil
			})},
		&cobra.Command{Use: "edit", Short: "Open config.toml in $EDITOR (default nano)",
			RunE: run(func(cmd *cobra.Command, a *app.App, _ []string) error {
				editor := os.Getenv("EDITOR")
				if editor == "" {
					editor = "nano"
				}
				e := exec.Command(editor, a.Paths.ConfigFile())
				e.Stdin, e.Stdout, e.Stderr = os.Stdin, os.Stdout, os.Stderr
				if err := e.Run(); err != nil {
					return err
				}
				if _, err := config.Load(a.Paths.ConfigFile(), a.Env.Home); err != nil {
					return fmt.Errorf("config.toml has a problem, please fix it: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Saved. Apply with: hampp restart")
				return nil
			})},
	)
	return cmd
}

func toMap(c config.Config) (map[string]any, error) {
	b, err := toml.Marshal(c)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, toml.Unmarshal(b, &m)
}

func getKey(m map[string]any, key string) (any, error) {
	parts := strings.Split(key, ".")
	cur := any(m)
	for _, p := range parts {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unknown setting %q", key)
		}
		if cur, ok = mm[p]; !ok {
			return nil, fmt.Errorf("unknown setting %q", key)
		}
	}
	return cur, nil
}

// SetKey sets a dotted key, converting value to the existing field's type.
func SetKey(c config.Config, key, value string) (config.Config, error) {
	m, err := toMap(c)
	if err != nil {
		return c, err
	}
	parts := strings.Split(key, ".")
	parent := m
	for _, p := range parts[:len(parts)-1] {
		next, ok := parent[p].(map[string]any)
		if !ok {
			return c, fmt.Errorf("unknown setting %q", key)
		}
		parent = next
	}
	last := parts[len(parts)-1]
	old, ok := parent[last]
	if !ok {
		return c, fmt.Errorf("unknown setting %q", key)
	}
	switch old.(type) {
	case bool:
		v, err := strconv.ParseBool(value)
		if err != nil {
			return c, fmt.Errorf("%s must be true or false", key)
		}
		parent[last] = v
	case int64:
		v, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return c, fmt.Errorf("%s must be a number", key)
		}
		parent[last] = v
	case string:
		parent[last] = value
	default:
		return c, fmt.Errorf("%s cannot be set from the command line; use `hampp config edit`", key)
	}
	b, err := toml.Marshal(m)
	if err != nil {
		return c, err
	}
	var out config.Config
	if err := toml.Unmarshal(b, &out); err != nil {
		return c, err
	}
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(out.Web.Root, "~/") {
		out.Web.Root = home + out.Web.Root[1:]
	}
	return out, out.Validate()
}
