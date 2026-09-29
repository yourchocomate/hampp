//go:build unix

// Package proc starts detached daemons, tracks pidfiles and waits for health.
package proc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yourchocomate/hampp/internal/sys"
)

// Spawn starts a process in its own session so it survives hampp exiting and
// is not killed with the terminal. Output goes to logFile. If pidFile is set,
// the child's pid is written there.
func Spawn(name string, args, env []string, logFile, pidFile string) (int, error) {
	log, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer log.Close()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return 0, err
	}
	defer devnull.Close()

	// sys.Command applies the Android exec workaround; `exec "$0" "$@"` in the
	// wrapper keeps the pid, so pidfiles stay correct.
	run, runArgs := sys.Command(name, args)
	cmd := exec.Command(run, runArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, log, log
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		if sys.IsPermission(err) && sys.Degrade() {
			run, runArgs = sys.Command(name, args)
			cmd = exec.Command(run, runArgs...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, log, log
			cmd.Env = append(os.Environ(), env...)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			err = cmd.Start()
		}
		if err != nil {
			return 0, err
		}
	}
	pid := cmd.Process.Pid
	if pidFile != "" {
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
			_ = cmd.Process.Kill()
			return 0, err
		}
	}
	// Reap the child in the background if it exits while hampp is still running.
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

// ReadPID returns the pid in a pidfile, or 0 if absent/invalid.
func ReadPID(pidFile string) int {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// Alive reports whether pid is a running, non-zombie process owned by this
// user. kill(pid, 0) answers EPERM for other users' processes; on Android that
// means another app, typically one that reused the pid of a server Android
// killed while Termux was in the background. That is not our process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	// A zombie still answers kill(0); check /proc when available (Linux/Android).
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if i := strings.LastIndexByte(string(b), ')'); i > 0 && i+2 < len(b) && b[i+2] == 'Z' {
			return false
		}
	}
	return true
}

// Matches reports whether pid's command line (or name) contains one of names.
// It guards against pid reuse by this user's own unrelated processes, which a
// stale pidfile would otherwise make hampp signal. Without /proc (macOS, where
// only the tests run) it cannot tell and says yes.
func Matches(pid int, names ...string) bool {
	if len(names) == 0 {
		return true
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		if _, serr := os.Stat("/proc/self/cmdline"); serr != nil {
			return true
		}
		return false
	}
	comm, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	hay := strings.ToLower(strings.ReplaceAll(string(cmdline), "\x00", " ") + " " + string(comm))
	for _, n := range names {
		if strings.Contains(hay, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// Running returns the live pid from a pidfile, or 0. Stale pidfiles are removed.
func Running(pidFile string) int { return RunningAs(pidFile) }

// RunningAs returns the pid in pidFile if it is a live process of this user
// whose command line contains one of names; otherwise it removes the stale
// pidfile and returns 0.
func RunningAs(pidFile string, names ...string) int {
	pid := ReadPID(pidFile)
	if pid == 0 {
		return 0
	}
	if !Alive(pid) || !Matches(pid, names...) {
		_ = os.Remove(pidFile)
		return 0
	}
	return pid
}

// Proc is one of this user's processes, read from /proc.
type Proc struct {
	PID     int
	PPID    int
	Cmdline string
}

// Own lists this user's processes. Android hides other apps' processes from
// /proc, and kill(pid, 0) filters out anything else we could not signal.
func Own() []Proc {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var out []Proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self || !Alive(pid) {
			continue
		}
		cmd, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			continue
		}
		stat, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		out = append(out, Proc{PID: pid, PPID: parsePPID(string(stat)), Cmdline: strings.ReplaceAll(strings.TrimRight(string(cmd), "\x00"), "\x00", " ")})
	}
	return out
}

// parsePPID reads the parent pid from /proc/<pid>/stat ("pid (comm) state ppid ...").
func parsePPID(stat string) int {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(stat[i+1:])
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}

// KillLeftovers SIGKILLs this user's processes for which match returns true
// and reports their pids. It is for processes a server left behind when
// Android killed its master (for example an nginx worker still holding the
// port), which nothing else would ever stop.
func KillLeftovers(match func(p Proc, byPID map[int]Proc) bool) []int {
	procs := Own()
	byPID := make(map[int]Proc, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	var killed []int
	for _, p := range procs {
		if match(p, byPID) && syscall.Kill(p.PID, syscall.SIGKILL) == nil {
			killed = append(killed, p.PID)
		}
	}
	if len(killed) > 0 {
		time.Sleep(200 * time.Millisecond) // let the kernel release their sockets
	}
	return killed
}

// StartTime returns when the pidfile was written, as an approximation of uptime.
func StartTime(pidFile string) time.Time {
	st, err := os.Stat(pidFile)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// Stop sends sig, waits up to grace for exit, then SIGKILLs.
func Stop(pid int, sig syscall.Signal, grace time.Duration) error {
	if !Alive(pid) {
		return nil
	}
	if err := syscall.Kill(pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EPERM) {
			return nil // gone, or no longer ours
		}
		return err
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !Alive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	time.Sleep(200 * time.Millisecond)
	if Alive(pid) {
		return fmt.Errorf("pid %d did not exit", pid)
	}
	return nil
}

// WaitDial polls until network/addr accepts connections or ctx expires.
func WaitDial(ctx context.Context, network, addr string) error {
	delay := 50 * time.Millisecond
	for {
		c, err := net.DialTimeout(network, addr, time.Second)
		if err == nil {
			c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s %s not ready: %w", network, addr, err)
		case <-time.After(delay):
		}
		if delay < 500*time.Millisecond {
			delay *= 2
		}
	}
}

// PortFree reports whether host:port can be bound right now.
func PortFree(host string, port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// CountByNames counts processes whose command name is in names (Linux /proc).
// Used by doctor to estimate usage of Android's phantom-process budget.
func CountByNames(names ...string) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return -1
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	n := 0
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/comm")
		if err == nil && want[strings.TrimSpace(string(b))] {
			n++
		}
	}
	return n
}
