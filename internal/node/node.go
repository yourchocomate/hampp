// Package node provides nvm-style Node.js version switching on Termux.
//
// Real nvm cannot work on Termux: it refuses to run while $PREFIX is set and it
// downloads glibc builds that do not run on Android's bionic libc. Instead,
// hampp uses the TUR packages nodejs-NN, which install side by side into
// $PREFIX/opt/nodejs-NN, and switches between them with a symlink that the
// user's shell puts first on PATH.
package node

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// System means "use $PREFIX/bin/node from the nodejs or nodejs-lts package".
const System = "system"

// Package returns the TUR package name for a major version.
func Package(major int) string { return fmt.Sprintf("nodejs-%d", major) }

// InstallDir is where TUR installs nodejs-NN.
func InstallDir(prefix string, major int) string {
	return filepath.Join(prefix, "opt", Package(major))
}

// Installed lists majors present in $PREFIX/opt, newest first.
func Installed(prefix string) []int {
	matches, _ := filepath.Glob(filepath.Join(prefix, "opt", "nodejs-*", "bin", "node"))
	var majors []int
	for _, m := range matches {
		name := filepath.Base(filepath.Dir(filepath.Dir(m)))
		if n, err := strconv.Atoi(strings.TrimPrefix(name, "nodejs-")); err == nil {
			majors = append(majors, n)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(majors)))
	return majors
}

// Current returns the active major (0 for system/none) from the symlink.
func Current(link string) (int, bool) {
	target, err := os.Readlink(link)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(target), "nodejs-"))
	return n, err == nil
}

// Use points link at the given major's install dir, or removes it for System.
func Use(prefix, link string, major int) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return err
	}
	if major == 0 {
		if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	dir := InstallDir(prefix, major)
	if _, err := os.Stat(filepath.Join(dir, "bin", "node")); err != nil {
		return fmt.Errorf("node %d is not installed (run: hampp node install %d)", major, major)
	}
	tmp := link + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(dir, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// ltsCodenames maps nvm's lts/<name> aliases to majors.
var ltsCodenames = map[string]int{
	"argon": 4, "boron": 6, "carbon": 8, "dubnium": 10, "erbium": 12, "fermium": 14,
	"gallium": 16, "hydrogen": 18, "iron": 20, "jod": 22, "krypton": 24,
}

var versionRe = regexp.MustCompile(`^v?(\d+)(?:\.(\d+|x))?(?:\.(\d+|x))?$`)

// Spec is a parsed .nvmrc / .node-version value.
type Spec struct {
	Raw    string
	Major  int  // exact major, 0 if Latest/LTS
	Exact  bool // a full x.y.z was requested
	Latest bool // "node" / "latest" / "current"
	LTS    bool // "lts/*"
	System bool
}

// ParseSpec understands the forms nvm accepts in .nvmrc.
func ParseSpec(s string) (Spec, error) {
	raw := strings.TrimSpace(s)
	v := strings.ToLower(raw)
	sp := Spec{Raw: raw}
	switch {
	case v == "":
		return sp, errors.New("empty version")
	case v == "system":
		sp.System = true
	case v == "node" || v == "latest" || v == "current" || v == "stable":
		sp.Latest = true
	case v == "lts/*" || v == "lts" || v == "lts/latest":
		sp.LTS = true
	case strings.HasPrefix(v, "lts/"):
		n, ok := ltsCodenames[strings.TrimPrefix(v, "lts/")]
		if !ok {
			return sp, fmt.Errorf("unknown LTS codename %q", raw)
		}
		sp.Major = n
	default:
		m := versionRe.FindStringSubmatch(v)
		if m == nil {
			return sp, fmt.Errorf("unsupported version %q (use a major like 22, v22.11.0, lts/*, or node)", raw)
		}
		sp.Major, _ = strconv.Atoi(m[1])
		sp.Exact = m[3] != "" && m[3] != "x"
	}
	return sp, nil
}

// Resolve picks a major from the candidates (installed or available, newest first).
func (sp Spec) Resolve(candidates []int) (int, error) {
	switch {
	case sp.System:
		return 0, nil
	case sp.Latest:
		if len(candidates) == 0 {
			return 0, errors.New("no Node.js versions available")
		}
		return candidates[0], nil
	case sp.LTS:
		for _, c := range candidates {
			if c%2 == 0 { // Node LTS lines are the even majors
				return c, nil
			}
		}
		return 0, errors.New("no LTS Node.js version available")
	}
	for _, c := range candidates {
		if c == sp.Major {
			return c, nil
		}
	}
	return 0, fmt.Errorf("Node.js %d is not available from TUR (available: %s)", sp.Major, joinInts(candidates))
}

// FindRC walks up from dir looking for .nvmrc or .node-version.
func FindRC(dir string) (spec Spec, file string, err error) {
	for d := dir; ; d = filepath.Dir(d) {
		for _, name := range []string{".nvmrc", ".node-version"} {
			p := filepath.Join(d, name)
			if b, rerr := os.ReadFile(p); rerr == nil {
				line := firstLine(string(b))
				sp, perr := ParseSpec(line)
				return sp, p, perr
			}
		}
		if parent := filepath.Dir(d); parent == d {
			break
		}
	}
	return Spec{}, "", errors.New("no .nvmrc or .node-version found in this folder or its parents")
}

func firstLine(s string) string {
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" && !strings.HasPrefix(l, "#") {
			return l
		}
	}
	return ""
}

func joinInts(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = strconv.Itoa(n)
	}
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}
