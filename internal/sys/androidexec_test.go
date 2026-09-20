package sys

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func reset(t *testing.T, m ExecMode) {
	t.Helper()
	old := Mode()
	execMode.Store(int32(m))
	t.Cleanup(func() { execMode.Store(int32(old)) })
}

func TestCommandDirect(t *testing.T) {
	reset(t, ModeDirect)
	name, args := Command("/p/bin/pkg", []string{"install", "-y", "php"})
	if name != "/p/bin/pkg" || !reflect.DeepEqual(args, []string{"install", "-y", "php"}) {
		t.Fatalf("%s %v", name, args)
	}
	if FallbackName() != "" {
		t.Fatal("no workaround in direct mode")
	}
}

func TestCommandShellKeepsArgumentsIntact(t *testing.T) {
	reset(t, ModeShell)
	name, args := Command("/p/bin/php", []string{"-r", "echo 'a b';"})
	if name != SystemShell {
		t.Fatal(name)
	}
	// `exec "$0" "$@"` needs no quoting and keeps the pid, which pidfiles rely on.
	want := []string{"-c", `exec "$0" "$@"`, "/p/bin/php", "-r", "echo 'a b';"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("%v", args)
	}
}

func TestCommandLinkerResolvesShebang(t *testing.T) {
	reset(t, ModeLinker)
	dir := t.TempDir()
	script := filepath.Join(dir, "pkg")
	os.WriteFile(script, []byte("#!/data/data/com.termux/files/usr/bin/bash\necho hi\n"), 0o755)
	elf := filepath.Join(dir, "httpd")
	os.WriteFile(elf, []byte("\x7fELF..."), 0o755)

	name, args := Command(script, []string{"install"})
	if !strings.HasPrefix(name, "/system/bin/linker") {
		t.Fatal(name)
	}
	if !reflect.DeepEqual(args, []string{"/data/data/com.termux/files/usr/bin/bash", script, "install"}) {
		t.Fatalf("script: %v", args)
	}
	_, args = Command(elf, []string{"-k", "start"})
	if !reflect.DeepEqual(args, []string{elf, "-k", "start"}) {
		t.Fatalf("elf: %v", args)
	}
}

func TestDegradeWalksTheStrategies(t *testing.T) {
	reset(t, ModeAuto)
	seen := []ExecMode{}
	for Degrade() {
		seen = append(seen, Mode())
		if len(seen) > 4 {
			t.Fatal("Degrade must stop")
		}
	}
	// Whatever exists on this machine, it must end up giving up rather than looping.
	if Mode() != modeFailed {
		t.Fatalf("ended in %v", Mode())
	}
}

func TestIsPermission(t *testing.T) {
	if !IsPermission(&os.PathError{Op: "fork/exec", Path: "/p/bin/pkg", Err: os.ErrPermission}) {
		t.Error("EACCES")
	}
	if IsPermission(nil) || IsPermission(os.ErrNotExist) {
		t.Error("false positive")
	}
}
