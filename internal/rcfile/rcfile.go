// Package rcfile manages hampp's marked block in shell startup files.
package rcfile

import (
	"errors"
	"os"
	"strings"

	"github.com/yourchocomate/hampp/internal/config"
)

const (
	Begin = "# >>> hampp >>>"
	End   = "# <<< hampp <<<"
)

// Options are the paths the shell block wires up.
type Options struct {
	NodeBin    string   // hampp's "current" Node.js bin dir, put first on PATH
	CABundle   string   // exported as SSL_CERT_FILE when present
	PHPScanDir string   // PHP_INI_SCAN_DIR value (":<hampp dir>:<user dir>")
	AfterPath  []string // appended to PATH (e.g. Composer's global vendor/bin)
}

// Block builds the shell snippet. It only references paths, so it stays valid
// when node versions or the CA bundle change later.
func Block(o Options) string {
	var b strings.Builder
	b.WriteString(Begin + "\n")
	b.WriteString("# Managed by hampp (Node version switching, Composer, local HTTPS trust). Remove with: hampp uninstall\n")
	b.WriteString(`case ":$PATH:" in *":` + o.NodeBin + `:"*) ;; *) export PATH="` + o.NodeBin + `:$PATH" ;; esac` + "\n")
	for _, p := range o.AfterPath {
		b.WriteString(`case ":$PATH:" in *":` + p + `:"*) ;; *) export PATH="$PATH:` + p + `" ;; esac` + "\n")
	}
	b.WriteString(`[ -f "` + o.CABundle + `" ] && export SSL_CERT_FILE="` + o.CABundle + `"` + "\n")
	// Leading ":" keeps PHP's compiled-in scan dir; hampp's and the user's dirs follow.
	if o.PHPScanDir != "" {
		b.WriteString(`export PHP_INI_SCAN_DIR="` + o.PHPScanDir + `"` + "\n")
	}
	b.WriteString(End + "\n")
	return b.String()
}

// Apply inserts or replaces the block in content, leaving everything else intact.
func Apply(content, block string) string {
	stripped, _ := strip(content)
	if stripped != "" && !strings.HasSuffix(stripped, "\n") {
		stripped += "\n"
	}
	return stripped + block
}

// Remove deletes the block from content.
func Remove(content string) (string, bool) { return strip(content) }

func strip(content string) (string, bool) {
	start := strings.Index(content, Begin)
	if start < 0 {
		return content, false
	}
	end := strings.Index(content[start:], End)
	if end < 0 {
		return content, false // unterminated block: leave the file alone
	}
	end += start + len(End)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[:start] + content[end:], true
}

// Install applies the block to path, creating the file if needed.
func Install(path, block string) (changed bool, err error) {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	next := Apply(string(old), block)
	if next == string(old) {
		return false, nil
	}
	perm := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	return true, config.WriteFileAtomic(path, []byte(next), perm)
}

// Uninstall removes the block from path if present and reports whether it did.
func Uninstall(path string) (bool, error) {
	old, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	next, ok := Remove(string(old))
	if !ok {
		return false, nil
	}
	st, _ := os.Stat(path)
	return true, config.WriteFileAtomic(path, []byte(next), st.Mode().Perm())
}
