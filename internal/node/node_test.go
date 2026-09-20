package node

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSpecAndResolve(t *testing.T) {
	cands := []int{24, 22, 20, 18, 16, 12} // newest first, like TUR
	cases := []struct {
		in    string
		want  int
		exact bool
	}{
		{"22", 22, false}, {"v20.11.0", 20, true}, {"20.x", 20, false}, {"v18", 18, false},
		{"lts/*", 24, false}, {"lts/iron", 20, false}, {"lts/jod", 22, false},
		{"node", 24, false}, {"latest", 24, false}, {"system", 0, false}, {" 16 \n", 16, false},
	}
	for _, c := range cases {
		sp, err := ParseSpec(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		got, err := sp.Resolve(cands)
		if err != nil || got != c.want || sp.Exact != c.exact {
			t.Errorf("%q → %d exact=%v err=%v; want %d exact=%v", c.in, got, sp.Exact, err, c.want, c.exact)
		}
	}
	for _, bad := range []string{"", "lts/unknown", "banana", "v"} {
		if _, err := ParseSpec(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	sp, _ := ParseSpec("14")
	if _, err := sp.Resolve(cands); err == nil {
		t.Error("14 is not in TUR; must error")
	}
	sp, _ = ParseSpec("lts/*")
	if _, err := sp.Resolve([]int{25, 23}); err == nil {
		t.Error("odd majors are not LTS")
	}
}

func TestFindRCWalksUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "app", "src", "x")
	os.MkdirAll(deep, 0o755)
	os.WriteFile(filepath.Join(root, "app", ".nvmrc"), []byte("# comment\nlts/jod\n"), 0o644)
	sp, file, err := FindRC(deep)
	if err != nil || sp.Major != 22 || file != filepath.Join(root, "app", ".nvmrc") {
		t.Fatal(sp, file, err)
	}
	os.WriteFile(filepath.Join(root, "app", "src", ".node-version"), []byte("20.18.1"), 0o644)
	sp, _, _ = FindRC(deep)
	if sp.Major != 20 || !sp.Exact {
		t.Fatal("nearest file wins", sp)
	}
}

func TestInstalledUseCurrent(t *testing.T) {
	prefix := t.TempDir()
	for _, m := range []int{20, 22} {
		bin := filepath.Join(InstallDir(prefix, m), "bin")
		os.MkdirAll(bin, 0o755)
		os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755)
	}
	if got := Installed(prefix); len(got) != 2 || got[0] != 22 {
		t.Fatalf("installed: %v", got)
	}
	link := filepath.Join(t.TempDir(), "node", "current")
	if _, ok := Current(link); ok {
		t.Fatal("nothing active yet")
	}
	if err := Use(prefix, link, 20); err != nil {
		t.Fatal(err)
	}
	if m, ok := Current(link); !ok || m != 20 {
		t.Fatal(m, ok)
	}
	if err := Use(prefix, link, 22); err != nil {
		t.Fatal(err)
	}
	if m, _ := Current(link); m != 22 {
		t.Fatal("switch failed")
	}
	if _, err := os.Stat(filepath.Join(link, "bin", "node")); err != nil {
		t.Fatal("symlink must resolve to bin/node")
	}
	if err := Use(prefix, link, 18); err == nil {
		t.Fatal("uninstalled version must error")
	}
	if err := Use(prefix, link, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := Current(link); ok {
		t.Fatal("system removes the link")
	}
}
