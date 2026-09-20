// Package site discovers the sites hampp serves: the default web root, every
// subfolder of it ("parked", Valet-style) and explicitly linked projects.
package site

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yourchocomate/hampp/internal/config"
)

// TLD is the reserved loopback domain (RFC 6761 §6.3). Chromium and Firefox
// resolve every *.localhost name to 127.0.0.1 without DNS.
const TLD = "localhost"

type Site struct {
	Name    string // DNS label, "" for the default site
	Host    string // localhost or <name>.localhost
	Dir     string // project folder
	DocRoot string // served directory
	Linked  bool
	Default bool
}

// Discover returns the default site first, then parked and linked sites sorted by name.
// Problems with individual folders are returned as warnings, not errors.
func Discover(webRoot string, links []config.Link) ([]Site, []string) {
	sites := []Site{{Host: TLD, Dir: webRoot, DocRoot: webRoot, Default: true}}
	var warnings []string
	byName := map[string]Site{}

	entries, err := os.ReadDir(webRoot)
	if err != nil && !os.IsNotExist(err) {
		warnings = append(warnings, fmt.Sprintf("cannot read %s: %v", webRoot, err))
	}
	for _, e := range entries {
		if !isDirEntry(webRoot, e) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := Sanitize(e.Name())
		if name == "" {
			warnings = append(warnings, fmt.Sprintf("skipping %q: no usable characters for a hostname", e.Name()))
			continue
		}
		dir := filepath.Join(webRoot, e.Name())
		if prev, dup := byName[name]; dup {
			warnings = append(warnings, fmt.Sprintf("%q and %q both map to %s.%s; keeping %q", filepath.Base(prev.Dir), e.Name(), name, TLD, filepath.Base(prev.Dir)))
			continue
		}
		byName[name] = Site{Name: name, Host: name + "." + TLD, Dir: dir, DocRoot: DetectDocRoot(dir)}
	}
	for _, l := range links {
		if _, dup := byName[l.Name]; dup {
			warnings = append(warnings, fmt.Sprintf("link %q overrides the parked folder with the same name", l.Name))
		}
		root := DetectDocRoot(l.Path)
		if l.Root != "" {
			root = filepath.Join(l.Path, l.Root)
		}
		if _, err := os.Stat(root); err != nil {
			warnings = append(warnings, fmt.Sprintf("link %q: %v", l.Name, err))
		}
		byName[l.Name] = Site{Name: l.Name, Host: l.Name + "." + TLD, Dir: l.Path, DocRoot: root, Linked: true}
	}

	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sites = append(sites, byName[n])
	}
	return sites, warnings
}

func isDirEntry(parent string, e os.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink != 0 {
		st, err := os.Stat(filepath.Join(parent, e.Name()))
		return err == nil && st.IsDir()
	}
	return false
}

// Sanitize turns a folder name into a DNS label: lowercase, [a-z0-9-], no
// leading/trailing dash, at most 63 characters.
func Sanitize(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	return s
}

// DetectDocRoot picks the public folder used by common frameworks:
// public/ (Laravel, Symfony, Slim), web/ (Drupal/Craft), else the folder itself.
func DetectDocRoot(dir string) string {
	for _, sub := range []string{"public", "web"} {
		p := filepath.Join(dir, sub)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return dir
}

// Hosts returns the certificate SANs for a site.
func (s Site) Hosts() []string {
	if s.Default {
		return []string{"localhost", "127.0.0.1", "::1"}
	}
	return []string{s.Host, "localhost", "127.0.0.1", "::1"}
}

// CertName is the file stem for the site's certificate.
func (s Site) CertName() string {
	if s.Default {
		return "localhost"
	}
	return s.Name
}

// URL builds a browser URL for the site.
func (s Site) URL(port int, https bool) string {
	scheme := "http"
	if https {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d/", scheme, s.Host, port)
}

// OnSharedStorage reports whether a path is on Android shared storage (FUSE),
// where npm/composer symlinks and exec bits do not work.
func OnSharedStorage(p string) bool {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		real = p
	}
	for _, prefix := range []string{"/sdcard", "/storage/", "/mnt/sdcard"} {
		if strings.HasPrefix(real, prefix) || strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return strings.Contains(p, "/storage/shared")
}
