package rcfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyIsIdempotentAndPreservesContent(t *testing.T) {
	block := Block(Options{NodeBin: "/h/.local/share/hampp/node/current/bin", CABundle: "/h/.local/share/hampp/ca/bundle.pem", PHPScanDir: ":/h/php.d:/u/php.d", AfterPath: []string{"/h/.composer/vendor/bin"}})
	user := "alias ll='ls -l'\nexport EDITOR=nano"
	once := Apply(user, block)
	twice := Apply(once, block)
	if once != twice {
		t.Fatal("not idempotent")
	}
	if !strings.HasPrefix(once, user+"\n") || strings.Count(once, Begin) != 1 {
		t.Fatalf("content:\n%s", once)
	}
	removed, ok := Remove(once)
	if !ok || removed != user+"\n" {
		t.Fatalf("remove: %q", removed)
	}
	// Block in the middle of a file is replaced in place of being duplicated.
	mid := "a\n" + block + "b\n"
	if got := Apply(mid, block); strings.Count(got, Begin) != 1 || !strings.Contains(got, "a\nb\n") {
		t.Fatalf("mid: %q", got)
	}
	// An unterminated block is left alone.
	broken := "x\n" + Begin + "\nstuff\n"
	if _, ok := Remove(broken); ok {
		t.Fatal("unterminated block must not be removed")
	}
}

func TestInstallUninstallFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".bashrc")
	os.WriteFile(p, []byte("echo hi\n"), 0o644)
	block := Block(Options{NodeBin: "/n/bin", CABundle: "/ca.pem", PHPScanDir: ":/php.d"})
	if changed, err := Install(p, block); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if changed, _ := Install(p, block); changed {
		t.Fatal("second install must be a no-op")
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o644 {
		t.Fatal("permissions must be preserved")
	}
	if removed, err := Uninstall(p); err != nil || !removed {
		t.Fatal(removed, err)
	}
	if removed, _ := Uninstall(filepath.Join(t.TempDir(), "missing")); removed {
		t.Fatal("missing file reports nothing removed")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "echo hi\n" {
		t.Fatalf("%q", b)
	}
}

// The block must be valid POSIX sh and must not add PATH twice.
func TestBlockRunsInSh(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	bundle := filepath.Join(dir, "bundle.pem")
	os.WriteFile(bundle, nil, 0o644)
	blk := Block(Options{NodeBin: "/n/bin", CABundle: bundle, PHPScanDir: ":/a/php.d:/b/php.d", AfterPath: []string{"/c/bin"}})
	script := blk + blk + `echo "$PATH|$SSL_CERT_FILE|$PHP_INI_SCAN_DIR"`
	out, err := exec.Command(sh, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if strings.Count(got, "/n/bin") != 1 || !strings.HasPrefix(got, "/n/bin:") || !strings.HasSuffix(got, "|"+bundle+"|:/a/php.d:/b/php.d") ||
		strings.Count(got, "/c/bin") != 1 || !strings.Contains(got, ":/c/bin|") {
		t.Fatalf("got %q", got)
	}
}
