// Package sys wraps process execution so higher layers can be tested with fakes.
package sys

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Runner executes external programs.
type Runner interface {
	// Output runs a command and returns its combined stdout. Stderr is included in the error.
	Output(ctx context.Context, stdin string, name string, args ...string) (string, error)
	// Stream runs a command with stdout/stderr attached to w.
	Stream(ctx context.Context, w io.Writer, name string, args ...string) error
	LookPath(name string) (string, error)
}

// Exec is the real Runner.
type Exec struct {
	Env []string // extra environment, appended to os.Environ()
}

func (e Exec) cmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	if len(e.Env) > 0 {
		c.Env = append(os.Environ(), e.Env...)
	}
	return c
}

func (e Exec) Output(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	c := e.cmd(ctx, name, args...)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return out.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
	}
	return out.String(), nil
}

func (e Exec) Stream(ctx context.Context, w io.Writer, name string, args ...string) error {
	c := e.cmd(ctx, name, args...)
	c.Stdout, c.Stderr, c.Stdin = w, w, os.Stdin
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func (Exec) LookPath(name string) (string, error) { return exec.LookPath(name) }
