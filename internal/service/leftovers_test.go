//go:build unix

package service

import (
	"testing"

	"github.com/yourchocomate/hampp/internal/proc"
)

func TestLeftoversOnlyMatchHamppProcesses(t *testing.T) {
	conf := "/h/.local/share/hampp/conf/nginx.conf"
	l := leftovers{conf: conf, workers: []string{"nginx: worker process"}, masters: []string{"nginx: master process"}}
	procs := []proc.Proc{
		// hampp's master, started with hampp's config.
		{PID: 10, PPID: 1, Cmdline: "nginx: master process /usr/bin/nginx -p /h/.local/state/hampp -c " + conf},
		// A worker whose master Android killed: reparented to init.
		{PID: 11, PPID: 1, Cmdline: "nginx: worker process"},
		// The user's own nginx with its own config and a live master.
		{PID: 20, PPID: 1, Cmdline: "nginx: master process /usr/bin/nginx -c /usr/etc/nginx/nginx.conf"},
		{PID: 21, PPID: 20, Cmdline: "nginx: worker process"},
		// Unrelated.
		{PID: 30, PPID: 1, Cmdline: "bash"},
	}
	byPID := map[int]proc.Proc{}
	for _, p := range procs {
		byPID[p.PID] = p
	}
	want := map[int]bool{10: true, 11: true, 20: false, 21: false, 30: false}
	for _, p := range procs {
		if got := l.matches(p, byPID); got != want[p.PID] {
			t.Errorf("pid %d (%s): matches=%v, want %v", p.PID, p.Cmdline, got, want[p.PID])
		}
	}
	// A worker whose parent pid points at something that is not a master is orphaned too.
	byPID[40] = proc.Proc{PID: 40, PPID: 1, Cmdline: "sh"}
	if !l.matches(proc.Proc{PID: 41, PPID: 40, Cmdline: "nginx: worker process"}, byPID) {
		t.Error("worker under a non-master parent is a leftover")
	}
}
