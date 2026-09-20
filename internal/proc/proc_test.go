//go:build unix

package proc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSpawnRunningStop(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "sleep.pid")
	pid, err := Spawn("sleep", []string{"30"}, nil, filepath.Join(dir, "log"), pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if Running(pidFile) != pid {
		t.Fatal("pidfile should point at the live child")
	}
	// Setsid: the child leads its own session, so closing hampp's terminal won't kill it.
	// syscall.Getsid is not exported on Linux, x/sys/unix has it everywhere.
	if sid, _ := unix.Getsid(pid); sid != pid {
		t.Fatalf("child sid %d, want %d", sid, pid)
	}
	if err := Stop(pid, syscall.SIGTERM, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if Running(pidFile) != 0 {
		t.Fatal("process should be gone")
	}
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatal("stale pidfile should be removed by Running")
	}
}

func TestStopEscalatesToKill(t *testing.T) {
	dir := t.TempDir()
	// Ignores SIGTERM; Stop must fall back to SIGKILL after the grace period.
	pid, err := Spawn("sh", []string{"-c", "trap '' TERM; sleep 30"}, nil, filepath.Join(dir, "log"), "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	if err := Stop(pid, syscall.SIGTERM, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("took too long")
	}
}

func TestReadPIDGarbage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.pid")
	os.WriteFile(p, []byte("not a pid"), 0o600)
	if ReadPID(p) != 0 || Running(p) != 0 {
		t.Fatal("garbage pidfile")
	}
	os.WriteFile(p, []byte("999999\n"), 0o600)
	if Running(p) != 0 {
		t.Fatal("dead pid")
	}
}

func TestWaitDialAndPortFree(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	if PortFree("127.0.0.1", port) {
		t.Fatal("port is in use")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := WaitDial(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(port)); err != nil {
		t.Fatal(err)
	}
	l.Close()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	if err := WaitDial(ctx2, "tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
		t.Fatal("closed port must time out")
	}
}
