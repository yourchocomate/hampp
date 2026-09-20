package sys

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Fake is a scripted Runner for tests. Responses are keyed by the joined command line.
type Fake struct {
	mu        sync.Mutex
	Responses map[string]FakeResult
	Paths     map[string]string
	Calls     []string
	Stdins    []string
}

type FakeResult struct {
	Out string
	Err error
}

func (f *Fake) record(stdin, name string, args []string) FakeResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.Calls = append(f.Calls, line)
	f.Stdins = append(f.Stdins, stdin)
	if r, ok := f.Responses[line]; ok {
		return r
	}
	// Prefix match lets tests stub a command regardless of trailing args.
	for k, r := range f.Responses {
		if strings.HasSuffix(k, "*") && strings.HasPrefix(line, strings.TrimSuffix(k, "*")) {
			return r
		}
	}
	return FakeResult{}
}

func (f *Fake) Output(_ context.Context, stdin, name string, args ...string) (string, error) {
	r := f.record(stdin, name, args)
	return r.Out, r.Err
}

func (f *Fake) Stream(_ context.Context, w io.Writer, name string, args ...string) error {
	r := f.record("", name, args)
	if r.Out != "" {
		fmt.Fprint(w, r.Out)
	}
	return r.Err
}

func (f *Fake) LookPath(name string) (string, error) {
	if p, ok := f.Paths[name]; ok {
		return p, nil
	}
	return "", fmt.Errorf("%s: not found", name)
}
