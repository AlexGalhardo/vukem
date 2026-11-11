//go:build windows

package engine

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestKillProcessGroupKillsChildren(t *testing.T) {
	t.Parallel()

	// cmd.exe starts ping.exe as a child that outlives a plain Kill of cmd.
	cmd := exec.CommandContext(t.Context(), "cmd.exe", "/c", "ping -n 60 127.0.0.1 > nul")
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = cmd.Process.Kill() })

	pid := strconv.Itoa(cmd.Process.Pid)

	var child string

	for range 50 {
		out, err := exec.Command("powershell", "-NoProfile", "-Command",
			"(Get-CimInstance Win32_Process -Filter 'ParentProcessId="+pid+"').ProcessId").Output()
		if err != nil {
			t.Fatal(err)
		}

		if child = strings.TrimSpace(string(out)); child != "" {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if child == "" {
		t.Fatal("the helper never started a child")
	}

	if !alive(t, child) {
		t.Fatal("child not running before the kill")
	}

	if err := killProcessGroup(cmd); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for alive(t, child) {
		if time.Now().After(deadline) {
			t.Fatalf("child %s still running 5s after killProcessGroup", child)
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// alive reads tasklist, whose "no match" message is localized: a live
// process is recognized by its image name instead.
func alive(t *testing.T, pid string) bool {
	t.Helper()

	out, err := exec.Command("tasklist", "/FI", "PID eq "+pid, "/NH").Output()
	if err != nil {
		t.Fatal(err)
	}

	return strings.Contains(strings.ToLower(string(out)), "ping.exe")
}
